package logging

import (
	"context"
	"log/slog"
	"os"
)

type contextKey int

const loggerKey contextKey = iota

// Config controls how the logger is initialized.
type Config struct {
	ServiceName string
	Level       slog.Level
	AddSource   bool
}

// Init creates a JSON slog.Logger with service-level attributes baked in,
// sets it as the slog default, and returns it.
func Init(cfg Config) *slog.Logger {
	level := cfg.Level
	if level == 0 {
		level = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:     level,
		AddSource: cfg.AddSource,
	})

	logger := slog.New(handler).With("service", cfg.ServiceName)
	slog.SetDefault(logger)
	return logger
}

// WithLogger stores a *slog.Logger in the context.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

// FromContext extracts the logger from context, falling back to slog.Default().
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
