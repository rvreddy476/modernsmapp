package middleware

import (
	"github.com/gin-gonic/gin"
)

// OtelTracing is a no-op tracing middleware stub.
// To enable full W3C trace-context propagation and span creation, add
// go.opentelemetry.io/otel to go.mod and replace this with the full
// OTel Gin middleware implementation.
func OtelTracing(serviceName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
	}
}
