// Command hook is Claude Code's PreToolUse hook (wired in
// deploy/managed-settings.json). Claude runs it before every tool call with
// the call as JSON on stdin; it forwards that to the server over the unix
// socket and blocks until the server answers — which, for a gated action, is
// when someone presses a button in Telegram.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"app/internal/core/hookapi"
)

func main() {
	os.Exit(run())
}

func run() int {
	in, err := io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
	if err != nil {
		fmt.Fprintln(os.Stderr, "inoagent hook: read stdin:", err)
		return 2
	}
	runID := os.Getenv(hookapi.EnvRunID)
	if runID == "" {
		// claude started by hand inside the container (say, `claude
		// setup-token`), not by the server: nobody could approve anything,
		// so no tool runs.
		_ = hookapi.Emit(os.Stdout, hookapi.Deny("этот запуск claude не от Иноагента: инструменты отключены"))
		return 0
	}
	sock := os.Getenv(hookapi.EnvSocket)
	if sock == "" {
		sock = hookapi.DefaultSocket
	}
	body, _ := json.Marshal(hookapi.Request{RunID: runID, Input: in})
	resp, err := hookapi.UnixClient(sock).Post("http://inoagent/hook", "application/json", bytes.NewReader(body))
	if err != nil {
		// Exit code 2 blocks the tool call and shows stderr to Claude.
		fmt.Fprintln(os.Stderr, "inoagent: сервер недоступен, действие заблокировано")
		return 2
	}
	defer resp.Body.Close()
	var reply hookapi.Reply
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&reply) != nil {
		fmt.Fprintln(os.Stderr, "inoagent: сервер ответил ошибкой, действие заблокировано")
		return 2
	}
	_ = hookapi.Emit(os.Stdout, reply)
	return 0
}
