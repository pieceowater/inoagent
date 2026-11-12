package svc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"sync"
	"time"

	"app/internal/core/cfg"
	"app/internal/core/hookapi"
	"app/internal/core/messenger"
	"app/internal/core/telegram"
	"app/internal/pkg/approval/ent"
)

// Runs is how this module finds the message that started a claude run, to
// post its buttons in the same topic. The agent module implements it.
type Runs interface {
	LookupRun(runID string) (*telegram.Message, bool)
}

// Members tells whether a Telegram user is in the agent's group: any member
// may press an approval button. The access module implements it.
type Members interface {
	IsMember(ctx context.Context, userID int64) bool
}

// ApprovalService answers the PreToolUse hook: allow, deny, or ask a person.
type ApprovalService struct {
	cfg     *cfg.Config
	policy  Policy
	runs    Runs
	members Members
	msg     *messenger.Messenger
	log     *slog.Logger

	pending sync.Map // approval id → chan ent.Verdict
}

func NewApprovalService(c *cfg.Config, runs Runs, members Members, m *messenger.Messenger, log *slog.Logger) *ApprovalService {
	return &ApprovalService{cfg: c, policy: Policy{Workspace: c.Workspace}, runs: runs, members: members, msg: m, log: log}
}

// Decide is the verdict for one tool call. For a gated call it blocks until a
// button is pressed, the timeout passes, or ctx ends (the claude process died).
func (s *ApprovalService) Decide(ctx context.Context, req hookapi.Request) hookapi.Reply {
	var in hookapi.Input
	_ = json.Unmarshal(req.Input, &in)
	origin, ok := s.runs.LookupRun(req.RunID)
	if !ok {
		return hookapi.Deny("неизвестный запуск")
	}
	d := s.policy.Classify(in.ToolName, in.ToolInput)
	switch {
	case d.Level == ent.Deny:
		return hookapi.Deny(d.Reason)
	case d.Level == ent.Allow,
		s.cfg.ApprovalMode == "off",
		s.cfg.ApprovalMode == "danger" && d.Level != ent.Danger:
		return hookapi.Allow("")
	}
	v := s.ask(ctx, origin, d, in.ToolName, in.ToolInput)
	if v.OK {
		return hookapi.Allow("подтвердил " + v.By)
	}
	return hookapi.Deny("Действие не подтверждено (" + v.By + "). Не пытайся сделать то же самое другим " +
		"способом: объясни в ответе, что хотел сделать и зачем, и спроси, как быть.")
}

// Answer delivers a button press. The returned text is shown to whoever pressed it.
func (s *ApprovalService) Answer(ctx context.Context, id string, from telegram.User, ok bool) string {
	if !s.cfg.IsApprover(from.ID) && !s.members.IsMember(ctx, from.ID) {
		return "Подтверждать могут только участники чата."
	}
	v, found := s.pending.Load(id)
	if !found {
		return "Запрос уже неактуален."
	}
	select {
	case v.(chan ent.Verdict) <- ent.Verdict{OK: ok, By: from.DisplayName()}:
		return "Принято"
	default:
		return "Уже ответили."
	}
}

func (s *ApprovalService) ask(ctx context.Context, origin *telegram.Message, d ent.Decision, tool string, input json.RawMessage) ent.Verdict {
	id := randID()
	ch := make(chan ent.Verdict, 1)
	s.pending.Store(id, ch)
	defer s.pending.Delete(id)

	head := "⚠️ <b>Нужно подтверждение</b>"
	if d.Level == ent.Danger {
		head = "🛑 <b>Опасное действие</b>"
	}
	body := fmt.Sprintf("%s\n%s\n<pre>%s</pre>\nПочему спрашиваю: %s\nЗадачу дал: %s",
		head, html.EscapeString(tool), html.EscapeString(describe(tool, input)),
		html.EscapeString(d.Reason), html.EscapeString(origin.From.DisplayName()))
	markup := &telegram.InlineKeyboardMarkup{InlineKeyboard: [][]telegram.InlineKeyboardButton{{
		{Text: "✅ Разрешить", CallbackData: CallbackPrefix + id + ":y"},
		{Text: "❌ Отклонить", CallbackData: CallbackPrefix + id + ":n"},
	}}}
	sent, err := s.msg.SendHTML(ctx, origin, body, markup)
	if err != nil {
		return ent.Verdict{OK: false, By: "никто: не смог отправить запрос"}
	}

	var v ent.Verdict
	var outcome string
	select {
	case v = <-ch:
		outcome = "❌ Отклонил " + v.By
		if v.OK {
			outcome = "✅ Разрешил " + v.By
		}
	case <-time.After(s.cfg.ApprovalTimeout):
		v = ent.Verdict{OK: false, By: "никто не ответил вовремя"}
		outcome = "⌛ Никто не ответил — отклонено"
	case <-ctx.Done():
		v = ent.Verdict{OK: false, By: "задача остановлена"}
		outcome = "⏹ Задача остановлена"
	}
	s.msg.EditHTML(context.WithoutCancel(ctx), sent, body+"\n\n"+html.EscapeString(outcome), nil)
	s.log.Info("approval", "tool", tool, "level", d.Level.String(), "ok", v.OK)
	return v
}

// CallbackPrefix marks the buttons this module owns: "ap:<id>:y|n".
const CallbackPrefix = "ap:"

// ParseCallback splits button data; ok is false for data that isn't ours.
func ParseCallback(data string) (id string, allow bool, ok bool) {
	parts := strings.Split(strings.TrimPrefix(data, CallbackPrefix), ":")
	if !strings.HasPrefix(data, CallbackPrefix) || len(parts) != 2 {
		return "", false, false
	}
	return parts[0], parts[1] == "y", true
}

// describe is the part of the tool call a person needs to judge it.
func describe(tool string, input json.RawMessage) string {
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	str := func(k string) string { v, _ := in[k].(string); return v }
	switch tool {
	case "Bash":
		return truncate(str("command"), 2500)
	case "Write":
		return str("file_path") + "\n\n" + truncate(str("content"), 1200)
	case "Edit", "MultiEdit":
		return str("file_path") + "\n\n- " + truncate(str("old_string"), 600) + "\n+ " + truncate(str("new_string"), 600)
	}
	b, _ := json.MarshalIndent(in, "", "  ")
	return truncate(string(b), 2500)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func randID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
