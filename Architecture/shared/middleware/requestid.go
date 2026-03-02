package middleware

import (
	"log/slog"

	"github.com/facebook-like/shared/o11y/logging"
	"github.com/facebook-like/shared/o11y/trace"
	"github.com/gin-gonic/gin"
)

// RequestID extracts or generates a request ID and trace ID, stores them
// in the context, and sets response headers. It also enriches the slog
// logger in context with request_id, trace_id, and user_id.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader(trace.HeaderRequestID)
		if requestID == "" {
			requestID = trace.NewRequestID()
		}

		traceID := c.GetHeader(trace.HeaderTraceID)
		if traceID == "" {
			traceID = requestID
		}

		userID := c.GetHeader(trace.HeaderUserID)

		ctx := c.Request.Context()
		ctx = trace.WithRequestID(ctx, requestID)
		ctx = trace.WithTraceID(ctx, traceID)
		if userID != "" {
			ctx = trace.WithUserID(ctx, userID)
		}

		logger := slog.Default().With(
			"request_id", requestID,
			"trace_id", traceID,
		)
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
