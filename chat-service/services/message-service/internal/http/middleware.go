package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/chat-service/shared/api"
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
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-User-Id, X-Request-Id, X-Session-Id, Idempotency-Key")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func AuthMiddleware(jwtSecret string, log *slog.Logger) gin.HandlerFunc {
	if log == nil {
		log = slog.Default()
	}
	secret := []byte(strings.TrimSpace(jwtSecret))

	return func(c *gin.Context) {
		if strings.HasSuffix(c.Request.URL.Path, "/health") {
			c.Next()
			return
		}
		if len(secret) == 0 {
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
		userID, err := parseAndValidateJWT(token, secret)
		if err != nil {
			log.Warn("invalid bearer token", "err", err, "request_id", RequestIDFromContext(c))
			api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid token", nil, nil)
			c.Abort()
			return
		}

		c.Set(userIDKey, userID)
		c.Next()
	}
}

func parseAndValidateJWT(token string, secret []byte) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("invalid token format")
	}

	headerRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", errors.New("invalid token header")
	}
	payloadRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("invalid token payload")
	}
	signatureRaw, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", errors.New("invalid token signature")
	}

	var header map[string]any
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		return "", errors.New("invalid token header json")
	}
	alg, _ := header["alg"].(string)
	if alg != "HS256" {
		return "", errors.New("unsupported jwt algorithm")
	}

	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(signingInput))
	expected := mac.Sum(nil)
	if !hmac.Equal(signatureRaw, expected) {
		return "", errors.New("invalid token signature")
	}

	var payload map[string]any
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		return "", errors.New("invalid token payload json")
	}

	nowUnix := time.Now().Unix()
	if exp, ok := readNumericClaim(payload["exp"]); ok && nowUnix >= exp {
		return "", errors.New("token expired")
	}
	if nbf, ok := readNumericClaim(payload["nbf"]); ok && nowUnix < nbf {
		return "", errors.New("token not active yet")
	}

	userID, _ := payload["sub"].(string)
	if userID == "" {
		userID, _ = payload["user_id"].(string)
	}
	if userID == "" {
		return "", errors.New("missing subject claim")
	}
	return userID, nil
}

func readNumericClaim(raw any) (int64, bool) {
	switch v := raw.(type) {
	case float64:
		return int64(v), true
	case int64:
		return v, true
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}
