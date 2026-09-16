// Package accesstoken is the production access-token minter.
//
// Module 3 LB-1. It exists as an importable `pkg/` package for one reason: the
// claim set it produces is a CONTRACT with the API gateway's verifier, and the
// only honest way to test a contract is to exercise both real implementations.
//
// The previous attempt did that by making identity-auth-service require
// github.com/atpost/api-gateway with a source-local `replace`. That broke the
// auth container build: the Dockerfile copies only `shared/` and
// `services/auth-service/` into the build context, so the replace target
// (`../../../Architecture/services/api-gateway`) does not exist inside the
// image and `go mod download` fails. A test dependency had been added to a
// production module and pointed outside that module's image context.
//
// The fix is directional. Neither deployable service depends on the other:
// this package is imported by the auth service in production AND by a
// CI-only contract module that also imports the gateway's verifier. The
// contract proof stays real — the same function that mints in production is
// the one under test — while both images build from their own context.
package accesstoken

import (
	"crypto/rsa"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// DefaultIssuer is used when no issuer is configured. Production sets one
// explicitly and refuses to start without it.
const DefaultIssuer = "auth-service"

// AccessTokenType is the `typ` claim value the gateway requires. A refresh
// token presented as a bearer credential must not authenticate an API call,
// and the gateway can only tell them apart if the mint side says which is which.
const AccessTokenType = "access"

// Claims is the access-token claim set.
//
// Every field here is required by the gateway's policy
// (api-gateway/pkg/tokenpolicy). Removing one does not degrade
// authentication — it breaks it, platform-wide, at the edge.
type Claims struct {
	jwt.RegisteredClaims
	// SessionID is `sid`. Production requires it so session revocation can be
	// reasoned about.
	SessionID string `json:"sid"`
	// Scopes is a space-separated authorization scope list resolved
	// SERVER-SIDE at mint time. A client can never influence it — it is bound
	// to the user id inside the signature.
	Scopes string `json:"scopes,omitempty"`
	// TokenType is `typ`.
	TokenType string `json:"typ"`

	// ── Admin session claims (admin console Wave 0, A2) ──────────────────────
	//
	// GATEWAY CONTRACT. The api-gateway will verify these and stamp them onto
	// the proxied request for admin-service and every admin route, replacing
	// any client-supplied copy:
	//
	//	X-Auth-Time    = auth_time   (unix seconds; absent when the claim is)
	//	X-Admin-MFA    = admin_mfa   ("true" or "false"; absent claim = "false")
	//	X-Step-Up-At   = step_up_at  (unix seconds; absent when the claim is)
	//
	// Downstream rules (admin-service): an admin route requires
	// X-Admin-MFA=true; money actions, KYC/document reveal, bans and role
	// changes additionally require now - X-Step-Up-At <= StepUpValidity.

	// AuthTime is `auth_time`: unix seconds of the last FULL authentication
	// (password / OAuth / passkey, plus a second factor when one was asked
	// for). Carried through refresh unchanged; a step-up does not move it.
	AuthTime int64 `json:"auth_time,omitempty"`
	// AMR is `amr` (RFC 8176 style): how this session authenticated, e.g.
	// ["pwd"], ["pwd","otp"], ["fed"], ["hwk"]. "otp" means a TOTP challenge
	// was completed ON THIS SESSION. Carried through refresh unchanged.
	AMR []string `json:"amr,omitempty"`
	// AdminMFA is `admin_mfa`: true only when the user resolves to at least one
	// admin permission AND this session's amr includes "otp". Always present;
	// an admin without it gets no admin scope in `scopes` either.
	AdminMFA bool `json:"admin_mfa"`
	// StepUpAt is `step_up_at`: unix seconds of the last POST /v1/auth/step-up
	// on this session. Only the token that step-up returns carries it; refresh
	// drops it. Valid for StepUpValidity.
	StepUpAt int64 `json:"step_up_at,omitempty"`
}

// AdminSessionMaxTTL caps the access-token lifetime of a token that carries
// admin_mfa=true, whatever the consumer ACCESS_TOKEN_TTL is.
const AdminSessionMaxTTL = 15 * time.Minute

// StepUpValidity is how long a step_up_at stays usable for a sensitive admin
// action (money, KYC reveal, bans, role changes). Enforced by auth-service for
// role changes and force logout, and by admin-service for everything else.
const StepUpValidity = 300 * time.Second

// StepUpFresh reports whether a step_up_at (unix seconds) is still valid at
// now. A zero value, or one more than a minute in the future (clock skew
// allowance), is not.
func StepUpFresh(stepUpAt int64, now time.Time) bool {
	if stepUpAt <= 0 {
		return false
	}
	age := now.Unix() - stepUpAt // whole seconds, like the claim
	if age < -60 {
		return false
	}
	return age <= int64(StepUpValidity/time.Second)
}

// Session is the per-session part of the claim set. The zero value mints a
// token with no auth_time/amr/step_up_at and admin_mfa=false.
type Session struct {
	AuthTime time.Time
	AMR      []string
	AdminMFA bool
	StepUpAt time.Time
}

// Config is the minting configuration.
//
// It deliberately holds no store, logger or context: minting is a pure
// function of configuration, key material and the identity being minted, which
// is what lets the contract test call the real thing without standing up a
// database.
type Config struct {
	// Issuer must be a member of the gateway's JWT_ISSUER allowlist.
	Issuer string
	// Audience must equal the gateway's JWT_AUDIENCE exactly. A mismatch is a
	// silent, total authentication failure: both services start happily and
	// every request 401s.
	Audience string
	// TTL is the access-token lifetime.
	TTL time.Duration
	// RS256KID is stamped into the header so the verifier can select the key.
	// It must match the gateway's JWT_RS256_KID or every token fails with
	// "unknown kid".
	RS256KID string
	// HS256KID and HS256Secret drive the development/legacy symmetric path.
	// RS256 is the only algorithm the gateway accepts in production.
	HS256KID    string
	HS256Secret string
}

// Mint builds and signs an access token.
//
// signingKey non-nil selects RS256, which is the production path: only this
// service holds the private key, so verifiers can check a token but never
// create one. With a shared HS256 secret every verifier is also an identity
// provider.
func Mint(cfg Config, signingKey *rsa.PrivateKey, userID, sessionID uuid.UUID, scopes string, now time.Time) (string, error) {
	token, _, err := MintSession(cfg, signingKey, userID, sessionID, scopes, Session{}, now)
	return token, err
}

// TTLFor returns the access-token lifetime for a session: the configured TTL,
// capped at AdminSessionMaxTTL when the token carries admin_mfa=true.
func TTLFor(cfg Config, sess Session) time.Duration {
	if sess.AdminMFA && (cfg.TTL <= 0 || cfg.TTL > AdminSessionMaxTTL) {
		return AdminSessionMaxTTL
	}
	return cfg.TTL
}

// MintSession is Mint with the session claims (auth_time, amr, admin_mfa,
// step_up_at). It returns the token's expiry, which is shorter than cfg.TTL
// for an admin-MFA token (TTLFor).
func MintSession(cfg Config, signingKey *rsa.PrivateKey, userID, sessionID uuid.UUID, scopes string, sess Session, now time.Time) (string, time.Time, error) {
	issuer := strings.TrimSpace(cfg.Issuer)
	if issuer == "" {
		issuer = DefaultIssuer
	}
	expiresAt := now.Add(TTLFor(cfg, sess))

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			Issuer:    issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
		SessionID: sessionID.String(),
		Scopes:    scopes,
		TokenType: AccessTokenType,
		AdminMFA:  sess.AdminMFA,
	}
	if !sess.AuthTime.IsZero() {
		claims.AuthTime = sess.AuthTime.Unix()
	}
	if len(sess.AMR) > 0 {
		claims.AMR = append([]string(nil), sess.AMR...)
	}
	if !sess.StepUpAt.IsZero() {
		claims.StepUpAt = sess.StepUpAt.Unix()
	}
	if aud := strings.TrimSpace(cfg.Audience); aud != "" {
		claims.Audience = jwt.ClaimStrings{aud}
	}

	if signingKey != nil {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		if cfg.RS256KID != "" {
			token.Header["kid"] = cfg.RS256KID
		}
		signed, err := token.SignedString(signingKey)
		return signed, expiresAt, err
	}

	if cfg.HS256Secret == "" {
		return "", time.Time{}, fmt.Errorf("accesstoken: no RSA signing key and no HS256 secret configured")
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	// Stamp `kid` so the verifier can pick the right secret during a rotation.
	// Tokens minted before kid support omit it and fall back to the active
	// secret on the verifier side.
	if cfg.HS256KID != "" {
		token.Header["kid"] = cfg.HS256KID
	}
	signed, err := token.SignedString([]byte(cfg.HS256Secret))
	return signed, expiresAt, err
}
