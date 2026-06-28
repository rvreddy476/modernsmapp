package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds runtime configuration sourced from environment variables.
type Config struct {
	HTTPPort                 string
	PostgresDSN              string
	RedisAddr                string
	KafkaBrokers             []string
	KafkaTopic               string
	OTPBypassCode            string
	OTPDigits                int
	OTPExpiry                time.Duration
	OTPMaxAttempts           int
	// BcryptCost is the bcrypt work factor used for password hashing.
	// Audit A9: default was bcrypt.DefaultCost (10), but under high
	// login load this was the throughput bottleneck. Make it tunable
	// so production can dial up the cost for sensitive deploys (12+)
	// and CI / test can dial down to 4 for fast suites. 10 is the
	// safe default for everyone else.
	BcryptCost               int
	AccessTokenTTL           time.Duration
	RefreshTokenTTL          time.Duration
	JWTSecret                string
	// C7: JWT key versioning. JWTKID is stamped into the `kid` header of
	// every minted access token so future verification can pick the right
	// secret. JWTSecretPrevious + JWTKIDPrevious allow rolling rotation:
	// during the cutover, both kids verify; once tokens minted with the
	// old kid have expired (≤ AccessTokenTTL), the previous can be
	// unset. Tokens with no `kid` (legacy) fall back to JWTSecret.
	JWTKID                   string
	JWTSecretPrevious        string
	JWTKIDPrevious           string
	CookieDomain             string
	CookieSecure             bool
	TrustedProxies           []string
	InternalServiceKey       string
	TwoFAIssuer              string
	FrontendURL              string
	OAuth                    *OAuthConfig
	RateLimitEnabled         bool
	// LoginAnomalyEnforce controls A13 anomaly enforcement at login.
	// Allowed values:
	//   "shadow"  — log anomaly + issue session (legacy behaviour, default).
	//   "enforce" — gate the session behind step-up verification when the
	//               risk band warrants it (new IP+device combo). Safe to
	//               toggle at runtime; no migration required.
	// Keep "shadow" as default so rolling out the feature flag is opt-in
	// and ops can revert without a deploy if step-up causes UX issues.
	LoginAnomalyEnforce      string
	MiniAppSessionTTL        time.Duration
	MiniAppSessionIssuer     string
	MiniAppSessionKeyID      string
	MiniAppSessionPrivateKey string
	// Scope allowlists — the SERVER-SIDE source of truth for authorization
	// scopes that get stamped into the signed access token. Previously the
	// platform had NO server-side notion of "admin": clients declared their
	// own privileges via an X-Scopes header, which the gateway trusted. These
	// env allowlists (comma-separated user UUIDs) move that decision to the
	// server. A full RBAC roles table is the next step; this is the secure,
	// reversible v1 that closes the privilege-escalation hole today.
	ScopeAdminUserIDs      map[string]struct{}
	ScopeModeratorUserIDs  map[string]struct{}
	ScopeSuperadminUserIDs map[string]struct{}
	// RS256 access-token signing (optional, additive). When
	// AccessTokenPrivateKeyPEM is set, access tokens are signed with RS256 and
	// verifiers use the matching public key — so a compromised downstream
	// service (which holds only the public key) can no longer mint tokens.
	// When empty, signing stays HS256 (shared secret) for backward compat, so
	// enabling RS256 is a deliberate, reversible opt-in. AccessTokenRS256KID is
	// stamped as the token `kid` and must match the verifiers' configured kid.
	AccessTokenPrivateKeyPEM string
	AccessTokenRS256KID      string
	// RequireMFAForPrivileged, when true, blocks privileged actions (role
	// management) unless the acting user has 2FA enabled. Default off so dev /
	// first-superadmin bootstrap isn't locked out before enrolling MFA.
	RequireMFAForPrivileged bool
	// WebAuthn / passkey relying-party config (used by the `webauthn`-tagged
	// ceremony). RPID is the registrable domain (e.g. "cleestudio.com");
	// RPOrigins are the full origins allowed to authenticate.
	WebAuthnRPID          string
	WebAuthnRPDisplayName string
	WebAuthnRPOrigins     []string
}

// EnvRolesForUser returns the raw roles assigned to a user via the env
// allowlists (the bootstrap source of truth, alongside the DB roles table).
func (c *Config) EnvRolesForUser(userID string) []string {
	var roles []string
	if _, ok := c.ScopeSuperadminUserIDs[userID]; ok {
		roles = append(roles, "superadmin")
	}
	if _, ok := c.ScopeAdminUserIDs[userID]; ok {
		roles = append(roles, "admin")
	}
	if _, ok := c.ScopeModeratorUserIDs[userID]; ok {
		roles = append(roles, "moderator")
	}
	return roles
}

