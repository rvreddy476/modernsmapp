package http

import (
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

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
		// Auth is header-based (Authorization bearer), not cookie-based, so
		// credentials are never needed. `Allow-Credentials: true` combined
		// with a wildcard origin is invalid per the CORS spec (browsers
		// reject the response for credentialed requests) — do not add it.
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-User-Id, X-Request-Id, X-Session-Id, Idempotency-Key")
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
	ActiveKID      string
	ActiveSecret   string
	PreviousKID    string
	PreviousSecret string
	// RSAKeys (optional) verify RS256 tokens, keyed by `kid`. HS256 stays
	// active in parallel so pre-cutover tokens keep verifying.
	RSAKeys map[string]*rsa.PublicKey
}

func (k JWTKeySet) rsaFor(kid string) (*rsa.PublicKey, bool) {
	if len(k.RSAKeys) == 0 {
		return nil, false
	}
	if kid != "" {
		pub, ok := k.RSAKeys[kid]
		return pub, ok
	}
	if len(k.RSAKeys) == 1 {
		for _, pub := range k.RSAKeys {
			return pub, true
		}
	}
	return nil, false
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
		// Internal-only dating-match conversation create: the caller is
		// dating-service's match saga (service-to-service — no end-user
		// JWT exists). CreateDatingMatchConversation authenticates via
		// X-Internal-Service-Key itself and hard-refuses when the key is
		// absent or mismatched, so skipping the bearer gate here does not
		// expose the endpoint.
		if strings.HasSuffix(c.Request.URL.Path, "/conversations/dating-match") {
			c.Next()
			return
		}
		// RSA-only deployments (JWT_SECRET retired post-RS256-cutover) are
		// valid — refuse only when NO verification key is configured at all.
		if strings.TrimSpace(keys.ActiveSecret) == "" && len(keys.RSAKeys) == 0 {
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
		userID, err := parseAndValidateJWTWithKeys(token, keys)
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
	return parseAndValidateJWTWithKeys(token, JWTKeySet{ActiveSecret: string(secret)})
}

func parseAndValidateJWTWithKeys(token string, keys JWTKeySet) (string, error) {
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
	kid, _ := header["kid"].(string)
	signingInput := parts[0] + "." + parts[1]
	// Accept HS256 and RS256 only (no `none`/alg-confusion). RS256 verifies
	// with a public key this service can't mint with; HS256 stays accepted in
	// parallel so tokens minted before the cutover keep working.
	switch alg {
	case "HS256":
		secret, ok := keys.secretFor(kid)
		if !ok {
			return "", errors.New("unknown kid")
		}
		mac := hmac.New(sha256.New, secret)
		_, _ = mac.Write([]byte(signingInput))
		if !hmac.Equal(signatureRaw, mac.Sum(nil)) {
			return "", errors.New("invalid token signature")
		}
	case "RS256":
		pub, ok := keys.rsaFor(kid)
		if !ok {
			return "", errors.New("unknown kid")
		}
		if err := verifyRS256(signingInput, signatureRaw, pub); err != nil {
			return "", errors.New("invalid token signature")
		}
	default:
		return "", errors.New("unsupported jwt algorithm")
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
