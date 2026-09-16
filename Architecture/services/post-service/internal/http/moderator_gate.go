package http

import (
	"crypto/hmac"
	"net/http"
	"strings"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// moderatorScopes are the gateway-verified X-Scopes values that may read the
// moderation queues and change a post's review state.
var moderatorScopes = []string{"moderator", "admin", "superadmin"}

// reviewServiceActor is recorded as the actor when a review-status or
// visibility change arrives service-to-service. The internal service key is
// shared by every backend service, so it proves "a trusted service", not which
// one; today reviewer-service is the only caller of these routes.
const reviewServiceActor = "internal-service-key"

// requireModeratorIdentity checks the gateway-verified identity headers:
// no or invalid X-User-Id is 401, a user without a moderator-or-higher scope
// is 403. It writes the error response itself.
func requireModeratorIdentity(c *gin.Context) (uuid.UUID, bool) {
	actorID, err := uuid.Parse(strings.TrimSpace(c.GetHeader("X-User-Id")))
	if err != nil || actorID == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required", nil)
		return uuid.Nil, false
	}
	if !hasAnyScope(c.GetHeader("X-Scopes"), moderatorScopes...) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Moderator scope required", nil)
		return uuid.Nil, false
	}
	return actorID, true
}

// requireModerator is route middleware for moderator-only read and admin
// surfaces (reel moderation queue, /v1/admin/reports, /v1/admin/comments).
func requireModerator() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := requireModeratorIdentity(c); !ok {
			c.Abort()
			return
		}
		c.Next()
	}
}

// resolveReviewActor decides who is changing a post's review_status or
// visibility. A request carrying user identity headers (the gateway path) must
// be a moderator and is recorded as that user. A request with no user identity
// must present the internal service key and is recorded as a service. Anything
// else is refused, so no change is ever made without an actor.
func (h *Handler) resolveReviewActor(c *gin.Context) (postgres.ReviewAuditActor, bool) {
	if strings.TrimSpace(c.GetHeader("X-User-Id")) != "" || strings.TrimSpace(c.GetHeader("X-Scopes")) != "" {
		actorID, ok := requireModeratorIdentity(c)
		if !ok {
			return postgres.ReviewAuditActor{}, false
		}
		return postgres.ReviewAuditActor{UserID: &actorID}, true
	}
	key := c.GetHeader("X-Internal-Service-Key")
	if h.internalKey == "" || key == "" || !hmac.Equal([]byte(key), []byte(h.internalKey)) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required", nil)
		return postgres.ReviewAuditActor{}, false
	}
	return postgres.ReviewAuditActor{Service: reviewServiceActor}, true
}
