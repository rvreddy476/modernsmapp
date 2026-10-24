package http

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/atpost/chat-shared/accessauth"
	"github.com/atpost/chat-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	requestIDHeader = "X-Request-Id"
	requestIDKey    = "request_id"
	userIDKey       = "user_id"
)

func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader(requestIDHeader)
		if requestID == "" {
			requestID = uuid.New().String()
		}
		c.Set(requestIDKey, requestID)
		c.Writer.Header().Set(requestIDHeader, requestID)
		c.Next()
	}
}

func RequestIDFromContext(c *gin.Context) string {
	if v, ok := c.Get(requestIDKey); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func LoggerMiddleware(log *slog.Logger) gin.HandlerFunc {
	if log == nil {
		log = slog.Default()
	}
	return func(c *gin.Context) {
		if strings.HasSuffix(c.Request.URL.Path, "/health") {
			c.Next()
			return
		}
		start := time.Now()
		c.Next()
		duration := time.Since(start)
		status := c.Writer.Status()
		requestID := RequestIDFromContext(c)
		attrs := []any{
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", status,
			"duration_ms", duration.Milliseconds(),
			"client_ip", c.ClientIP(),
			"request_id", requestID,
		}
		if len(c.Errors) > 0 {
			attrs = append(attrs, "errors", c.Errors.String())
		}
		switch {
		case status >= http.StatusInternalServerError:
			log.Error("request completed", attrs...)
		case status >= http.StatusBadRequest:
			log.Warn("request completed", attrs...)
		default:
			log.Info("request completed", attrs...)
		}
	}
}

func RecoveryMiddleware(log *slog.Logger) gin.HandlerFunc {
	if log == nil {
		log = slog.Default()
	}
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("panic recovered", "panic", rec, "stack", string(debug.Stack()), "request_id", RequestIDFromContext(c))
				if !c.Writer.Written() {
					api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
				}
				c.Abort()
			}
		}()
		c.Next()
	}
}

func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-User-Id, X-Request-Id, X-Session-Id, Idempotency-Key, X-Chat-Reveal, X-Chat-Open, X-Step-Up-Token, X-Device-Id")
		c.Writer.Header().Set("Access-Control-Expose-Headers", "X-Request-Id, X-Chat-Open-Expires-At, X-Chat-Reveal-Expires-At")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// JWTKeySet — C7. Picks the verifying secret by `kid` so a kid-rotation
// window can verify both old and new tokens. Pre-C7 tokens (no kid) fall
// back to the active secret.
type JWTKeySet struct {
	ActiveKID          string
	ActiveSecret       string
	PreviousKID        string
	PreviousSecret     string
	AccessKeys         accessauth.KeySet
	Policy             accessauth.Policy
	InternalServiceKey string
}

func (k JWTKeySet) secretFor(kid string) ([]byte, bool) {
	active := strings.TrimSpace(k.ActiveSecret)
	if kid == "" || kid == k.ActiveKID {
		if active == "" {
			return nil, false
		}
		return []byte(active), true
	}
	prev := strings.TrimSpace(k.PreviousSecret)
	if prev != "" && kid == k.PreviousKID {
		return []byte(prev), true
	}
	return nil, false
}

func AuthMiddleware(jwtSecret string, log *slog.Logger) gin.HandlerFunc {
	return AuthMiddlewareWithKeys(JWTKeySet{ActiveSecret: jwtSecret}, log)
}

func AuthMiddlewareWithKeys(keys JWTKeySet, log *slog.Logger) gin.HandlerFunc {
	if log == nil {
		log = slog.Default()
	}
	return func(c *gin.Context) {
		if strings.HasSuffix(c.Request.URL.Path, "/health") {
			c.Next()
			return
		}
		if strings.HasPrefix(c.Request.URL.Path, "/internal/v1/chat/") &&
			keys.InternalServiceKey != "" &&
			subtle.ConstantTimeCompare([]byte(c.GetHeader("X-Internal-Service-Key")), []byte(keys.InternalServiceKey)) == 1 {
			c.Next()
			return
		}
		resolved := keys.accessKeys()
		if strings.TrimSpace(resolved.ActiveSecret) == "" && len(resolved.RSAKeys) == 0 {
			api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication not configured", nil, nil)
			c.Abort()
			return
		}

		authz := strings.TrimSpace(c.GetHeader("Authorization"))
		if !strings.HasPrefix(authz, "Bearer ") {
			api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing bearer token", nil, nil)
			c.Abort()
			return
		}

		token := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
		identity, err := parseIdentityWithKeys(token, keys)
		if err != nil {
			log.Warn("invalid bearer token", "err", err, "request_id", RequestIDFromContext(c))
			api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid token", nil, nil)
			c.Abort()
			return
		}

		c.Set(userIDKey, identity.UserID)
		c.Set(accessIdentityKey, identity)
		// Hidden chats reset (POST /v1/chat/hidden/reset) accepts a separate
		// step-up access token from identity. It is verified with exactly the
		// same keys and policy as the bearer, and kept only when it names the
		// same user (and, when both carry one, the same session).
		if raw := strings.TrimSpace(c.GetHeader(stepUpTokenHeader)); raw != "" {
			if su, err := parseIdentityWithKeys(raw, keys); err == nil && su.UserID == identity.UserID &&
				(su.SessionID == "" || identity.SessionID == "" || su.SessionID == identity.SessionID) {
				c.Set(stepUpIdentityKey, su)
			}
		}
		c.Next()
	}
}

const (
	// accessIdentityKey holds the verified bearer's accessauth.Identity.
	accessIdentityKey = "access_identity"
	// stepUpIdentityKey holds a verified X-Step-Up-Token's identity.
	stepUpIdentityKey = "step_up_identity"
	stepUpTokenHeader = "X-Step-Up-Token"
)

// accessIdentityFrom returns the verified bearer identity set by the auth
// middleware (zero value when absent, e.g. on an internal-key request).
func accessIdentityFrom(c *gin.Context) accessauth.Identity {
	if v, ok := c.Get(accessIdentityKey); ok {
		if id, ok := v.(accessauth.Identity); ok {
			return id
		}
	}
	return accessauth.Identity{}
}

func parseIdentityWithKeys(token string, keys JWTKeySet) (accessauth.Identity, error) {
	return accessauth.Verify(token, keys.accessKeys(), keys.accessPolicy(), time.Now())
}

func parseAndValidateJWT(token string, secret []byte) (string, error) {
	return parseAndValidateJWTWithKeys(token, JWTKeySet{ActiveSecret: string(secret)})
}

func parseAndValidateJWTWithKeys(token string, keys JWTKeySet) (string, error) {
	identity, err := accessauth.Verify(token, keys.accessKeys(), keys.accessPolicy(), time.Now())
	if err != nil {
		return "", err
	}
	return identity.UserID, nil
}

func (k JWTKeySet) accessKeys() accessauth.KeySet {
	if k.AccessKeys.ActiveSecret != "" || len(k.AccessKeys.RSAKeys) > 0 {
		return k.AccessKeys
	}
	return accessauth.KeySet{
		ActiveKID: k.ActiveKID, ActiveSecret: k.ActiveSecret,
		PreviousKID: k.PreviousKID, PreviousSecret: k.PreviousSecret,
	}
}

func (k JWTKeySet) accessPolicy() accessauth.Policy {
	if k.Policy.Production || len(k.Policy.AllowedIssuers) > 0 || k.Policy.RequiredAudience != "" || k.Policy.AllowHS256 {
		return k.Policy
	}
	return accessauth.Policy{AllowHS256: true, ClockSkew: time.Minute}
}
