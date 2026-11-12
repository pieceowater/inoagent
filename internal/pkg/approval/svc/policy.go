// Package policy decides, for every tool call Claude wants to make, whether it
// runs straight away, needs a person to press a button first, or never runs.
//
// The decision is made here, by the bridge, never by the model: the model only
// proposes. Everything that is not provably read-only falls through to Ask, so
// a command this file has never heard of is gated, not waved through.
package svc

import (
	"app/internal/pkg/approval/ent"

	"encoding/json"
	"path/filepath"
	"strings"
)

var allow = ent.Decision{Level: ent.Allow}

// Policy holds the few paths the decisions depend on.
type Policy struct {
	Workspace string // the agent's own folder: writing there is always fine
}

// Tools that only read, talk to the model's own bookkeeping, or manage its
// own background shells.
var safeTools = map[string]bool{
	"WebSearch": true, "WebFetch": true, "TodoWrite": true, "Task": true, "Agent": true,
	"BashOutput": true, "KillShell": true, "KillBash": true, "TaskOutput": true,
	"TaskStop": true, "ExitPlanMode": true, "ToolSearch": true,
}

func (p Policy) Classify(tool string, input json.RawMessage) ent.Decision {
	var in struct {
		Command      string `json:"command"`
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		Path         string `json:"path"`
		Pattern      string `json:"pattern"`
	}
	_ = json.Unmarshal(input, &in)

	switch tool {
	case "Bash":
		return Shell(in.Command)
	case "Read", "Glob", "Grep", "LS", "NotebookRead":
		for _, s := range []string{in.FilePath, in.Path, in.Pattern} {
			if sensitive(s) {
				return ent.Decision{Level: ent.Ask, Reason: "чтение секретов"}
			}
		}
		return allow
	case "Write", "Edit", "MultiEdit", "NotebookEdit":
		path := in.FilePath
		if path == "" {
			path = in.NotebookPath
		}
		if p.inWorkspace(path) && filepath.Base(path) != "CLAUDE.md" {
			return allow
		}
		return ent.Decision{Level: ent.Ask, Reason: "запись файла вне /workspace"}
	}
	if safeTools[tool] {
		return allow
	}
	return ent.Decision{Level: ent.Ask, Reason: "инструмент " + tool}
}

func (p Policy) inWorkspace(path string) bool {
	if p.Workspace == "" || path == "" || !filepath.IsAbs(path) {
		return false
	}
	rel, err := filepath.Rel(p.Workspace, filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

// Anything that could print a credential back into the chat. Reading these is
// not forbidden — an approver can still allow it — just never automatic.
var sensitiveMarks = []string{
	"/secrets", "environ", ".credentials.json", "/etc/shadow", "/etc/gshadow",
	"id_ed25519", "id_rsa", "id_ecdsa", ".env", "secrets.yml", "/etc/sudoers",
}

func sensitive(s string) bool {
	if s == "" {
		return false
	}
	for _, m := range sensitiveMarks {
		if strings.Contains(s, m) && !strings.HasSuffix(s, ".pub") {
			return true
		}
	}
	return false
}
