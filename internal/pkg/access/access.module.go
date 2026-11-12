package access

import (
	"log/slog"

	"app/internal/core/cfg"
	"app/internal/core/messenger"
	"app/internal/pkg/access/ctrl"
	"app/internal/pkg/access/svc"
)

// Module is the gate in front of everything: who may talk to the agent.
type Module struct {
	name    string
	version string
	API     *ctrl.AccessController
	Service *svc.AccessService
}

// New creates a new instance of the Access module.
func New(m *messenger.Messenger, log *slog.Logger) *Module {
	service := svc.NewAccessService(cfg.Inst(), m.Client(), log)
	controller := ctrl.NewAccessController(service, m, log)

	return &Module{
		name:    "Access",
		version: "v1",
		API:     controller,
		Service: service,
	}
}

func (m Module) Initialize() error { return nil }
func (m Module) Version() string   { return m.version }
func (m Module) Name() string      { return m.name }
