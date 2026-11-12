package svc

import (
	"context"
	"errors"
	"html"
	"strings"
	"time"

	"app/internal/core/telegram"
	"app/internal/pkg/agent/ent"
)

// bridgeUser stands for the owner's Claude on their own machine in prompts.
var bridgeUser = &telegram.User{FirstName: "Claude на компьютере Юрия (по SSH-мосту)"}

// BridgeThread is the topic the bridge works in (0: the general one).
func (s *AgentService) BridgeThread() int {
	if s.cfg.BridgeThread != 0 || s.cfg.BridgeTopic == "" {
		return s.cfg.BridgeThread
	}
	return s.repo.ThreadByName(s.cfg.TelegramChatID, s.cfg.BridgeTopic)
}

// Ask runs a request that came over the bridge in the bridge topic and
// waits for the final answer. The request and the answer are shown in the
// topic too (the run answers there as it does to anyone), so the people there see what the two Claudes tell each other.
func (s *AgentService) Ask(ctx context.Context, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("empty request")
	}
	thread := s.BridgeThread()
	m := &telegram.Message{
		Chat:            telegram.Chat{ID: s.cfg.TelegramChatID, Type: "supergroup"},
		MessageThreadID: thread,
		IsTopicMessage:  thread > 0,
		From:            bridgeUser,
		Text:            text,
	}
	sent, err := s.msg.SendHTML(ctx, m, "🖥 <b>Claude с компа:</b>\n"+html.EscapeString(truncate(text, 3500)), nil)
	if err != nil {
		return "", err
	}
	m.MessageID = sent.MessageID
	done := make(chan string, 1)
	prompt := "(запрос пришёл от Claude Юрия с его компьютера по SSH-мосту, не из чата. Ответ получит он; " +
		"коротко и по делу, с точными путями и командами. Если нужно что-то опасное или необратимое — скажи, что нужно подтверждение Юрия.)\n" + text
	if !s.Enqueue(ent.Job{Msg: m, Text: prompt, Done: done}) {
		return "", errors.New("queue is full")
	}
	select {
	case answer := <-done:
		return strings.TrimSpace(answer), nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(s.cfg.RunTimeout * 2):
		return "", errors.New("timed out")
	}
}
