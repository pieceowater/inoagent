package ctrl

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"app/internal/core/cfg"
	"app/internal/core/messenger"
	"app/internal/core/telegram"
	"app/internal/pkg/agent/ent"
	"app/internal/pkg/agent/svc"
)

// AgentController turns an admitted Telegram message into either a bot
// command or a job for the agent.
type AgentController struct {
	cfg     *cfg.Config
	svc     *svc.AgentService
	msg     *messenger.Messenger
	me      telegram.User
	started time.Time
	people  roster
	batch   *batcher
	logger  *slog.Logger
}

func NewAgentController(c *cfg.Config, service *svc.AgentService, m *messenger.Messenger, me telegram.User, log *slog.Logger) *AgentController {
	ac := &AgentController{cfg: c, svc: service, msg: m, me: me, started: time.Now(), logger: log}
	if c.Debounce > 0 {
		ac.batch = newBatcher(c.Debounce, ac.dispatch)
	}
	return ac
}

func (c *AgentController) log() *slog.Logger { return c.logger }

// Commands are the built-in ones; quick commands from the workspace are
// appended to them in Telegram's menu (see WatchCommands).
var Commands = []telegram.BotCommand{
	{Command: "menu", Description: "показать кнопки рядом со скрепкой"},
	{Command: "limits", Description: "лимиты подписки: 5 часов и неделя"},
	{Command: "status", Description: "жив ли агент и что сейчас делает"},
	{Command: "cancel", Description: "отменить, что делаю или жду в очереди"},
	{Command: "stop", Description: "остановить текущую задачу в этой теме"},
	{Command: "reset", Description: "начать разговор в теме заново"},
	{Command: "compact", Description: "сжать разговор, чтобы меньше жрать лимиты"},
	{Command: "archive", Description: "записать суть в заметку и начать заново"},
	{Command: "context", Description: "насколько разросся разговор"},
	{Command: "plan", Description: "сначала план, потом по кнопке выполню"},
	{Command: "resume", Description: "вернуться к прошлому разговору в теме"},
	{Command: "model", Description: "какая модель отвечает (opus, sonnet, haiku)"},
	{Command: "id", Description: "id чата, темы и твой"},
	{Command: "help", Description: "что я умею"},
}

func (c *AgentController) HandleMessage(ctx context.Context, m *telegram.Message) {
	c.people.see(m.From)
	c.svc.LearnTopic(m)
	text := strings.TrimSpace(m.Text)
	if text == "" {
		text = strings.TrimSpace(m.Caption)
	}
	if cmd, ok := c.buttonCommand(text); ok {
		text = cmd
	}
	if strings.HasPrefix(text, "/") && c.command(ctx, m, text) {
		return
	}
	// A message right after one of ours joins its request, mention or not.
	joining := c.batch != nil && c.batch.has(batchKey{ent.KeyOf(m), m.From.ID}) && !strings.HasPrefix(text, "/")
	if !joining && !c.addressed(m, text) {
		c.svc.Overhear(m, text)
		return
	}
	if c.forSomeoneElse(m, text) {
		c.svc.Overhear(m, text)
		return
	}
	if link, ok := svc.MediaURL(text); ok {
		go c.svc.FetchMedia(m, link)
		return
	}
	if isStatusQuestion(text) && !isCancel(text) {
		if p := c.svc.Progress(ent.KeyOf(m)); p.Busy {
			c.msg.Reply(ctx, m, progressText(p))
			return
		}
	}
	switch {
	case text == "" && !m.HasAttachment():
		// Stickers, service messages: conversation, not requests.
	case c.cfg.DailyLimit > 0 && c.svc.UsageToday() >= c.cfg.DailyLimit:
		c.msg.Reply(ctx, m, fmt.Sprintf("Дневной лимит (%d запросов) исчерпан, продолжу завтра.", c.cfg.DailyLimit))
	case !c.cfg.HasClaudeAuth:
		c.msg.Reply(ctx, m, "Нет токена Claude: запусти `make setup-token` и положи токен в secrets.yml (см. README).")
	default:
		it := ent.BatchItem{Msg: m, Text: c.withCancelNote(m, text)}
		key := batchKey{ent.KeyOf(m), m.From.ID}
		switch {
		case c.batch == nil:
			c.dispatch([]ent.BatchItem{it})
		case isCancel(text):
			c.batch.drop(key)
			c.dispatch([]ent.BatchItem{it})
		default:
			c.batch.add(key, it)
		}
	}
}

