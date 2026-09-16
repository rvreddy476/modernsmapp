package http

// The guard over /v1/commerce/internal.
//
// Every route under that prefix is an ops or service-to-service capability:
// seller approve/reject/suspend, KYC verify, pending payouts, product
// moderation, COD settlement, banners, catalogue authoring, the search
// read-back. Each group used to install the internal-key middleware only
// `if h.internalKey != ""`, so a process started without the key served all
// of them to anyone who could reach it.
//
// requireInternalKey is installed unconditionally. An unset key does not
// switch the check off — it makes every guarded route answer 503
// INTERNAL_KEY_NOT_CONFIGURED, because "this deployment cannot authenticate
// its internal callers" is a configuration fault, not permission.

import (
	"crypto/subtle"
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// CodeInternalKeyNotConfigured is the stable error code a guarded route
// answers with when the service was started without INTERNAL_SERVICE_KEY.
const CodeInternalKeyNotConfigured = "INTERNAL_KEY_NOT_CONFIGURED"

// CodeActorRequired is the stable error code a human admin action answers
// with when no real actor id arrived in X-User-Id.
const CodeActorRequired = "ACTOR_REQUIRED"

// requireActor reads the human behind an admin action from X-User-Id — set by
// the gateway from the verified token, or forwarded by admin-service. A
// missing, malformed or nil id is refused with 400 ACTOR_REQUIRED before
// anything is written: these routes used to record the nil UUID as the actor
// of seller and product decisions, which reads as an id and names nobody.
func requireActor(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil || id == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			CodeActorRequired, "this admin action requires the acting user's id in X-User-Id", nil)
		c.Abort()
		return uuid.Nil, false
	}
	return id, true
}

func requireInternalKey(key string) gin.HandlerFunc {
	want := []byte(key)
	return func(c *gin.Context) {
		if len(want) == 0 {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable,
				CodeInternalKeyNotConfigured, "internal routes are disabled: INTERNAL_SERVICE_KEY is not configured", nil)
			c.Abort()
			return
		}
		got := []byte(c.GetHeader(InternalServiceKeyHeader))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized,
				"UNAUTHORIZED", "internal service key required", nil)
			c.Abort()
			return
		}
		c.Next()
	}
}
