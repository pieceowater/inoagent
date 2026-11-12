// Command healthcheck is the container's HEALTHCHECK: healthy when the server
// is up and Telegram has answered its long poll recently.
package main

import (
	"net/http"
	"os"

	"app/internal/core/hookapi"
)

func main() {
	sock := os.Getenv(hookapi.EnvSocket)
	if sock == "" {
		sock = hookapi.DefaultSocket
	}
	resp, err := hookapi.UnixClient(sock).Get("http://inoagent/healthz")
	if err != nil || resp.StatusCode != http.StatusOK {
		os.Exit(1)
	}
}