// ExpandRoles turns a set of raw roles into the space-separated scope string
// embedded in the access-token `scopes` claim. superadmin implies admin+
// moderator; admin implies moderator. Order is stable for deterministic tokens.
// Returns "" when no privileged role is present.
func ExpandRoles(roles []string) string {
	set := map[string]struct{}{}
	for _, r := range roles {
		switch r {
		case "superadmin":
			set["superadmin"], set["admin"], set["moderator"] = struct{}{}, struct{}{}, struct{}{}
		case "admin":
			set["admin"], set["moderator"] = struct{}{}, struct{}{}
		case "moderator":
			set["moderator"] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for _, s := range []string{"superadmin", "admin", "moderator"} {
		if _, ok := set[s]; ok {
			out = append(out, s)
		}
	}
	return strings.Join(out, " ")
}

// ScopesForUser returns the env-derived scopes for a user (bootstrap path).
// The service unions these with the DB roles table at mint time.
func (c *Config) ScopesForUser(userID string) string {
	return ExpandRoles(c.EnvRolesForUser(userID))
}

// Load reads configuration from environment variables and applies sensible defaults for local development.
func Load() *Config {
	cfg := &Config{
		HTTPPort:                 getEnv("HTTP_PORT", "8081"),
		PostgresDSN:              getEnv("DATABASE_URL", getEnv("POSTGRES_DSN", "")),
		RedisAddr:                getEnv("REDIS_ADDR", "localhost:6379"),
		KafkaBrokers:             splitAndClean(getEnv("KAFKA_BROKERS", "localhost:9092")),
		KafkaTopic:               getEnv("KAFKA_TOPIC", "identity.events.v1"),
		OTPBypassCode:            getEnv("OTP_BYPASS_CODE", ""),
		OTPDigits:                getEnvInt("OTP_DIGITS", 6),
		OTPExpiry:                getEnvDuration("OTP_EXPIRY", 5*time.Minute),
		OTPMaxAttempts:           getEnvInt("OTP_MAX_ATTEMPTS", 5),
		BcryptCost:               getEnvInt("BCRYPT_COST", 10),
		AccessTokenTTL:           getEnvDuration("ACCESS_TOKEN_TTL", 15*time.Minute),
		RefreshTokenTTL:          getEnvDuration("REFRESH_TOKEN_TTL", 30*24*time.Hour),
		JWTSecret:                getEnv("JWT_SECRET", ""),
		JWTKID:                   getEnv("JWT_KID", "v1"),
		JWTSecretPrevious:        getEnv("JWT_SECRET_PREVIOUS", ""),
		JWTKIDPrevious:           getEnv("JWT_KID_PREVIOUS", ""),
		CookieDomain:             getEnv("COOKIE_DOMAIN", ""),
		CookieSecure:             getEnvBool("COOKIE_SECURE", false),
		TrustedProxies:           splitAndClean(getEnv("TRUSTED_PROXIES", "")),
		InternalServiceKey:       getEnv("INTERNAL_SERVICE_KEY", ""),
		TwoFAIssuer:              getEnv("TWOFA_ISSUER", "AtPost"),
		FrontendURL:              getEnv("FRONTEND_URL", "http://localhost:3000"),
		OAuth:                    LoadOAuth(),
		RateLimitEnabled:         getEnvBool("RATE_LIMIT_ENABLED", true),
		LoginAnomalyEnforce:      strings.ToLower(getEnv("LOGIN_ANOMALY_ENFORCE", "shadow")),
		MiniAppSessionTTL:        getEnvDuration("MINI_APP_SESSION_TTL", 5*time.Minute),
		MiniAppSessionIssuer:     getEnv("MINI_APP_SESSION_ISSUER", "atpost-mini-app-runtime"),
		MiniAppSessionKeyID:      getEnv("MINI_APP_SESSION_KEY_ID", "mini-app-session-1"),
		MiniAppSessionPrivateKey: getEnv("MINI_APP_SESSION_PRIVATE_KEY_PEM", ""),
		ScopeAdminUserIDs:        splitToSet(getEnv("ADMIN_USER_IDS", "")),
		ScopeModeratorUserIDs:    splitToSet(getEnv("MODERATOR_USER_IDS", "")),
		ScopeSuperadminUserIDs:   splitToSet(getEnv("SUPERADMIN_USER_IDS", "")),
		AccessTokenPrivateKeyPEM: getEnv("JWT_PRIVATE_KEY_PEM", ""),
		AccessTokenRS256KID:      getEnv("JWT_RS256_KID", "rsa-1"),
		RequireMFAForPrivileged:  getEnvBool("REQUIRE_MFA_FOR_PRIVILEGED", false),
		WebAuthnRPID:             getEnv("WEBAUTHN_RP_ID", "localhost"),
		WebAuthnRPDisplayName:    getEnv("WEBAUTHN_RP_NAME", "atPost"),
		WebAuthnRPOrigins:        splitAndClean(getEnv("WEBAUTHN_RP_ORIGINS", "http://localhost:3000")),
	}
	return cfg
}

// splitToSet parses a comma-separated env value into a set of trimmed,
// non-empty tokens for O(1) membership checks.
func splitToSet(v string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			set[p] = struct{}{}
		}
	}
	return set
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func splitAndClean(val string) []string {
	parts := strings.Split(val, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			return parsed
		}
	}
	return def
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func getEnvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			return parsed
		}
	}
	return def
}
