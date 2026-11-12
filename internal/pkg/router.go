package pkg

import (
	"context"
	"log/slog"
	"net/http"

	"app/internal/core/cfg"
	"app/internal/core/generic/interfaces"
	"app/internal/core/messenger"
	"app/internal/core/telegram"
	"app/internal/pkg/access"
	"app/internal/pkg/agent"
	agentCtrl "app/internal/pkg/agent/ctrl"
	"app/internal/pkg/agent/repo"
	"app/internal/pkg/approval"
)

// Router manages the modules and routes Telegram updates and hook requests to them.
type Router struct {
	modules map[string]interfaces.IModule
	cfg     *cfg.Config
	tg      *telegram.Client
}

// NewRouter creates a new Router instance and initializes every module.
// me is the bot's own account (from getMe), which the agent needs to tell
// when it is being addressed.
func NewRouter(ctx context.Context, repository repo.AgentRepository, m *messenger.Messenger, me telegram.User, log *slog.Logger) *Router {
	accessModule := access.New(m, log)
	agentModule := agent.New(ctx, repository, m, me, log)
	approvalModule := approval.New(agentModule.Service, accessModule.Service, m, log)

	return &Router{
		cfg: cfg.Inst(),
		tg:  m.Client(),
		modules: map[string]interfaces.IModule{
			accessModule.Name():   accessModule,
			agentModule.Name():    agentModule,
			approvalModule.Name(): approvalModule,
		},
	}
}

// InitializeRouter runs every module's Initialize.
func (r *Router) InitializeRouter() (any, error) {
	for _, m := range r.modules {
		if err := m.Initialize(); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// GetModules returns the map of modules.
func (r *Router) GetModules() map[string]interfaces.IModule {
	return r.modules
}

func (r *Router) access() *access.Module     { return r.modules["Access"].(*access.Module) }
func (r *Router) agent() *agent.Module       { return r.modules["Agent"].(*agent.Module) }
func (r *Router) approval() *approval.Module { return r.modules["Approval"].(*approval.Module) }

// Route hands one Telegram update to the module that owns it. It must return
// quickly: long work (claude runs) happens in the agent's topic workers.
func (r *Router) Route(ctx context.Context, u telegram.Update) {
	switch {
	case u.CallbackQuery != nil:
		q := u.CallbackQuery
		switch {
		case !r.access().API.AdmitCallback(ctx, q):
			_ = r.tg.AnswerCallbackQuery(ctx, q.ID, "Кнопки работают только для участников группы.")
		case agentCtrl.OwnsCallback(q.Data):
			r.agent().API.HandleCallback(ctx, q)
		default:
			r.approval().API.HandleCallback(ctx, q)
		}
	case u.MyChatMember != nil:
		r.access().API.OnMembership(ctx, u.MyChatMember)
	case u.Message != nil:
		if r.cfg.SetupMode() {
			r.access().API.SetupReply(ctx, u.Message)
			return
		}
		if r.access().API.Admit(ctx, u.Message) {
			r.agent().API.HandleMessage(ctx, u.Message)
		}
	}
}

// HookRoutes registers the unix-socket endpoints.
func (r *Router) HookRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /hook", r.approval().API.HookHandler)
}

// BridgeRoutes registers the endpoints of the SSH bridge socket.
func (r *Router) BridgeRoutes(mux *http.ServeMux) {
	r.agent().API.BridgeRoutes(mux)
}
