package ctrl

import (
	"context"
	"encoding/json"
	"net/http"

	"app/internal/core/hookapi"
	"app/internal/core/telegram"
	"app/internal/pkg/approval/svc"
)

// ApprovalController has two doors: the hook socket (Claude asking "may I?")
// and the Telegram buttons (a person answering).
type ApprovalController struct {
	svc *svc.ApprovalService
	tg  *telegram.Client
}

func NewApprovalController(service *svc.ApprovalService, tg *telegram.Client) *ApprovalController {
	return &ApprovalController{svc: service, tg: tg}
}

// HookHandler serves POST /hook on the unix socket. The request context ends
// when the hook process dies with its claude run, which also takes a pending
// approval down.
func (c *ApprovalController) HookHandler(w http.ResponseWriter, r *http.Request) {
	var req hookapi.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = json.NewEncoder(w).Encode(hookapi.Deny("некорректный запрос хука"))
		return
	}
	_ = json.NewEncoder(w).Encode(c.svc.Decide(r.Context(), req))
}

// HandleCallback answers a button press; one that isn't an approval just
// gets its spinner stopped.
func (c *ApprovalController) HandleCallback(ctx context.Context, q *telegram.CallbackQuery) {
	id, allow, ok := svc.ParseCallback(q.Data)
	if !ok {
		_ = c.tg.AnswerCallbackQuery(ctx, q.ID, "")
		return
	}
	_ = c.tg.AnswerCallbackQuery(ctx, q.ID, c.svc.Answer(ctx, id, q.From, allow))
}
