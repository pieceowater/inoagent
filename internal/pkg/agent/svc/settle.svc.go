package svc

import (
	"context"
	"strings"

	"app/internal/core/telegram"
	"app/internal/pkg/agent/ent"
)

// settle drops the messages of a batch that were deleted while it waited and
// folds the rest into one request. Telegram sends bots no news about
// deletions, so each message is probed (clearing the bot's reaction on it,
// which is harmless): a deleted one is refused. false = nothing is left.
func (s *AgentService) settle(ctx context.Context, j ent.Job) (ent.Job, bool) {
	var live []ent.BatchItem
	for _, it := range j.Batch {
		if err := s.msg.Client().ClearReaction(ctx, it.Msg.Chat.ID, it.Msg.MessageID); telegram.IsGone(err) {
			continue
		}
		live = append(live, it)
	}
	if len(live) == 0 {
		return j, false
	}
	var texts []string
	j.Extra = nil
	for i, it := range live {
		if t := strings.TrimSpace(it.Text); t != "" {
			texts = append(texts, t)
		}
		if i > 0 {
			j.Extra = append(j.Extra, it.Msg)
		}
	}
	j.Msg, j.Text, j.Batch = live[0].Msg, strings.Join(texts, "\n"), nil
	return j, true
}
