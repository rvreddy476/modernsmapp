package http

import (
	"strings"

	"github.com/atpost/shared/o11y/trace"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// codeActorRequired is the error code for a human admin change that has no
// verified actor. Nothing is changed.
const codeActorRequired = "ACTOR_REQUIRED"

// adminAuditMeta builds the audit context for a human admin change. The
// actor is the gateway-set X-User-Id; when the gateway also sent
// X-Verified-User-Id the two must agree. There is no fallback actor: a
// missing, malformed or nil id reports ok=false and the caller answers
// 400 ACTOR_REQUIRED.
func adminAuditMeta(c *gin.Context, reason string) (postgres.AuditMeta, bool) {
	raw := strings.TrimSpace(c.GetHeader("X-User-Id"))
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return postgres.AuditMeta{}, false
	}
	if verified := strings.TrimSpace(c.GetHeader("X-Verified-User-Id")); verified != "" {
		vid, err := uuid.Parse(verified)
		if err != nil || vid != id {
			return postgres.AuditMeta{}, false
		}
	}
	return postgres.AuditMeta{
		Actor:     postgres.UserActor(id),
		Reason:    strings.TrimSpace(reason),
		RequestID: requestIDOf(c),
	}, true
}

// requestIDOf is the propagated request id, if any.
func requestIDOf(c *gin.Context) string {
	if id := strings.TrimSpace(c.GetHeader(trace.HeaderRequestID)); id != "" {
		if len(id) > 128 {
			id = id[:128]
		}
		return id
	}
	return trace.RequestIDFrom(c.Request.Context())
}
