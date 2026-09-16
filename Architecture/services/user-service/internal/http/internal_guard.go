package http

// The guard over /internal/*.
//
// Those routes are service-to-service: projection repair, the dead-letter
// queue (list AND replay), projection health, subscriber fan-out ids. The
// group used to install the key check only `if h.internalRouteKey != ""`,
// so a process started without INTERNAL_SERVICE_KEY served all of them —
// including DLQ replay and every channel's subscriber list — to anyone who
// could reach the port.
//
// requireInternalRouteKey is installed unconditionally. An unset key answers
// 503 INTERNAL_KEY_NOT_CONFIGURED on every guarded route; it never opens.

import (
	"crypto/subtle"
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// CodeInternalKeyNotConfigured is the stable error code an /internal route
// answers with when the service was started without INTERNAL_SERVICE_KEY.
const CodeInternalKeyNotConfigured = "INTERNAL_KEY_NOT_CONFIGURED"

const internalServiceKeyHeader = "X-Internal-Service-Key"

func requireInternalRouteKey(key string) gin.HandlerFunc {
	want := []byte(key)
	return func(c *gin.Context) {
		if len(want) == 0 {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable,
				CodeInternalKeyNotConfigured, "internal routes are disabled: INTERNAL_SERVICE_KEY is not configured", nil)
			c.Abort()
			return
		}
		got := []byte(c.GetHeader(internalServiceKeyHeader))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized,
				"UNAUTHORIZED", "internal service key required", nil)
			c.Abort()
			return
		}
		c.Next()
	}
}
