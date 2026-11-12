package svc

import (
	_ "embed"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The workspace's starting CLAUDE.md: the agent's own memory, written only
// when there is none, so what the agent wrote there survives redeploys.
//
//go:embed workspace/CLAUDE.md
var defaultClaudeMD []byte

// The rules of the job and the default manner of speaking ship with the
// code, so changing them is a commit and a redeploy — which the agent can do
// to itself. Nothing in them is specific to one server or one owner: those
// facts live in the vault, which is per install.
//
//go:embed prompt/rules.md
var rules string

//go:embed prompt/persona.md
var defaultPersona string

// SystemPrompt is what every run appends to Claude Code's system prompt: the
// rules, with this install's names filled in, and the persona — the vault's
// own persona.md when there is one (read on every run, so an edit applies to
// the next message), else the default.
func SystemPrompt(workspace string) string {
	host, _ := os.Hostname()
	r := strings.NewReplacer(
		"{{HOSTNAME}}", orDefault(host, "сервер"),
		"{{REPO}}", orDefault(os.Getenv("INOAGENT_REPO"), "<не задан>"),
		"{{VAULT_REPO}}", orDefault(os.Getenv("INOAGENT_VAULT_REPO"), "<не задан>"),
	).Replace(rules)
	persona := defaultPersona
	if b, err := os.ReadFile(filepath.Join(workspace, "persona.md")); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		persona = string(b)
	}
	return r + "\n\n" + persona
}

// EnsureWorkspace lays out the agent's vault on first start: CLAUDE.md, the
// folders, and a git repo, so anything the agent writes there can be looked
// at, rolled back, and synced to Obsidian.
func EnsureWorkspace(dir string) error {
	for _, sub := range []string{"server", "incidents", "people", "howto", "inbox"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return err
		}
	}
	md := filepath.Join(dir, "CLAUDE.md")
	if _, err := os.Stat(md); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(md, defaultClaudeMD, 0o644); err != nil {
			return err
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); !errors.Is(err, os.ErrNotExist) {
		return nil
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "Иноагент"},
		{"config", "user.email", "inoagent@localhost"},
		{"add", "-A"},
		{"commit", "-q", "-m", "Пустой workspace"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return errors.New("git " + args[0] + ": " + string(out))
		}
	}
	return nil
}
