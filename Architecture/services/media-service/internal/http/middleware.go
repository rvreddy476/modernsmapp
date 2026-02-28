package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/facebook-like/shared/api"
	"github.com/gin-gonic/gin"
)

type jwtClaims struct {
	Sub    string `json:"sub"`
	UserID string `json:"user_id"`
	Exp    int64  `json:"exp"`
}

// verifyJWT validates an HS256 JWT and returns the user ID.
// Uses stdlib only (crypto/hmac + crypto/sha256) — no external JWT library needed.
func verifyJWT(tokenStr string, secret []byte) (string, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid token format")
	}

	// Verify HMAC-SHA256 signature
	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(parts[2]), []byte(expectedSig)) {
		return "", fmt.Errorf("invalid signature")
	}

	// Decode payload
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("payload decode: %w", err)
	}

	var claims jwtClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("claims parse: %w", err)
	}

	if claims.Exp > 0 && time.Now().Unix() > claims.Exp {
		return "", fmt.Errorf("token expired")
	}

	userID := claims.Sub
	if userID == "" {
		userID = claims.UserID
	}
	if userID == "" {
		return "", fmt.Errorf("no user_id in token")
	}

	return userID, nil
}

// extractToken gets the JWT from Authorization header or access_token cookie.
func extractToken(c *gin.Context) string {
	if auth := c.GetHeader("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if cookie, err := c.Cookie("access_token"); err == nil && cookie != "" {
		return cookie
	}
	return ""
}

// AuthMiddleware validates JWT and sets X-User-Id header. Rejects unauthenticated requests.
func AuthMiddleware(jwtSecret string) gin.HandlerFunc {
	secret := []byte(jwtSecret)
	return func(c *gin.Context) {
		tokenStr := extractToken(c)
		if tokenStr == "" {
			api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing access token", nil, nil)
			c.Abort()
			return
		}

		userID, err := verifyJWT(tokenStr, secret)
		if err != nil {
			api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid access token", nil, nil)
			c.Abort()
			return
		}

		c.Request.Header.Set("X-User-Id", userID)
		c.Next()
	}
}

// OptionalAuthMiddleware validates JWT if present but doesn't require it.
// If token is present and invalid, returns 401. If absent, continues without user context.
func OptionalAuthMiddleware(jwtSecret string) gin.HandlerFunc {
	secret := []byte(jwtSecret)
	return func(c *gin.Context) {
		tokenStr := extractToken(c)
		if tokenStr == "" {
			c.Next()
			return
		}

		userID, err := verifyJWT(tokenStr, secret)
		if err != nil {
			api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid access token", nil, nil)
			c.Abort()
			return
		}

		c.Request.Header.Set("X-User-Id", userID)
		c.Next()
	}
}

// RequestLoggerMiddleware logs each request with method, path, status, duration.
// Skips /healthz to avoid log noise.
func RequestLoggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == "/healthz" {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()
		duration := time.Since(start)

		status := c.Writer.Status()
		attrs := []any{
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", status,
			"duration_ms", duration.Milliseconds(),
			"client_ip", c.ClientIP(),
		}

		switch {
		case status >= http.StatusInternalServerError:
			slog.Error("request completed", attrs...)
		case status >= http.StatusBadRequest:
			slog.Warn("request completed", attrs...)
		default:
			slog.Info("request completed", attrs...)
		}
	}
}
