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

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

type jwtClaims struct {
	Sub    string `json:"sub"`
	UserID string `json:"user_id"`
	Exp    int64  `json:"exp"`
}

// JWTKeySet — C7. Active is the current signing secret; previous is the
// outgoing one accepted during a rotation window. A token with no `kid`
// header (pre-C7) falls back to the active secret.
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

// verifyJWT validates an HS256 JWT against the key set and returns the
// user ID. Uses stdlib only (crypto/hmac + crypto/sha256). C7: picks
// secret by `kid` so rotation windows can verify both old + new tokens.
func verifyJWT(tokenStr string, keys JWTKeySet) (string, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid token format")
	}

	headerRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("header decode: %w", err)
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		return "", fmt.Errorf("header parse: %w", err)
	}
	if header.Alg != "" && header.Alg != "HS256" {
		return "", fmt.Errorf("unsupported jwt algorithm")
	}
	secret, ok := keys.secretFor(header.Kid)
	if !ok {
		return "", fmt.Errorf("unknown kid")
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
	return AuthMiddlewareWithKeys(JWTKeySet{ActiveSecret: jwtSecret})
}

// AuthMiddlewareWithKeys is the C7 kid-aware entry point. Use it from main.go
// when JWT_KID / JWT_SECRET_PREVIOUS / JWT_KID_PREVIOUS are configured for a
// rotation window.
func AuthMiddlewareWithKeys(keys JWTKeySet) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr := extractToken(c)
		if tokenStr == "" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing access token", nil)
			c.Abort()
			return
		}

		userID, err := verifyJWT(tokenStr, keys)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid access token", nil)
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
	return OptionalAuthMiddlewareWithKeys(JWTKeySet{ActiveSecret: jwtSecret})
}

func OptionalAuthMiddlewareWithKeys(keys JWTKeySet) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr := extractToken(c)
		if tokenStr == "" {
			c.Next()
			return
		}

		userID, err := verifyJWT(tokenStr, keys)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid access token", nil)
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
