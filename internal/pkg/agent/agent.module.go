package agent

import (
	"context"
	"log/slog"

	"app/internal/core/asr"
	"app/internal/core/cfg"
	"app/internal/core/claude"
	"app/internal/core/messenger"
	"app/internal/core/telegram"
	"app/internal/pkg/agent/ctrl"
	"app/internal/pkg/agent/repo"
	"app/internal/pkg/agent/svc"
)

// Module is the agent itself: topics, sessions, the queue and the claude runs.
type Module struct {
	name    string
	version string
	API     *ctrl.AgentController
	Service *svc.AgentService
	cfg     *cfg.Config
	ctx     context.Context
}

// New creates a new instance of the Agent module.
func New(ctx context.Context, repository repo.AgentRepository, m *messenger.Messenger, me telegram.User, log *slog.Logger) *Module {
	c := cfg.Inst()
	runner := &claude.Runner{
		Bin:     c.ClaudeBin,
		Workdir: c.Workspace,
		Model:   c.ClaudeModel,
		Env:     svc.ClaudeEnv(),

		SystemPrompt: func() string { return svc.SystemPrompt(c.Workspace) },
	}
	speech := asr.New(c.ASRURL, c.ASRLanguage)
	service := svc.NewAgentService(ctx, c, repository, m, runner, speech, log)
	service.RestoreModel()
	controller := ctrl.NewAgentController(c, service, m, me, log)

	return &Module{
		name:    "Agent",
		version: "v1",
		API:     controller,
		Service: service,
		cfg:     c,
		ctx:     ctx,
	}
}

// Initialize lays out the workspace before the first message arrives and
// starts keeping Telegram's command menu in step with the quick commands.
func (m Module) Initialize() error {
	if err := svc.EnsureWorkspace(m.cfg.Workspace); err != nil {
		return err
	}
	m.Service.ForgetOtherAccounts()
	m.API.WatchCommands(m.ctx)
	m.Service.WatchDeploys()
	m.Service.WatchReminders()
	go m.Service.SyncVault()
	m.Service.PruneUploads()
	return nil
}
func (m Module) Version() string { return m.version }
func (m Module) Name() string    { return m.name }
