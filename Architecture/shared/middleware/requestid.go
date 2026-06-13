package middleware

import (
	"log/slog"

	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/trace"
	"github.com/gin-gonic/gin"
)

// RequestID extracts or generates a request ID and trace ID, stores them
// in the context, and sets response headers. It also enriches the slog
// logger in context with request_id, trace_id, span_id, and user_id.
//
// Phase F3.4 — when an OTel span is active on the request (set by the
// OtelTracing middleware ahead of this one), trace_id + span_id come
// from the W3C span context so logs correlate with Jaeger spans.
// Falls back to the legacy X-Trace-Id header when no span is active so
// downstream consumers see a stable trace_id during the rollout.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader(trace.HeaderRequestID)
		if requestID == "" {
			requestID = trace.NewRequestID()
		}

		ctx := c.Request.Context()

		// Prefer the OTel span context — it's the single source of
		// truth once tracing is wired. Falls back to the legacy header
		// for callers that haven't been migrated yet.
		spanCtx := trace.SpanFromContext(ctx).SpanContext()
		var traceID, spanID string
		if spanCtx.IsValid() {
			traceID = spanCtx.TraceID().String()
			spanID = spanCtx.SpanID().String()
		} else {
			traceID = c.GetHeader(trace.HeaderTraceID)
			if traceID == "" {
				traceID = requestID
			}
		}

		userID := c.GetHeader(trace.HeaderUserID)

		ctx = trace.WithRequestID(ctx, requestID)
		ctx = trace.WithTraceID(ctx, traceID)
		if userID != "" {
			ctx = trace.WithUserID(ctx, userID)
		}

		logger := slog.Default().With(
			"request_id", requestID,
			"trace_id", traceID,
		)
		if spanID != "" {
			logger = logger.With("span_id", spanID)
		}
		if userID != "" {
			logger = logger.With("user_id", userID)
		}
		ctx = logging.WithLogger(ctx, logger)

		c.Request = c.Request.WithContext(ctx)

		c.Header(trace.HeaderRequestID, requestID)
		c.Header(trace.HeaderTraceID, traceID)

		c.Next()
	}
}
