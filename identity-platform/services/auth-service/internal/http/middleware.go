package http

import (
	"crypto/hmac"
	"crypto/rsa"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-auth-service/pkg/accesstoken"
	"github.com/atpost/identity-shared/api"
	identitymiddleware "github.com/atpost/identity-shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type AccessClaims struct {
	jwt.RegisteredClaims
	SessionID string `json:"sid"`
	// Admin session claims (A2); see pkg/accesstoken.Claims for meaning.
	AuthTime int64    `json:"auth_time,omitempty"`
	AMR      []string `json:"amr,omitempty"`
	AdminMFA bool     `json:"admin_mfa"`
	StepUpAt int64    `json:"step_up_at,omitempty"`
	// SessionKind is `sk`: "admin" only on an admin console session.
	SessionKind string `json:"sk,omitempty"`
}

// sessionAuth converts verified claims into the service's context value.
func (c *AccessClaims) sessionAuth() service.SessionAuth {
	sid, _ := uuid.Parse(c.SessionID)
	return service.SessionAuth{
		SessionID: sid,
		AuthTime:  c.AuthTime,
		AMR:       c.AMR,
		AdminMFA:  c.AdminMFA,
		StepUpAt:  c.StepUpAt,
	}
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

func AuthMiddleware(jwtSecret string) gin.HandlerFunc {
	return AuthMiddlewareWithRevoke(jwtSecret, nil)
}

// JWTKeySet describes the kid-versioned secret set used to verify access
// tokens. The active (kid, secret) is used for new signatures; the previous
// pair (when set) verifies tokens minted before the most recent rotation.
// A token with no `kid` (legacy, pre-C7) falls back to the active secret.
type JWTKeySet struct {
	ActiveKID      string
	ActiveSecret   string
	PreviousKID    string
	PreviousSecret string
	// RSAPublic (optional) verifies RS256 access tokens. When auth-service is
	// configured to sign RS256, this is its own public key so its protected
	// endpoints accept the tokens it mints. HS256 stays accepted in parallel.
	RSAPublic *rsa.PublicKey
	RSAKID    string
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

// AuthMiddlewareWithKeys is the kid-aware verify path. Use this when the
// caller has loaded a JWTKeySet from config (active + optional previous
// during rotation). For backward compatibility, AuthMiddleware /
// AuthMiddlewareWithRevoke continue to accept a single secret.
func AuthMiddlewareWithKeys(keys JWTKeySet, rdb *redis.Client) gin.HandlerFunc {
	return authMiddleware(keys, rdb)
}

// AuthMiddlewareWithRevoke is the cache-backed variant. When `rdb` is
// non-nil, every request looks up `sess_revoked:<sid>` to short-circuit
// access tokens whose session has been revoked since the token was
// minted. Fail-open on Redis errors — the access-token TTL is the
// upper bound on revocation lag anyway, so a Redis blip doesn't
// degrade security beyond that.
//
// A10: at billions-of-users scale the session table itself is too hot
// to consult on every authenticated request. Redis is the right tier.
// Revocation entries are TTL'd to the access-token life so the cache
// drains naturally without a cleaner job.
func AuthMiddlewareWithRevoke(jwtSecret string, rdb *redis.Client) gin.HandlerFunc {
	return authMiddleware(JWTKeySet{ActiveSecret: jwtSecret}, rdb)
}

func authMiddleware(keys JWTKeySet, rdb *redis.Client) gin.HandlerFunc {
	return authMiddlewareFor(keys, rdb, func(c *gin.Context) (string, identitymiddleware.CredentialSource) {
		return identitymiddleware.ReadAccessToken(c, accessTokenCookieName)
	}, false)
}

// AdminAuthMiddlewareWithKeys authenticates the admin console session routes
// (/v1/auth/admin-session/*). Unlike the consumer middleware it reads ONLY the
// admin_access_token cookie — never an Authorization header, never the
// consumer access_token cookie — and accepts only a token whose sk claim says
// admin. A consumer session therefore cannot reach these routes by any
// transport, and CSRF is always enforced (the credential is always ambient).
func AdminAuthMiddlewareWithKeys(keys JWTKeySet, rdb *redis.Client) gin.HandlerFunc {
	return authMiddlewareFor(keys, rdb, func(c *gin.Context) (string, identitymiddleware.CredentialSource) {
		if token, err := c.Cookie(adminAccessCookieName); err == nil && token != "" {
			return token, identitymiddleware.CredentialCookie
		}
		return "", ""
	}, true)
}

func authMiddlewareFor(keys JWTKeySet, rdb *redis.Client, read func(*gin.Context) (string, identitymiddleware.CredentialSource), adminOnly bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr, credentialSource := read(c)

		if tokenStr == "" {
			api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing access token", nil, nil)
			c.Abort()
			return
		}

		claims := &AccessClaims{}
		// Audit A1: defense-in-depth algorithm pin. golang-jwt/jwt v5
		// already rejects `alg: none` and prevents RSA→HMAC confusion
		// (type-asserts the keyfunc's return against the algorithm's
		// expected key type), but we pin the algorithm explicitly here
		// so a future library downgrade or accidental method
		// registration can't reintroduce the classic alg-confusion
		// vulnerability. Tokens not signed with HS256 are rejected.
		//
		// C7: pick the secret by `kid`. Tokens minted before C7 omit
		// `kid` entirely — secretFor("") falls back to the active key.
		token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
			// Accept HS256 (shared secret) and RS256 (public key) only. Any
			// other method — including `none` — is rejected. golang-jwt also
			// type-checks the returned key against the method, blocking the
			// classic RSA↔HMAC alg-confusion attack.
			kid, _ := t.Header["kid"].(string)
			switch t.Method.(type) {
			case *jwt.SigningMethodHMAC:
				secret, ok := keys.secretFor(kid)
				if !ok {
					return nil, fmt.Errorf("unknown kid: %s", kid)
				}
				return secret, nil
			case *jwt.SigningMethodRSA:
				if keys.RSAPublic == nil {
					return nil, fmt.Errorf("RS256 not configured")
				}
				if keys.RSAKID != "" && kid != "" && kid != keys.RSAKID {
					return nil, fmt.Errorf("unknown kid: %s", kid)
				}
				return keys.RSAPublic, nil
			default:
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
		})
		if err != nil || !token.Valid {
			api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid access token", nil, nil)
			c.Abort()
			return
		}
		if adminOnly && claims.SessionKind != accesstoken.SessionKindAdmin {
			api.Error(c.Writer, http.StatusUnauthorized, CodeWrongSession, "An admin console session is required", nil, nil)
			c.Abort()
			return
		}

		// A10 — session revocation check. Cache-only; the session
		// table itself is not hit. Empty `sid` (legacy tokens predating
		// this change) bypass — the next refresh will mint one.
		if rdb != nil && claims.SessionID != "" {
			val, err := rdb.Get(c.Request.Context(), "sess_revoked:"+claims.SessionID).Result()
			if err == nil && val != "" {
				api.Error(c.Writer, http.StatusUnauthorized, "SESSION_REVOKED", "Session has been revoked", nil, nil)
				c.Abort()
				return
			}
			// redis.Nil (key absent) + any transient errors → fail open.
		}

		c.Request.Header.Set("X-User-Id", claims.Subject)
		// The verified session claims ride on the request context, never on
		// a header, so nothing a client sends can stand in for them.
		c.Request = c.Request.WithContext(service.WithSessionAuth(c.Request.Context(), claims.sessionAuth()))
		identitymiddleware.MarkAuthenticatedCredential(c, credentialSource)
		c.Next()
	}
}

func RequireCSRFMiddleware() gin.HandlerFunc {
	return identitymiddleware.RequireCSRF(csrfCookieName, "X-CSRF-Token")
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
