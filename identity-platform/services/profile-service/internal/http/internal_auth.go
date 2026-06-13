package http

import (
	"crypto/hmac"
	"net/http"

	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
)

// RequireInternalServiceKey enforces the X-Internal-Service-Key header on
// every gated request. Audit UC1: previously profile-service had no
// internal-key gate at all — every endpoint was reachable directly
// without going through the API gateway, which meant X-User-Id was
// effectively a public header that any caller could spoof.
//
// Empty secret keeps dev unblocked (the wiring in main.go logs a loud
// WARN when the env var is unset). Mirrors the auth-service / user-
// service middleware of the same name.
func RequireInternalServiceKey(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if secret == "" {
			c.Next()
			return
		}
		key := c.GetHeader("X-Internal-Service-Key")
		if !hmac.Equal([]byte(key), []byte(secret)) {
			api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "internal service key required", nil, nil)
			c.Abort()
			return
		}
		c.Next()
	}
}
