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
	issuer := strings.TrimSpace(cfg.Issuer)
	if issuer == "" {
		issuer = DefaultIssuer
	}

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			Issuer:    issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(cfg.TTL)),
		},
		SessionID: sessionID.String(),
		Scopes:    scopes,
		TokenType: AccessTokenType,
	}
	if aud := strings.TrimSpace(cfg.Audience); aud != "" {
		claims.Audience = jwt.ClaimStrings{aud}
	}

	if signingKey != nil {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		if cfg.RS256KID != "" {
			token.Header["kid"] = cfg.RS256KID
		}
		return token.SignedString(signingKey)
	}

	if cfg.HS256Secret == "" {
		return "", fmt.Errorf("accesstoken: no RSA signing key and no HS256 secret configured")
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	// Stamp `kid` so the verifier can pick the right secret during a rotation.
	// Tokens minted before kid support omit it and fall back to the active
	// secret on the verifier side.
	if cfg.HS256KID != "" {
		token.Header["kid"] = cfg.HS256KID
	}
	return token.SignedString([]byte(cfg.HS256Secret))
}
