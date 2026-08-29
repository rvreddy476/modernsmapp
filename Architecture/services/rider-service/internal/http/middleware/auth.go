package middleware

import (
	"crypto/hmac"
	"crypto/rsa"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/atpost/shared/api"
	"github.com/atpost/shared/tokenpolicy"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	ContextKeyUserID = "authenticated_user_id"
	ContextKeyRoles  = "authenticated_roles"
	ContextKeyScopes = "authenticated_scopes"

	RiderWriteSourceHeader = "X-Rider-Write-Source"
	GatewayWriteSource     = "api-gateway"
)

func isProductionEnv() bool {
	env := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	if env == "" {
		env = strings.ToLower(strings.TrimSpace(os.Getenv("ENVIRONMENT")))
	}
	return env == "production" || env == "prod" || env == "staging"
}

// JWTKeySet wraps tokenpolicy KeySet and Policy with internal service authentication.
type JWTKeySet struct {
	KeySet      tokenpolicy.KeySet
	Policy      tokenpolicy.Policy
	InternalKey string
}

// LoadJWTKeySet loads the token verification keys and policy from environment.
func LoadJWTKeySet() (JWTKeySet, error) {
	policy, err := tokenpolicy.LoadFromEnv()
	if err != nil {
		return JWTKeySet{}, fmt.Errorf("load token policy: %w", err)
	}

	internalKey := strings.TrimSpace(os.Getenv("INTERNAL_SERVICE_KEY"))
	if policy.Production && internalKey == "" {
		return JWTKeySet{}, errors.New("INTERNAL_SERVICE_KEY must be set in production/staging")
	}

	keySet := tokenpolicy.KeySet{
		ActiveKID:      strings.TrimSpace(os.Getenv("JWT_KEY_ID")),
		ActiveSecret:   strings.TrimSpace(os.Getenv("JWT_SECRET")),
		PreviousKID:    strings.TrimSpace(os.Getenv("JWT_PREVIOUS_KEY_ID")),
		PreviousSecret: strings.TrimSpace(os.Getenv("JWT_PREVIOUS_SECRET")),
		RSAKeys:        make(map[string]*rsa.PublicKey),
	}

	if pemStr := strings.TrimSpace(os.Getenv("JWT_PUBLIC_KEY_PEM")); pemStr != "" {
		pubKey, err := tokenpolicy.ParseRSAPublicKeyPEM(pemStr)
		if err != nil {
			return JWTKeySet{}, fmt.Errorf("parse JWT_PUBLIC_KEY_PEM: %w", err)
		}
		kid := keySet.ActiveKID
		if kid == "" {
			kid = "active"
		}
		keySet.RSAKeys[kid] = pubKey
	}

	if policy.Production && len(keySet.RSAKeys) == 0 {
		return JWTKeySet{}, errors.New("production requires at least one configured RSA public key (JWT_PUBLIC_KEY_PEM)")
	}

	return JWTKeySet{
		KeySet:      keySet,
		Policy:      policy,
		InternalKey: internalKey,
	}, nil
}

// AuthRequired returns a middleware enforcing cryptographic JWT authentication or trusted internal edge.
func AuthRequired(keys JWTKeySet) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. Bearer JWT validation
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "INVALID_AUTH_HEADER", "Authorization must be Bearer token", nil)
				c.Abort()
				return
			}

			tokenStr := strings.TrimSpace(parts[1])
			identity, err := tokenpolicy.Verify(tokenStr, keys.KeySet, keys.Policy, time.Now())
			if err != nil {
				api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "INVALID_TOKEN", "Token verification failed: "+err.Error(), nil)
				c.Abort()
				return
			}

			uid, err := uuid.Parse(identity.UserID)
			if err != nil || uid == uuid.Nil {
				api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "INVALID_USER_ID_CLAIM", "Token user ID is not a valid UUID", nil)
				c.Abort()
				return
			}

			c.Set(ContextKeyUserID, uid)
			c.Set(ContextKeyScopes, identity.Scopes)
			c.Next()
			return
		}

		// 2. Cryptographic internal service key check for verified gateway proxying
		internalHeader := c.GetHeader("X-Internal-Service-Key")
		if internalHeader != "" && keys.InternalKey != "" && hmac.Equal([]byte(internalHeader), []byte(keys.InternalKey)) {
			gwUserID := c.GetHeader("X-Verified-User-Id")
			if gwUserID == "" {
				gwUserID = c.GetHeader("X-User-Id")
			}
			if gwUserID == "" {
				gwUserID = c.GetHeader("X-User-ID")
			}
			if gwUserID != "" {
				if uid, err := uuid.Parse(gwUserID); err == nil && uid != uuid.Nil {
					c.Set(ContextKeyUserID, uid)
					if scopes := c.GetHeader("X-Scopes"); scopes != "" {
						c.Set(ContextKeyScopes, scopes)
					}
					c.Next()
					return
				}
			}
		}

		// Direct caller sending unverified X-User-ID without valid Bearer token and without internal edge proof MUST fail closed.
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "AUTH_REQUIRED", "Authorization Bearer token or trusted edge required", nil)
		c.Abort()
	}
}

// GetAuthenticatedUserID extracts the verified user UUID from context.
func GetAuthenticatedUserID(c *gin.Context) (uuid.UUID, bool) {
	val, exists := c.Get(ContextKeyUserID)
	if !exists {
		return uuid.Nil, false
	}
	uid, ok := val.(uuid.UUID)
	return uid, ok
}

// GetAuthenticatedScopes extracts the scopes from context.
func GetAuthenticatedScopes(c *gin.Context) string {
	if val, exists := c.Get(ContextKeyScopes); exists {
		if s, ok := val.(string); ok {
			return s
		}
	}
	return ""
}
