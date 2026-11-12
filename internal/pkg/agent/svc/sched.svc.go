package svc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"app/internal/core/claude"
	"app/internal/core/telegram"
	"app/internal/pkg/agent/ent"
)

// Reminders, scheduled messages and scheduled tasks. The agent creates them
// with the `remind` helper, which drops one JSON file per item into
// <state>/reminders; the bot looks at that folder every few seconds and
// does what is due. Files, not memory, so nothing is lost on a restart and
// the agent can list or delete them with ordinary tools.
const (
	remindersDir = "reminders"
	schedTick    = 15 * time.Second
)

// WatchReminders runs until the service stops.
func (s *AgentService) WatchReminders() {
	dir := filepath.Join(s.cfg.StateDir, remindersDir)
	_ = os.MkdirAll(dir, 0o777)
	go func() {
		tick := time.NewTicker(schedTick)
		defer tick.Stop()
		for {
			s.fireDue(dir, time.Now())
			select {
			case <-s.ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
}

func (s *AgentService) fireDue(dir string, now time.Time) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, path := range files {
		var r ent.Reminder
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if err := json.Unmarshal(data, &r); err != nil || r.Chat == 0 || r.Text == "" {
			s.log.Warn("bad reminder file, set aside", "file", filepath.Base(path))
			_ = os.Rename(path, path+".bad")
			continue
		}
		if r.At.After(now) {
			continue
		}
		// Forget it (or move it to its next time) before acting: a crash
		// then loses one reminder instead of repeating it forever.
		if next, ok := r.Next(now); ok {
			r.At = next
			if out, err := json.Marshal(r); err == nil {
				_ = os.WriteFile(path, out, 0o666)
			}
		} else {
			_ = os.Remove(path)
		}
		s.fire(r)
	}
}

func (s *AgentService) fire(r ent.Reminder) {
	m := &telegram.Message{
		Chat:            telegram.Chat{ID: r.Chat, Type: "supergroup"},
		MessageThreadID: r.Thread,
		IsTopicMessage:  r.Thread != 0,
		From:            &telegram.User{ID: r.UserID, Username: r.Username, FirstName: r.Name},
	}
	switch r.Mode {
	case ent.ModeSay:
		s.msg.Reply(s.ctx, m, r.Text)
	case ent.ModeRun:
		if !s.Enqueue(ent.Job{Msg: m, Text: r.Text}) {
			s.msg.Reply(s.ctx, m, fmt.Sprintf("⏰ Не смог запустить по расписанию (очередь полная): %s", r.Text))
		}
	default:
		who := ""
		if r.Username != "" {
			who = "@" + r.Username + " "
		}
		s.msg.Reply(s.ctx, m, "⏰ "+who+r.Text)
	}
}

const (
	// limitGrace is how long after the reported reset the request is retried.
	limitGrace = 5 * time.Minute
	// limitUnknown is the wait when the limit hit carries no reset time.
	limitUnknown = 30 * time.Minute
	limitNote    = "(отложено: упёрся в лимит, теперь он сброшен — выполни как обычный запрос)\n"
)

// limitReset reports whether a run was stopped by a subscription limit and
// when the last of the exhausted windows resets.
func limitReset(limits []claude.Limit, now time.Time) (time.Time, bool) {
	var until time.Time
	hit := false
	for _, l := range limits {
		if l.Status != "rejected" {
			continue
		}
		hit = true
		if l.ResetsAt.After(until) {
			until = l.ResetsAt
		}
	}
	if !hit {
		return time.Time{}, false
	}
	if !until.After(now) {
		until = now.Add(limitUnknown - limitGrace)
	}
	return until, true
}

// deferForLimit parks a request that hit the limit as a one-off scheduled
// task: it runs again a few minutes after the window resets, with no
// action from anyone. If the limit is still there then, it is parked again.
func (s *AgentService) deferForLimit(ctx context.Context, m *telegram.Message, text, attached string, resetAt time.Time) {
	at := resetAt.Add(limitGrace)
	body := strings.TrimPrefix(text, limitNote)
	if attached != "" {
		body += "\n" + attached
	}
	r := ent.Reminder{
		ID:       randID(),
		Chat:     m.Chat.ID,
		Thread:   m.Thread(),
		UserID:   m.From.ID,
		Username: m.From.Username,
		Name:     m.From.FirstName,
		At:       at,
		Text:     limitNote + body,
		Mode:     ent.ModeRun,
	}
	dir := filepath.Join(s.cfg.StateDir, remindersDir)
	_ = os.MkdirAll(dir, 0o777)
	out, err := json.Marshal(r)
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, r.ID+".json"), out, 0o666)
	}
	if err != nil {
		s.log.Error("defer for limit", "err", err)
		s.msg.Reply(ctx, m, "🛑 Лимит кончился, и отложить запрос не вышло. Напиши позже.")
		return
	}
	s.msg.Reply(ctx, m, "🛑 Лимит кончился. Выполню сам после сброса, около "+at.Local().Format("15:04 02.01")+".")
}
