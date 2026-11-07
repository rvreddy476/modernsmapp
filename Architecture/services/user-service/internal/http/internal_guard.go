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
	"errors"
	"net/http"
	"strings"

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

// ErrInternalKeyRequired is why a production process refuses to start
// without INTERNAL_SERVICE_KEY (audit X-L1): the /internal routes above fail
// closed without it, so production would silently lose projection repair,
// DLQ replay and subscriber fan-out.
var ErrInternalKeyRequired = errors.New("INTERNAL_SERVICE_KEY is required in production for the /internal routes")

// CheckInternalKeyConfig returns ErrInternalKeyRequired when the key is unset
// in production, and nil otherwise (a dev process may run without it; its
// /internal routes then answer 503).
func CheckInternalKeyConfig(key string, getenv func(string) string) error {
	if strings.TrimSpace(key) == "" && IsProduction(getenv) {
		return ErrInternalKeyRequired
	}
	return nil
}

// IsProduction reports whether APP_ENV, ENVIRONMENT or ENV (the first one
// set) names production. Same rule as group-service.
func IsProduction(getenv func(string) string) bool {
	for _, key := range []string{"APP_ENV", "ENVIRONMENT", "ENV"} {
		switch strings.ToLower(strings.TrimSpace(getenv(key))) {
		case "prod", "production":
			return true
		case "":
			continue
		default:
			return false
		}
	}
	return false
}