// dispatch puts a gathered request into the topic's queue.
func (c *AgentController) dispatch(items []ent.BatchItem) {
	ctx := context.Background()
	m := items[len(items)-1].Msg
	var texts []string
	for _, it := range items {
		if it.Text != "" {
			texts = append(texts, it.Text)
		}
	}
	cancel := false
	for _, it := range items {
		cancel = cancel || isCancel(it.Text)
	}
	if !cancel && c.svc.Inject(ent.KeyOf(m), items) {
		_ = c.msg.Client().SetReaction(ctx, m.Chat.ID, m.MessageID, "👀")
		return
	}
	busy := c.svc.TopicStatus(ent.KeyOf(m)).Busy
	if !c.svc.Enqueue(ent.Job{Msg: items[0].Msg, Text: strings.Join(texts, "\n"), Batch: items}) {
		c.msg.Reply(ctx, m, "Очередь в этой теме полная, подожди, пока закончу.")
	} else if busy {
		// Занят: сообщение принято и ждёт своей очереди.
		_ = c.msg.Client().SetReaction(ctx, m.Chat.ID, m.MessageID, "👀")
	}
}

// addressed: in "mention" mode the agent only answers when called.
func (c *AgentController) addressed(m *telegram.Message, text string) bool {
	if c.cfg.Trigger == "all" || m.Chat.Type == "private" {
		return true
	}
	if r := m.ReplyToMessage; r != nil && r.From != nil && r.From.ID == c.me.ID {
		return true
	}
	return strings.Contains(strings.ToLower(text), "@"+strings.ToLower(c.me.Username))
}

// withCancelNote: a short "стой, не делай" while earlier requests are still
// running or waiting takes them back; the agent gets the message together
// with what was taken back, so it can say what was already done.
func (c *AgentController) withCancelNote(m *telegram.Message, text string) string {
	text = c.stripMention(text)
	if isUrgent(text) && !isCancel(text) {
		return urgentNote(c.svc.Interrupt(ent.KeyOf(m))) + text
	}
	if !isCancel(text) {
		return text
	}
	return cancelNote(c.svc.Interrupt(ent.KeyOf(m))) + text
}

func (c *AgentController) stripMention(text string) string {
	at := "@" + c.me.Username
	i := strings.Index(strings.ToLower(text), strings.ToLower(at))
	if c.me.Username == "" || i < 0 {
		return text
	}
	return strings.TrimSpace(text[:i] + text[i+len(at):])
}

const helpText = `Я **Иноагент**: сисадмин этого сервера и просто помощник.

Пиши задачу обычным текстом. Каждая тема — отдельный разговор со своей памятью.
На сервере у меня root: могу смотреть, чинить, ставить и перезапускать.

/menu — кнопки рядом со скрепкой: лимиты, статус, быстрые команды
/limits — сколько осталось от подписки (5 часов и неделя)
/status — жив ли я и что делаю
/stop — остановить задачу в этой теме (или кнопка ⏹ под ответом)
/reset — начать разговор в теме заново (заметки в /workspace остаются)
/compact — сжать разговор: меньше расход лимитов
/archive — записать суть в заметку темы и начать заново
/context — насколько разросся разговор
/plan задача — сначала план, выполню по кнопке
/resume — вернуться к одному из прошлых разговоров в теме
/model — модель (opus, sonnet, haiku, default)
/id — id чата, темы и твой

Попроси «сделай быструю команду …» — появится своя /команда и кнопка в /menu.
Попроси «измени свой код, чтобы …» — поменяю себя, запушу и передеплоюсь.`

