package ctrl

import (
	"context"
	"fmt"
	"log/slog"

	"app/internal/core/messenger"
	"app/internal/core/telegram"
	"app/internal/pkg/access/svc"
)

type AccessController struct {
	svc *svc.AccessService
	msg *messenger.Messenger
	log *slog.Logger
}

func NewAccessController(service *svc.AccessService, m *messenger.Messenger, log *slog.Logger) *AccessController {
	return &AccessController{svc: service, msg: m, log: log}
}

func (c *AccessController) Admit(ctx context.Context, m *telegram.Message) bool {
	return c.svc.Admit(ctx, m)
}

func (c *AccessController) AdmitCallback(ctx context.Context, q *telegram.CallbackQuery) bool {
	return c.svc.AdmitCallback(ctx, q)
}

func (c *AccessController) OnMembership(ctx context.Context, cm *telegram.ChatMemberUpdated) {
	c.svc.OnMembership(ctx, cm)
}

// SetupReply answers anyone, anywhere, with the ids that go into host_vars.
// Only used before a group is configured; it reveals nothing but the ids.
func (c *AccessController) SetupReply(ctx context.Context, m *telegram.Message) {
	if m.From == nil || m.From.IsBot {
		return
	}
	c.log.Info("setup mode: message", "chat_id", m.Chat.ID, "chat_type", m.Chat.Type, "is_forum", m.Chat.IsForum, "user_id", m.From.ID)
	c.msg.Reply(ctx, m, fmt.Sprintf(
		"Режим настройки.\nchat_id: `%d`\nтвой user_id: `%d`\n\nВпиши их в `ansible/host_vars` и сделай `make deploy`.",
		m.Chat.ID, m.From.ID))
}
