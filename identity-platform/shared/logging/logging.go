package logging

import (
	"log/slog"
	"os"
	"strings"
)

// New returns a JSON logger configured via environment variables.
// LOG_LEVEL: debug|info|warn|error (default: info)
// LOG_ADD_SOURCE: true|false (default: false)
func New(service string) *slog.Logger {
	level := parseLevel(os.Getenv("LOG_LEVEL"))
	addSource := strings.EqualFold(os.Getenv("LOG_ADD_SOURCE"), "true")
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:     level,
		AddSource: addSource,
	})
	return slog.New(handler).With("service", service)
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
