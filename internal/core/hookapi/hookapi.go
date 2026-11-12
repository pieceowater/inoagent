// Package hookapi is the contract between cmd/hook (the PreToolUse hook Claude
// Code runs before every tool call) and the server's unix socket.
package hookapi

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
)

// Env vars the server sets on every claude process it starts. The hook reads
// them to say which run a tool call belongs to and where the socket is.
const (
	EnvRunID  = "INOAGENT_RUN_ID"
	EnvSocket = "INOAGENT_HOOK_SOCK"
)

const DefaultSocket = "/tmp/inoagent-hook.sock"

// Request is what the hook sends to the server.
type Request struct {
	RunID string          `json:"run_id"`
	Input json.RawMessage `json:"input"` // the hook's stdin from Claude Code, as is
}

// Input is the part of Claude Code's PreToolUse payload the server reads.
type Input struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}

// Reply is the server's verdict.
type Reply struct {
	Decision string `json:"decision"` // "allow" or "deny"
	Reason   string `json:"reason"`
}

func Allow(reason string) Reply { return Reply{Decision: "allow", Reason: reason} }
func Deny(reason string) Reply  { return Reply{Decision: "deny", Reason: reason} }

// Emit writes the reply in the shape Claude Code expects on a hook's stdout.
// Anything but an explicit allow is a deny.
func Emit(w io.Writer, r Reply) error {
	decision := "deny"
	if r.Decision == "allow" {
		decision = "allow"
	}
	return json.NewEncoder(w).Encode(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       decision,
			"permissionDecisionReason": r.Reason,
		},
	})
}

// UnixClient talks HTTP to the server's socket; the host part of URLs is ignored.
func UnixClient(sock string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
}
