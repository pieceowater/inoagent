package ctrl

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"app/internal/core/claude"
	"app/internal/core/generic/format"
	"app/internal/core/telegram"
	"app/internal/pkg/agent/ent"
	"app/internal/pkg/agent/svc"
)

// Callback data of the menu's buttons: "m:<action>" for the fixed ones,
// "q:<command>" for quick commands.
const (
	menuPrefix   = "m:"
	quickPrefix  = "q:"
	resumePrefix = "rs:"
)

// OwnsCallback tells the router which button presses are ours.
func OwnsCallback(data string) bool {
	return strings.HasPrefix(data, menuPrefix) || strings.HasPrefix(data, quickPrefix) ||
		strings.HasPrefix(data, resumePrefix) || strings.HasPrefix(data, "pl:")
}

// WatchCommands keeps Telegram's "/" menu in step with the quick commands
// file: built-ins first, then whatever the agent was asked to create.
func (c *AgentController) WatchCommands(ctx context.Context) {
	c.svc.WatchQuickCommands(func(quick []ent.QuickCommand) {
		// When the buttons differ from the ones last sent (a quick command
		// changed, or an update changed the layout), re-send the keyboard to
		// the group's General topic so they show up without asking for /menu.
		kb := c.keyboard(quick)
		if !c.cfg.SetupMode() && c.keyboardChanged(kb) {
			general := &telegram.Message{Chat: telegram.Chat{ID: c.cfg.TelegramChatID}}
			_, _ = c.msg.SendKeyboard(ctx, general, "Кнопки обновлены 👇", kb)
		}
		cmds := append([]telegram.BotCommand{}, Commands...)
		for _, q := range quick {
			cmds = append(cmds, telegram.BotCommand{Command: q.Command, Description: q.Description})
		}
		if err := c.msg.Client().SetMyCommands(ctx, cmds); err != nil {
			// Not fatal: the commands still work when typed.
			c.log().Warn("setMyCommands failed", "err", err)
		}
	})
}

// The fixed buttons of the keyboard. Pressing one sends its text, which
// buttonCommand turns back into the command. Status, stop and reset are no
// longer shown, but stay mapped so keyboards already on screen keep working;
// /status, /stop and /reset still work as commands.
const (
	btnLimits = "📊 Лимиты"
	btnCancel = "❌ Отмена"
	btnStatus = "ℹ️ Статус"
	btnStop   = "⏹ Стоп"
	btnReset  = "🔄 Новый разговор"
	quickMark = "⚡ "
)

// quickLabel is the text of a quick command's button: its icon, then the
// description.
func quickLabel(q ent.QuickCommand) string {
	icon := q.Icon
	if icon == "" {
		icon = strings.TrimSpace(quickMark)
	}
	return icon + " " + q.Description
}

// keyboardChanged reports whether kb differs from the keyboard sent last
// time, and remembers it. With no earlier record it counts as changed, so a
// fresh install gets its buttons.
func (c *AgentController) keyboardChanged(kb *telegram.ReplyKeyboardMarkup) bool {
	var b strings.Builder
	for _, row := range kb.Keyboard {
		for _, btn := range row {
			b.WriteString(btn.Text + "\n")
		}
	}
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte(b.String())))
	path := filepath.Join(c.cfg.StateDir, "keyboard.sha")
	if old, err := os.ReadFile(path); err == nil && string(old) == sum {
		return false
	}
	_ = os.WriteFile(path, []byte(sum), 0o644)
	return true
}

