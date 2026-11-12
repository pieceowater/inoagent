package svc

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var vaultMu sync.Mutex

// SyncVault commits whatever the agent left uncommitted in its workspace (an
// Obsidian vault) and pushes it to the vault's GitHub repo, picking up edits
// made on the other side first. Runs after every answer and at start; does
// nothing until Ansible has given the workspace an origin.
func (s *AgentService) SyncVault() {
	vaultMu.Lock()
	defer vaultMu.Unlock()
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Minute)
	defer cancel()
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = s.cfg.Workspace
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	if out, _ := git("status", "--porcelain"); strings.TrimSpace(out) != "" {
		_, _ = git("add", "-A")
		if out, err := git("commit", "-q", "-m", "Заметки "+time.Now().Format("2006-01-02 15:04")); err != nil {
			s.log.Warn("vault commit failed", "out", out)
			return
		}
	}
	if _, err := git("remote", "get-url", "origin"); err != nil {
		return
	}
	// The very first push goes to an empty repo: nothing to pull yet.
	if out, err := git("pull", "-q", "--rebase", "origin", "main"); err != nil && !strings.Contains(out, "couldn't find remote ref") {
		s.log.Warn("vault pull failed", "out", out)
		_, _ = git("rebase", "--abort")
		return
	}
	if out, err := git("push", "-q", "origin", "HEAD:main"); err != nil {
		s.log.Warn("vault push failed", "out", out)
	}
}
