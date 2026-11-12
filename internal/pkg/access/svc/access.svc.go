package svc

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"app/internal/core/cfg"
	"app/internal/core/telegram"
)

// AccessService decides who the agent talks to: members of the one group it
// lives in, and approvers in private chat. Everyone else is ignored, and any
// other group it gets added to is left at once.
type AccessService struct {
	cfg *cfg.Config
	tg  *telegram.Client
	log *slog.Logger

	mu      sync.Mutex
	members map[int64]member
}

type member struct {
	ok bool
	at time.Time
}

// How long a "yes, still in the group" answer is trusted before asking again.
const memberTTL = 5 * time.Minute

func NewAccessService(c *cfg.Config, tg *telegram.Client, log *slog.Logger) *AccessService {
	return &AccessService{cfg: c, tg: tg, log: log, members: map[int64]member{}}
}

// Admit reports whether the agent should handle this message at all.
func (s *AccessService) Admit(ctx context.Context, m *telegram.Message) bool {
	if m.From == nil || m.From.IsBot {
		return false
	}
	switch {
	case m.Chat.ID == s.cfg.TelegramChatID:
		return s.IsMember(ctx, m.From.ID)
	case m.Chat.Type == "private":
		return s.cfg.AllowDM && s.cfg.IsApprover(m.From.ID)
	case m.Chat.IsGroup():
		// Not ours. No reply, and the content is never logged.
		s.log.Warn("message from a foreign chat, leaving", "chat_id", m.Chat.ID)
		_ = s.tg.LeaveChat(ctx, m.Chat.ID)
	}
	return false
}

// AdmitCallback: a button press counts only from a member, on a message in
// our own group.
func (s *AccessService) AdmitCallback(ctx context.Context, q *telegram.CallbackQuery) bool {
	if q.Message == nil || q.Message.Chat.ID != s.cfg.TelegramChatID {
		return false
	}
	return s.IsMember(ctx, q.From.ID)
}

// OnMembership: the bot was added to (or removed from) some chat. Anything but
// our own group is left — nobody gets to borrow the agent by adding it elsewhere.
func (s *AccessService) OnMembership(ctx context.Context, cm *telegram.ChatMemberUpdated) {
	if !cm.NewChatMember.Present() || !cm.Chat.IsGroup() || s.cfg.SetupMode() {
		return
	}
	if cm.Chat.ID != s.cfg.TelegramChatID {
		s.log.Warn("added to a foreign chat, leaving", "chat_id", cm.Chat.ID, "by", cm.From.ID)
		_ = s.tg.LeaveChat(ctx, cm.Chat.ID)
	}
}

// IsMember asks Telegram whether the user is still in the group. An error
// counts as "no".
func (s *AccessService) IsMember(ctx context.Context, userID int64) bool {
	s.mu.Lock()
	e, ok := s.members[userID]
	s.mu.Unlock()
	if ok && time.Since(e.at) < memberTTL {
		return e.ok
	}
	cm, err := s.tg.GetChatMember(ctx, s.cfg.TelegramChatID, userID)
	if err != nil {
		s.log.Warn("getChatMember failed", "user_id", userID, "err", err)
		return false
	}
	s.mu.Lock()
	s.members[userID] = member{ok: cm.Present(), at: time.Now()}
	s.mu.Unlock()
	return cm.Present()
}
