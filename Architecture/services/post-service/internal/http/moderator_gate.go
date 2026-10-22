package http

import (
	"net/http"
	"strings"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// moderatorScopes are the gateway-verified X-Scopes values that may read the
// moderation queues and change a post's review state.
var moderatorScopes = []string{"moderator", "admin", "superadmin"}

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

// resolveReviewActor was REMOVED (security sweep #11, 21 Oct 2026). It let a
// review-status / visibility write through on the shared internal key alone
// (recorded as "internal-service-key") or on gateway identity headers that
// anyone holding the key can set. The internal writes are now admitted by a
// signed service token (requireServiceToken) and recorded as its issuer.
