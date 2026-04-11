package http

import (
	"crypto/hmac"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type AccessClaims struct {
	jwt.RegisteredClaims
	SessionID string `json:"sid"`
}

const (
	requestIDHeader = "X-Request-Id"
	requestIDKey    = "request_id"
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
		if c.Request.URL.Path == "/v1/auth/health" {
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

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func AuthMiddleware(jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var tokenStr string

		// Try Authorization header first
		if auth := c.GetHeader("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			tokenStr = strings.TrimPrefix(auth, "Bearer ")
		}

		// Fallback to cookie
		if tokenStr == "" {
			if cookie, err := c.Cookie("access_token"); err == nil {
				tokenStr = cookie
			}
		}

		if tokenStr == "" {
			api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing access token", nil, nil)
			c.Abort()
			return
		}

		claims := &AccessClaims{}
		token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
			return []byte(jwtSecret), nil
		})
		if err != nil || !token.Valid {
			api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid access token", nil, nil)
			c.Abort()
			return
		}

		c.Request.Header.Set("X-User-Id", claims.Subject)
		c.Next()
	}
}

func RequireCSRFMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if isSafeMethod(c.Request.Method) {
			c.Next()
			return
		}

		headerToken := c.GetHeader("X-CSRF-Token")
		cookieToken, err := c.Cookie("csrf_token")
		if err != nil || headerToken == "" || headerToken != cookieToken {
			api.Error(c.Writer, http.StatusForbidden, "CSRF_FAILED", "CSRF token mismatch", nil, nil)
			c.Abort()
			return
		}
		c.Next()
	}
}

func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, X-Request-Id, X-Client-Platform, X-Client-Version, X-Client-Source")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE, PATCH")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

func RequireInternalServiceKey(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if secret == "" {
			api.Error(c.Writer, http.StatusServiceUnavailable, "INTERNAL_KEY_UNAVAILABLE", "Internal service authentication is not configured", nil, nil)
			c.Abort()
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
