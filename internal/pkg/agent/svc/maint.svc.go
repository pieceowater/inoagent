package svc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"app/internal/core/telegram"
	"app/internal/pkg/agent/ent"
)

// A long conversation costs more with every request: the whole of it is read
// again each time. These are the ways to make it short again: /compact
// squeezes it in place, /archive writes down what matters into the topic's
// note in the vault and starts clean, /reset just starts clean. Earlier
// sessions stay in /resume.

// maintain runs a compact or archive job on the topic's conversation.
func (s *AgentService) maintain(ctx context.Context, t *topic, j ent.Job, sessionID string, topicName string, env []string) {
	m := j.Msg
	if sessionID == "" {
		s.msg.Reply(ctx, m, "Разговор в теме и так пустой: сжимать нечего.")
		return
	}
	switch j.Kind {
	case ent.JobCompact:
		s.compact(ctx, m, t.key, sessionID, env, false)
	case ent.JobArchive:
		s.archive(ctx, m, t.key, sessionID, topicName, env)
	}
}

// compact squeezes the conversation with Claude Code's own /compact. It
// reports whether the conversation was compacted.
func (s *AgentService) compact(ctx context.Context, m *telegram.Message, key ent.TopicKey, sessionID string, env []string, auto bool) bool {
	before := 0
	if sess, ok := s.repo.Session(key); ok {
		before = sess.Tokens
	}
	if auto {
		s.msg.Reply(ctx, m, fmt.Sprintf("🗜 Разговор в теме разросся (~%s токенов), сжимаю, чтобы не жрать лимиты.", kilo(before)))
	}
	res, err := s.runner.Run(ctx, "/compact", sessionID, env, nil, nil)
	if err != nil || res.IsError {
		why := "ошибка"
		switch {
		case err != nil:
			why = err.Error()
		case res.Text != "":
			why = res.Text
		}
		s.log.Warn("compact failed", "err", why)
		s.msg.Reply(ctx, m, "⚠️ Не вышло сжать: "+truncate(why, 300)+"\nМожно `/archive` (записать суть в заметку и начать заново) или `/reset`.")
		return false
	}
	id := sessionID
	if res.SessionID != "" {
		id = res.SessionID
	}
	sess, _ := s.repo.Session(key)
	if err := s.repo.SetSession(key, id, sess.Context); err != nil {
		s.log.Error("save session", "err", err)
	}
	_ = s.repo.NoteSession(key, 0, "")
	if !auto {
		s.msg.Reply(ctx, m, fmt.Sprintf("🗜 Сжал разговор (было ~%s токенов).", kilo(before)))
	}
	return true
}

// archive has the agent write the gist of the conversation into the topic's
// note, then starts a fresh conversation: its first request carries that note.
func (s *AgentService) archive(ctx context.Context, m *telegram.Message, key ent.TopicKey, sessionID, topicName string, env []string) {
	file := TopicFile(topicName)
	if file == "" {
		file = "inbox/archive-" + time.Now().Format("2006-01-02-1504") + ".md"
	}
	prompt := fmt.Sprintf("Архивация разговора. Допиши в конец vault-заметки `%s` (создай, если её нет) раздел "+
		"«## Архив разговора %s»: что решили и сделали, текущее состояние, открытые задачи и договорённости, важные факты "+
		"(пути, имена, ссылки; без секретов). Коротко, по делу, без воды; то, что уже записано в заметке, не повторяй. "+
		"Когда допишешь, ответь одним словом: готово.", file, time.Now().Format("2006-01-02 15:04"))
	res, err := s.runner.Run(ctx, prompt, sessionID, env, nil, nil)
	if err != nil || res.IsError {
		why := "ошибка"
		if err != nil {
			why = err.Error()
		}
		s.msg.Reply(ctx, m, "⚠️ Не смог записать архив: "+truncate(why, 300)+"\nРазговор не трогаю.")
		return
	}
	if err := s.repo.DropSession(key); err != nil {
		s.log.Error("drop session", "err", err)
	}
	go s.SyncVault()
	s.msg.Reply(ctx, m, "📦 Записал суть в `"+file+"` и начал разговор заново. Старый можно вернуть через /resume.")
}

// kilo is a token count as "12k".
func kilo(n int) string {
	if n < 1000 {
		return fmt.Sprint(n)
	}
	return fmt.Sprintf("%dk", n/1000)
}

// Maintain queues an upkeep job for the topic.
func (s *AgentService) Maintain(m *telegram.Message, kind string) bool {
	return s.Enqueue(ent.Job{Msg: m, Kind: kind})
}

// ContextSize is the size of the topic's conversation in tokens, 0 if unknown.
func (s *AgentService) ContextSize(key ent.TopicKey) int {
	sess, ok := s.repo.Session(key)
	if !ok {
		return 0
	}
	return sess.Tokens
}

// History is the topic's earlier sessions, newest first.
func (s *AgentService) History(key ent.TopicKey) []ent.Session { return s.repo.History(key) }

// Resume switches the topic back to an earlier session.
func (s *AgentService) Resume(key ent.TopicKey, i int) (ent.Session, error) {
	return s.repo.Restore(key, i)
}

// Model is the model requests use now ("" is the default one).
func (s *AgentService) Model() string { return s.runner.CurrentModel() }

// SetModel switches the model for all topics; "" goes back to the default.
func (s *AgentService) SetModel(model string) error {
	if err := s.repo.SetModel(model); err != nil {
		return err
	}
	s.runner.SetModel(model)
	return nil
}

// RestoreModel applies the model chosen earlier with /model.
func (s *AgentService) RestoreModel() {
	if m := strings.TrimSpace(s.repo.Model()); m != "" {
		s.runner.SetModel(m)
	}
}

// compactLine is how /context describes the size of a conversation.
func CompactLine(tokens, at int) string {
	if tokens == 0 {
		return "размер пока не знаю (появится после следующего запроса)"
	}
	s := fmt.Sprintf("~%s токенов", kilo(tokens))
	if at > 0 {
		s += fmt.Sprintf(", сожму автоматически на %s", kilo(at))
	}
	return s
}

// takeMaintRequest does what the agent asked for with the `ctx` helper during
// the run: shorten the topic's conversation, after the answer has gone out.
func (s *AgentService) takeMaintRequest(key ent.TopicKey, m *telegram.Message) {
	path := filepath.Join(s.cfg.StateDir, "maint", fmt.Sprintf("%d_%d.json", key.Chat, key.Thread))
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	_ = os.Remove(path)
	var req struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(b, &req) != nil {
		return
	}
	switch req.Kind {
	case "compact":
		s.Maintain(m, ent.JobCompact)
	case "archive":
		s.Maintain(m, ent.JobArchive)
	case "reset":
		_ = s.repo.DropSession(key)
		s.msg.Reply(s.ctx, m, "🔄 Начал разговор в теме заново. Старый можно вернуть через /resume.")
	}
}