// keyboard is the chat's button keyboard: the fixed buttons, then one per
// quick command, two to a row.
func (c *AgentController) keyboard(quick []ent.QuickCommand) *telegram.ReplyKeyboardMarkup {
	rows := [][]telegram.KeyboardButton{
		{{Text: btnLimits}, {Text: btnCancel}},
	}
	var row []telegram.KeyboardButton
	for _, q := range quick {
		row = append(row, telegram.KeyboardButton{Text: quickLabel(q)})
		if len(row) == 2 {
			rows, row = append(rows, row), nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return &telegram.ReplyKeyboardMarkup{Keyboard: rows, IsPersistent: true, ResizeKeyboard: true}
}

// buttonCommand maps a keyboard button's text back to the command it stands for.
func (c *AgentController) buttonCommand(text string) (string, bool) {
	switch text {
	case btnLimits:
		return "/limits", true
	case btnCancel:
		return "/cancel", true
	case btnStatus:
		return "/status", true
	case btnStop:
		return "/stop", true
	case btnReset:
		return "/reset", true
	}
	for _, q := range c.svc.QuickCommands() {
		// The plain ⚡ form is what buttons looked like before icons.
		if text == quickLabel(q) || text == quickMark+q.Description {
			return "/" + q.Command, true
		}
	}
	return "", false
}

func (c *AgentController) sendMenu(ctx context.Context, m *telegram.Message) {
	text := "Кнопки внизу, рядом со скрепкой 👇"
	if len(c.svc.QuickCommands()) == 0 {
		text += "\nБыстрых команд пока нет: попроси «сделай быструю команду …» — появится кнопка."
	}
	_, _ = c.msg.SendKeyboard(ctx, m, text, c.keyboard(c.svc.QuickCommands()))
}

// HandleCallback answers a menu button. The reply goes into the topic the
// menu was posted in, as if whoever pressed the button had typed the command.
func (c *AgentController) HandleCallback(ctx context.Context, q *telegram.CallbackQuery) {
	if q.Message == nil {
		_ = c.msg.Client().AnswerCallbackQuery(ctx, q.ID, "")
		return
	}
	m := *q.Message
	m.From = &q.From
	m.ReplyToMessage = nil
	m.ForwardOrigin = nil
	key := ent.KeyOf(&m)

	answer := ""
	switch q.Data {
	case menuPrefix + "limits":
		c.msg.Reply(ctx, &m, c.limits(ctx))
	case menuPrefix + "status":
		c.msg.Reply(ctx, &m, c.status(key))
	case menuPrefix + "cancel":
		answer = c.cancel(ctx, &m)
	case menuPrefix + "stop":
		answer = "В этой теме ничего не выполняется."
		if c.svc.Stop(key) {
			answer = "Останавливаю."
		}
	case menuPrefix + "reset":
		_ = c.svc.Reset(key)
		answer = "Начинаю разговор в теме заново."
	case svc.PlanGo, svc.PlanEdit, svc.PlanNo:
		answer = c.planAnswer(ctx, &m, q)
	default:
		if strings.HasPrefix(q.Data, resumePrefix) {
			answer = c.resume(ctx, &m, key, strings.TrimPrefix(q.Data, resumePrefix))
			break
		}
		quick, ok := c.svc.QuickCommand(strings.TrimPrefix(q.Data, quickPrefix))
		if !ok {
			answer = "Такой команды больше нет."
			break
		}
		answer = "⚡ " + quick.Description
		c.runQuick(ctx, &m, quick)
	}
	_ = c.msg.Client().AnswerCallbackQuery(ctx, q.ID, answer)
}

// runQuick sends a quick command's prompt to the agent as a normal request.
func (c *AgentController) runQuick(ctx context.Context, m *telegram.Message, q ent.QuickCommand) {
	if !c.cfg.HasClaudeAuth {
		c.msg.Reply(ctx, m, "Нет токена Claude.")
		return
	}
	if !c.svc.Enqueue(ent.Job{Msg: m, Text: q.Prompt}) {
		c.msg.Reply(ctx, m, "Очередь в этой теме полная, подожди, пока закончу.")
	}
}

func (c *AgentController) limits(ctx context.Context) string {
	ls, stale, err := c.svc.Limits(ctx)
	if len(ls) == 0 {
		why := "на этом аккаунте Claude ещё не было запросов — цифры появятся после первого"
		if err != nil {
			why = err.Error() + ", а " + why
		}
		return "Лимиты пока не знаю: " + why + "."
	}
	var b strings.Builder
	b.WriteString("**Лимиты подписки**\n")
	for _, l := range ls {
		fmt.Fprintf(&b, "\n**%s** `%s` %.0f%%", claude.WindowName(l.Window), bar(l.Utilization), l.Utilization)
		if !l.ResetsAt.IsZero() {
			b.WriteString(" — сброс " + claude.ResetIn(l.ResetsAt))
		}
		if l.Status == "rejected" {
			b.WriteString(" — упёрлись")
		}
	}
	if stale {
		b.WriteString("\n\n(по данным последнего запроса: живые цифры сейчас недоступны)")
	}
	return b.String()
}

func bar(percent float64) string {
	filled := int(percent/10 + 0.5)
	filled = max(0, min(10, filled))
	return strings.Repeat("▰", filled) + strings.Repeat("▱", 10-filled)
}

// cancel is the "стой" button: it takes back what is running or waiting in the
// topic, exactly like a message with a stop word, and returns a short answer
// for the button's toast.
func (c *AgentController) cancel(ctx context.Context, m *telegram.Message) string {
	asked := c.svc.Interrupt(ent.KeyOf(m))
	if len(asked) == 0 {
		return "Нечего отменять."
	}
	if c.cfg.HasClaudeAuth {
		c.svc.Enqueue(ent.Job{Msg: m, Text: cancelNote(asked) + "стой"})
	}
	return "Отменяю."
}

// maintain queues /compact or /archive for the topic.
func (c *AgentController) maintain(ctx context.Context, m *telegram.Message, name string) {
	kind := ent.JobCompact
	if name == "/archive" {
		kind = ent.JobArchive
	}
	if !c.cfg.HasClaudeAuth {
		c.msg.Reply(ctx, m, "Нет токена Claude.")
		return
	}
	if !c.svc.Maintain(m, kind) {
		c.msg.Reply(ctx, m, "Очередь в этой теме полная, подожди, пока закончу.")
	}
}

// planAnswer handles the buttons under a plan. The plan message loses its
// buttons whatever was pressed.
func (c *AgentController) planAnswer(ctx context.Context, m *telegram.Message, q *telegram.CallbackQuery) string {
	c.msg.EditHTML(ctx, *m, "👆 План готов.", nil)
	switch q.Data {
	case svc.PlanGo:
		if !c.svc.Enqueue(ent.Job{Msg: m, Text: "Да, выполняй план, который ты только что составил."}) {
			return "Очередь полная, нажми ещё раз позже."
		}
		c.msg.Reply(ctx, m, "▶️ Выполняю.")
		return "Выполняю"
	case svc.PlanEdit:
		c.msg.Reply(ctx, m, "Напиши, что поменять в плане.")
		return "Жду правок"
	}
	return "Отменено"
}

// sendResume lists the topic's earlier conversations as buttons.
func (c *AgentController) sendResume(ctx context.Context, m *telegram.Message) {
	h := c.svc.History(ent.KeyOf(m))
	if len(h) == 0 {
		c.msg.Reply(ctx, m, "Прошлых разговоров в этой теме пока нет: они появляются после /reset или /archive.")
		return
	}
	var rows [][]telegram.InlineKeyboardButton
	var b strings.Builder
	b.WriteString("Прошлые разговоры в теме, новые сверху. Вернуть какой?\n")
	for i, s := range h {
		title := s.Title
		if title == "" {
			title = "без названия"
		}
		fmt.Fprintf(&b, "\n%d. %s — %s назад", i+1, title, time.Since(s.Updated).Round(time.Minute))
		rows = append(rows, []telegram.InlineKeyboardButton{{
			Text: fmt.Sprintf("%d. %s", i+1, truncateRunes(title, 40)), CallbackData: fmt.Sprintf("%s%d", resumePrefix, i),
		}})
	}
	_, _ = c.msg.SendHTML(ctx, m, format.HTML(b.String()), &telegram.InlineKeyboardMarkup{InlineKeyboard: rows})
}

func (c *AgentController) resume(ctx context.Context, m *telegram.Message, key ent.TopicKey, arg string) string {
	var i int
	if _, err := fmt.Sscanf(arg, "%d", &i); err != nil {
		return "Не понял"
	}
	s, err := c.svc.Resume(key, i)
	if err != nil {
		return "Этого разговора уже нет."
	}
	title := s.Title
	if title == "" {
		title = "без названия"
	}
	c.msg.EditHTML(ctx, *m, format.HTML("↩️ Вернулся к разговору: "+title), nil)
	return "Вернул"
}

// models are the short names /model accepts.
var models = map[string]string{"opus": "opus", "sonnet": "sonnet", "haiku": "haiku", "default": "", "по умолчанию": ""}

func (c *AgentController) model(ctx context.Context, m *telegram.Message, args []string) {
	if len(args) == 0 {
		cur := c.svc.Model()
		if cur == "" {
			cur = "по умолчанию"
		}
		c.msg.Reply(ctx, m, "Отвечает модель: **"+cur+"**.\nПоменять: `/model opus`, `/model sonnet`, `/model haiku` или `/model default`. Действует на все темы.")
		return
	}
	name := strings.ToLower(strings.Join(args, " "))
	val, ok := models[name]
	if !ok {
		if !strings.HasPrefix(name, "claude-") {
			c.msg.Reply(ctx, m, "Не знаю такую модель. Можно: opus, sonnet, haiku, default или полное имя `claude-…`.")
			return
		}
		val = name
	}
	if err := c.svc.SetModel(val); err != nil {
		c.msg.Reply(ctx, m, "Не смог сохранить: "+err.Error())
		return
	}
	if val == "" {
		val = "по умолчанию"
	}
	c.msg.Reply(ctx, m, "Теперь отвечает: **"+val+"**.")
}
