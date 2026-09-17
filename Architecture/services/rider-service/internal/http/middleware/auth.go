package middleware

import (
	"net/http"
	"strings"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	// ContextKeyUserID holds the caller's user id (uuid.UUID) once
	// GatewayIdentity has admitted the request.
	ContextKeyUserID = "authenticated_user_id"
	// ContextKeyScopes holds the gateway-verified scopes claim (string).
	ContextKeyScopes = "authenticated_scopes"
)

// GatewayIdentity admits a request that carries the gateway's verified
// identity headers and refuses everything else.
//
// The gateway strips X-User-Id, X-Verified-User-Id and X-Scopes from every
// inbound request and re-sets them from the signed JWT, and the whole
// /v1/rider group sits behind RequireInternalKey, so a header that reaches
// this middleware was written by the gateway. The service does not verify a
// bearer token itself on purpose: the gateway is the only JWT verifier, and
// it is where the dormant-product gate and the session-revocation check live.
func GatewayIdentity() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := strings.TrimSpace(c.GetHeader("X-Verified-User-Id"))
		if raw == "" {
			raw = strings.TrimSpace(c.GetHeader("X-User-Id"))
		}
		if raw == "" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "AUTH_REQUIRED", "authentication required", nil)
			c.Abort()
			return
		}
		uid, err := uuid.Parse(raw)
		if err != nil || uid == uuid.Nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "INVALID_USER_ID", "user id is not a valid UUID", nil)
			c.Abort()
			return
		}
		c.Set(ContextKeyUserID, uid)
		if scopes := c.GetHeader("X-Scopes"); scopes != "" {
			c.Set(ContextKeyScopes, scopes)
		}
		c.Next()
	}
}

// GetAuthenticatedUserID extracts the verified user UUID from context.
func GetAuthenticatedUserID(c *gin.Context) (uuid.UUID, bool) {
	val, exists := c.Get(ContextKeyUserID)
	if !exists {
		return uuid.Nil, false
	}
	uid, ok := val.(uuid.UUID)
	return uid, ok && uid != uuid.Nil
}

// GetAuthenticatedScopes extracts the gateway-verified scopes from context.
func GetAuthenticatedScopes(c *gin.Context) string {
	if val, exists := c.Get(ContextKeyScopes); exists {
		if s, ok := val.(string); ok {
			return s
		}
	}
	return ""
}