// command handles a bot command; false means "not a command of ours, treat as text".
func (c *AgentController) command(ctx context.Context, m *telegram.Message, text string) bool {
	name := strings.Fields(text)[0]
	if at := strings.IndexByte(name, '@'); at >= 0 {
		if !strings.EqualFold(name[at+1:], c.me.Username) {
			return true // a command for another bot in the group: not for us at all
		}
		name = name[:at]
	}
	key := ent.KeyOf(m)
	switch name {
	case "/start", "/help":
		c.msg.Reply(ctx, m, helpText)
	case "/id":
		c.msg.Reply(ctx, m, fmt.Sprintf("chat_id: `%d`\nтема: `%d`\nтвой user_id: `%d`", m.Chat.ID, m.Thread(), m.From.ID))
	case "/reset":
		_ = c.svc.Reset(key)
		c.msg.Reply(ctx, m, "Начинаю разговор в этой теме заново. Заметки в /workspace на месте.")
	case "/stop":
		if c.svc.Stop(key) {
			c.msg.Reply(ctx, m, "Останавливаю.")
		} else {
			c.msg.Reply(ctx, m, "В этой теме сейчас ничего не выполняется.")
		}
	case "/cancel":
		c.msg.Reply(ctx, m, c.cancel(ctx, m))
	case "/compact", "/archive":
		c.maintain(ctx, m, name)
	case "/context":
		c.msg.Reply(ctx, m, "Разговор в этой теме: "+svc.CompactLine(c.svc.ContextSize(key), c.cfg.CompactAt)+".")
	case "/plan":
		task := strings.TrimSpace(strings.TrimPrefix(text, strings.Fields(text)[0]))
		if task == "" {
			c.msg.Reply(ctx, m, "Напиши задачу: `/plan что сделать`. Я распишу план и спрошу, выполнять ли.")
		} else if !c.svc.Plan(m, task) {
			c.msg.Reply(ctx, m, "Очередь в этой теме полная, подожди, пока закончу.")
		}
	case "/resume":
		c.sendResume(ctx, m)
	case "/model":
		c.model(ctx, m, strings.Fields(text)[1:])
	case "/status":
		c.msg.Reply(ctx, m, c.status(key))
	case "/menu":
		c.sendMenu(ctx, m)
	case "/limits":
		c.msg.Reply(ctx, m, c.limits(ctx))
	default:
		q, ok := c.svc.QuickCommand(strings.TrimPrefix(name, "/"))
		if !ok {
			return false
		}
		c.runQuick(ctx, m, q)
	}
	return true
}

func (c *AgentController) status(key ent.TopicKey) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**Иноагент на связи**, работаю %s.\n", time.Since(c.started).Round(time.Minute))
	usage := fmt.Sprint(c.svc.UsageToday())
	if c.cfg.DailyLimit > 0 {
		usage += fmt.Sprintf(" из %d", c.cfg.DailyLimit)
	}
	running, limit := c.svc.Running()
	fmt.Fprintf(&b, "Запросов сегодня: %s. Одновременно работает %d из %d.\n", usage, running, limit)
	if st := c.svc.TopicStatus(key); st.Busy {
		fmt.Fprintf(&b, "В этой теме: работаю, в очереди ещё %d.\n", st.Queued)
	} else {
		b.WriteString("В этой теме: свободен.\n")
	}
	if n := c.svc.ContextSize(key); n > 0 {
		fmt.Fprintf(&b, "Разговор: %s.\n", svc.CompactLine(n, c.cfg.CompactAt))
	}
	if sess, ok := c.svc.Session(key); ok {
		id := sess.ID
		if len(id) > 8 {
			id = id[:8]
		}
		fmt.Fprintf(&b, "Сессия `%s`, последнее сообщение %s назад.", id, time.Since(sess.Updated).Round(time.Minute))
	} else {
		b.WriteString("Сессии в теме ещё нет.")
	}
	if !c.cfg.HasClaudeAuth {
		b.WriteString("\n⚠️ Нет токена Claude.")
	}
	return b.String()
}
