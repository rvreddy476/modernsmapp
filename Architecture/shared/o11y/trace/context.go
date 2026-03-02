package trace

import (
	"context"

	"github.com/google/uuid"
)

type contextKey int

const (
	requestIDKey contextKey = iota
	traceIDKey
	userIDKey
)

// Header constants for propagation.
const (
	HeaderRequestID = "X-Request-Id"
	HeaderTraceID   = "X-Trace-Id"
	HeaderUserID    = "X-User-Id"
)

// NewRequestID generates a new UUID v4 request ID.
func NewRequestID() string {
	return uuid.New().String()
}

// WithRequestID stores a request ID in context.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFrom retrieves the request ID from context.
func RequestIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

// WithTraceID stores a trace ID in context.
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceIDKey, id)
}

// TraceIDFrom retrieves the trace ID from context.
func TraceIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(traceIDKey).(string); ok {
		return v
	}
	return ""
}

// WithUserID stores a user ID in context.
func WithUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, userIDKey, id)
}

// UserIDFrom retrieves the user ID from context.
func UserIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(userIDKey).(string); ok {
		return v
	}
	return ""
}
