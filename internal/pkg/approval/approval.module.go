package approval

import (
	"log/slog"

	"app/internal/core/cfg"
	"app/internal/core/messenger"
	"app/internal/pkg/approval/ctrl"
	"app/internal/pkg/approval/svc"
)

// Module decides, for every tool call Claude wants to make, whether it runs
// straight away or waits for a button (INOAGENT_APPROVAL_MODE; off by default).
type Module struct {
	name    string
	version string
	API     *ctrl.ApprovalController
	Service *svc.ApprovalService
}

// New creates a new instance of the Approval module. runs is the agent
// module's registry of claude processes in flight, members the access
// module's view of who is in the group.
func New(runs svc.Runs, members svc.Members, m *messenger.Messenger, log *slog.Logger) *Module {
	service := svc.NewApprovalService(cfg.Inst(), runs, members, m, log)
	controller := ctrl.NewApprovalController(service, m.Client())

	return &Module{
		name:    "Approval",
		version: "v1",
		API:     controller,
		Service: service,
	}
}

func (m Module) Initialize() error { return nil }
func (m Module) Version() string   { return m.version }
func (m Module) Name() string      { return m.name }
