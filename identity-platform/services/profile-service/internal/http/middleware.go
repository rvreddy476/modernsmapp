package http

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/atpost/identity-shared/api"
)

const (
	accessTokenCookieName = "access_token"
	csrfCookieName        = "csrf_token"
	csrfHeaderName        = "X-CSRF-Token"
	requestIDHeader       = "X-Request-Id"
	requestIDKey          = "request_id"
)

type AccessClaims struct {
	jwt.RegisteredClaims
	SessionID string `json:"sid"`
}

// JWTKeySet describes the kid-versioned secret set used to verify access
// tokens. Mirrors auth-service.JWTKeySet — see C7 in the audit memory.
type JWTKeySet struct {
	ActiveKID      string
	ActiveSecret   string
	PreviousKID    string
	PreviousSecret string
}

func (k JWTKeySet) secretFor(kid string) ([]byte, bool) {
	if kid == "" || kid == k.ActiveKID {
		return []byte(k.ActiveSecret), true
	}
	if k.PreviousSecret != "" && kid == k.PreviousKID {
		return []byte(k.PreviousSecret), true
	}
	return nil, false
}

func AuthMiddleware(secret string) gin.HandlerFunc {
	return AuthMiddlewareWithKeys(JWTKeySet{ActiveSecret: secret})
}

func AuthMiddlewareWithKeys(keys JWTKeySet) gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.HasSuffix(c.Request.URL.Path, "/health") {
			c.Next()
			return
		}

		tokenStr := readBearerToken(c)
		if tokenStr == "" {
			api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing access token", nil, nil)
			c.Abort()
			return
		}

		claims := &AccessClaims{}
		parsed, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (interface{}, error) {
			// C7 + A1: pin HS256 (defence-in-depth against alg confusion)
			// and pick the secret by `kid`. Legacy tokens (no kid) fall
			// back to the active secret.
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrTokenUnverifiable
			}
			kid, _ := token.Header["kid"].(string)
			secret, ok := keys.secretFor(kid)
			if !ok {
				return nil, jwt.ErrTokenUnverifiable
			}
			return secret, nil
		})
		if err != nil || !parsed.Valid || claims.Subject == "" {
			api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid access token", nil, nil)
			c.Abort()
			return
		}

		if _, err := uuid.Parse(claims.Subject); err != nil {
			api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
			c.Abort()
			return
		}

		c.Request.Header.Set("X-User-Id", claims.Subject)
		c.Next()
	}
}

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

func RequireCSRFMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if isSafeMethod(c.Request.Method) {
			c.Next()
			return
		}

		headerToken := c.GetHeader(csrfHeaderName)
		cookieToken, err := c.Cookie(csrfCookieName)
		if err != nil || headerToken == "" || cookieToken == "" || headerToken != cookieToken {
			api.Error(c.Writer, http.StatusForbidden, "CSRF_FAILED", "Missing or invalid CSRF token", nil, nil)
			c.Abort()
			return
		}

		c.Next()
	}
}

func readBearerToken(c *gin.Context) string {
	if token, err := c.Cookie(accessTokenCookieName); err == nil && token != "" {
		return token
	}
	authHeader := c.GetHeader("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimPrefix(authHeader, "Bearer ")
	}
	return ""
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}
