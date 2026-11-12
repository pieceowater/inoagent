package observability

import (
	"log/slog"
	"os"
	"strings"
)

// Config controls how observability is initialised.
type Config struct {
	ServiceName string
	LogLevel    string
}

// Init sets up the JSON logger. Logs go to stdout → docker → journald-style
// rotation by the json-file driver; message contents never go into them.
func Init(cfg Config) *slog.Logger {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)})).
		With("service", cfg.ServiceName)
	slog.SetDefault(logger)
	return logger
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}
