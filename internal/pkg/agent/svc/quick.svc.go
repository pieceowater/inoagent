package svc

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"app/internal/pkg/agent/ent"
)

// Quick commands live in the workspace, in a file the agent itself edits when
// asked "make a quick command for …". The service watches it and reports
// changes, so new commands show up in Telegram's menu without a restart.
const quickFile = "quick-commands.json"

var quickName = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

type quickCache struct {
	mu       sync.Mutex
	mtime    time.Time
	commands []ent.QuickCommand
}

// QuickCommands returns the valid entries of the file, re-reading it only
// when it changed. Broken entries are skipped, a broken file reads as empty.
func (s *AgentService) QuickCommands() []ent.QuickCommand {
	cmds, _ := s.reloadQuick()
	return cmds
}

// QuickCommand finds one by name.
func (s *AgentService) QuickCommand(name string) (ent.QuickCommand, bool) {
	for _, c := range s.QuickCommands() {
		if c.Command == name {
			return c, true
		}
	}
	return ent.QuickCommand{}, false
}

// WatchQuickCommands calls onChange with the current list at start and every
// time the file changes, until the service stops.
func (s *AgentService) WatchQuickCommands(onChange func([]ent.QuickCommand)) {
	cmds, _ := s.reloadQuick()
	onChange(cmds)
	go func() {
		tick := time.NewTicker(10 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-tick.C:
				if cmds, changed := s.reloadQuick(); changed {
					onChange(cmds)
				}
			}
		}
	}()
}

func (s *AgentService) reloadQuick() ([]ent.QuickCommand, bool) {
	c := &s.quick
	c.mu.Lock()
	defer c.mu.Unlock()
	path := filepath.Join(s.cfg.Workspace, quickFile)
	st, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		changed := !c.mtime.IsZero()
		c.mtime, c.commands = time.Time{}, nil
		return nil, changed
	}
	if err != nil || st.ModTime().Equal(c.mtime) {
		return c.commands, false
	}
	c.mtime = st.ModTime()
	c.commands = nil
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, true
	}
	var all []ent.QuickCommand
	if err := json.Unmarshal(b, &all); err != nil {
		s.log.Warn("quick-commands.json is not valid JSON", "err", err)
		return nil, true
	}
	seen := map[string]bool{}
	for _, q := range all {
		if !quickName.MatchString(q.Command) || q.Prompt == "" || seen[q.Command] || Builtin(q.Command) {
			continue
		}
		if r := []rune(q.Description); len(r) > 60 {
			q.Description = string(r[:60])
		}
		if q.Description == "" {
			q.Description = q.Command
		}
		seen[q.Command] = true
		c.commands = append(c.commands, q)
	}
	return c.commands, true
}

// Builtin names can't be taken by a quick command.
func Builtin(name string) bool {
	switch name {
	case "start", "help", "menu", "limits", "status", "stop", "reset", "id":
		return true
	}
	return false
}
