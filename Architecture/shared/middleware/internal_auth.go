package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const internalKeyHeader = "X-Internal-Service-Key"

// InjectInternalKey returns a middleware that sets the X-Internal-Service-Key
// header on every request before it is proxied to a backend service.
// This allows backends to verify the request originated from the gateway.
func InjectInternalKey(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if secret != "" {
			c.Request.Header.Set(internalKeyHeader, secret)
		}
		c.Next()
	}
}

// RequireInternalKey returns a middleware that validates the X-Internal-Service-Key
// header. Returns HTTP 401 if the header is missing or does not match the secret.
// Use this on sensitive backend routes (admin, trust-safety) to prevent direct access.
func RequireInternalKey(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if secret == "" {
			// If secret not configured, skip validation (fail-open for backward compat)
			c.Next()
			return
		}
		if c.GetHeader(internalKeyHeader) != secret {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"code": "UNAUTHORIZED", "message": "internal service key required"},
			})
			return
		}
		c.Next()
	}
}
