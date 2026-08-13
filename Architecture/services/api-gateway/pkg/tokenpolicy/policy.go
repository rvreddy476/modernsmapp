package tokenpolicy

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Module 3 M3-P0-2 — edge token and identity-header integrity.
//
// The gateway is the only component that converts a bearer token into the
// trusted X-User-Id / X-Verified-User-Id / X-Scopes headers every downstream
// service believes. Its verifier is therefore the platform's authentication
// boundary, and it was too permissive:
//
//   - a token with NO `exp` claim was accepted forever (`if claims.Exp != 0`);
//   - `iss` and `aud` were never checked, so a token minted by any other
//     system holding the same secret — or by a different environment of this
//     system — authenticated here;
//   - `sub` was not required to be a non-empty UUID, so a malformed or empty
//     subject could produce an identity header;
//   - refresh tokens were indistinguishable from access tokens;
//   - `nbf` was ignored;
//   - HS256 remained accepted in production, which means any service holding
//     the shared secret can MINT platform identities rather than merely
//     verify them.
//
// This file holds the policy in one place so it can be unit-tested without
// standing up the proxy.

// Policy is the set of claim requirements the edge enforces.
type Policy struct {
	// Production hardens several rules that stay relaxed in development.
	Production bool
	// AllowedIssuers must contain the token's `iss`. Empty in dev = skip.
	AllowedIssuers []string
	// RequiredAudience must appear in the token's `aud`. Empty in dev = skip.
	RequiredAudience string
	// AllowHS256 permits symmetric tokens. MUST be false in production:
	// a verifier holding an HMAC secret can mint tokens, so symmetric keys
	// make every downstream holder of the secret an identity provider.
	AllowHS256 bool
	// ClockSkew tolerance applied to exp/nbf/iat.
	ClockSkew time.Duration
	// QueryTokenPaths is the explicit allowlist of path prefixes that may
	// carry a token in the query string. Everything else must use the
	// Authorization header or the cookie. Query strings leak through access
	// logs, browser history, Referer headers, and analytics.
	QueryTokenPaths []string
}

// ProductionEnvVars are the variables consulted, in order, to decide whether
// this process is running in production.
//
// Module 3 SR-1: ENV is in this list because it is what the Helm values
// actually set (`env: ENV: prod` in deploy/services/*/values-prod.yaml). Only
// APP_ENV and ENVIRONMENT were checked before, so the hardened production
// rules — RS256-only, mandatory issuer/audience/sid — silently did not apply
// in production, which is the one place they exist for. The deployment files
// now set APP_ENV too; ENV stays supported so an older manifest cannot
// downgrade a running cluster to development rules.
var ProductionEnvVars = []string{"APP_ENV", "ENVIRONMENT", "ENV"}

// IsProductionEnv reports whether this process should apply production rules.
// Both the gateway and auth-service use this exact logic; if they disagree,
// one side hardens and the other does not and every real token is rejected.
func IsProductionEnv() bool {
	for _, key := range ProductionEnvVars {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
		case "production", "prod":
			return true
		case "":
			continue
		default:
			// An explicit non-production value on a higher-priority variable
			// wins: APP_ENV=development must not be overridden by a stale
			// ENV=prod left in a manifest.
			return false
		}
	}
	return false
}

// LoadFromEnv builds the policy and FAILS CLOSED in production.
//
// Returning an error here is deliberate: the caller exits. A gateway that
// starts with a weak token policy is worse than one that does not start,
// because the weakness is silent and platform-wide.
func LoadFromEnv() (Policy, error) {
	production := IsProductionEnv()

	p := Policy{
		Production:       production,
		RequiredAudience: strings.TrimSpace(os.Getenv("JWT_AUDIENCE")),
		ClockSkew:        60 * time.Second,
	}
	if iss := strings.TrimSpace(os.Getenv("JWT_ISSUER")); iss != "" {
		for _, s := range strings.Split(iss, ",") {
			if s = strings.TrimSpace(s); s != "" {
				p.AllowedIssuers = append(p.AllowedIssuers, s)
			}
		}
	}
	if raw := strings.TrimSpace(os.Getenv("GATEWAY_QUERY_TOKEN_PATHS")); raw != "" {
		for _, s := range strings.Split(raw, ",") {
			if s = strings.TrimSpace(s); s != "" {
				p.QueryTokenPaths = append(p.QueryTokenPaths, s)
			}
		}
	}

	// HS256: allowed only outside production, and only when explicitly not
	// disabled. The default in dev is "allowed" so local work is unaffected.
	p.AllowHS256 = !production && strings.TrimSpace(os.Getenv("JWT_DISABLE_HS256")) != "true"

	if production {
		if len(p.AllowedIssuers) == 0 {
			return p, errors.New("JWT_ISSUER must be set in production: without it a token " +
				"minted by any other system holding the key authenticates here")
		}
		if p.RequiredAudience == "" {
			return p, errors.New("JWT_AUDIENCE must be set in production: without it a token " +
				"minted for a different audience of this platform authenticates here")
		}
		if strings.TrimSpace(os.Getenv("JWT_PUBLIC_KEY_PEM")) == "" {
			return p, errors.New("JWT_PUBLIC_KEY_PEM must be set in production: RS256 is the only " +
				"accepted production algorithm, so the gateway needs a public key and must " +
				"never hold a signing secret")
		}
		if len(p.QueryTokenPaths) > 0 {
			// Permitted but must be deliberate and narrow; log-worthy, not fatal.
			for _, path := range p.QueryTokenPaths {
				if path == "/" || path == "" {
					return p, errors.New("GATEWAY_QUERY_TOKEN_PATHS must not contain a catch-all " +
						"prefix: query tokens leak through logs, history and Referer headers")
				}
			}
		}
	}
	return p, nil
}

// QueryTokenAllowed reports whether a token may be read from the query string
// for this request path.
//
// Matching is EXACT or on a path-segment boundary. Raw prefix matching was
// wrong: an allowlist entry of "/v1/ws" also matched "/v1/ws-evil", so
// registering one realtime path silently opened query-token acceptance for
// every sibling route an attacker could name.
func (p Policy) QueryTokenAllowed(path string) bool {
	for _, allowed := range p.QueryTokenPaths {
		if path == allowed {
			return true
		}
		// A descendant must be separated by "/", never by an arbitrary
		// character. "/v1/ws/connect" qualifies; "/v1/ws-evil" does not.
		prefix := strings.TrimSuffix(allowed, "/")
		if strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// Claims is the claim subset the edge policy inspects.
type Claims struct {
	Sub       string `json:"sub"`
	UserID    string `json:"user_id"`
	Exp       int64  `json:"exp"`
	Nbf       int64  `json:"nbf"`
	Iat       int64  `json:"iat"`
	Iss       string `json:"iss"`
	Aud       any    `json:"aud"` // string or []string per RFC 7519
	Sid       string `json:"sid"`
	TokenType string `json:"typ"`
	Type      string `json:"type"`
	Scopes    string `json:"scopes"`
	DeviceID  string `json:"device_id"`
}

// ValidateClaims enforces the policy. Every failure is a hard reject: there
// is no "probably fine" branch, because the output of this function becomes a
// trusted identity header.
func ValidateClaims(c Claims, alg string, p Policy, now time.Time) error {
	// Algorithm. In production only RS256 is acceptable — a symmetric key
	// would let every verifier mint identities.
	switch alg {
	case "RS256":
	case "HS256":
		if !p.AllowHS256 {
			return fmt.Errorf("HS256 access tokens are not accepted in production; " +
				"a shared secret would let any verifier mint platform identities")
		}
	default:
		return fmt.Errorf("unsupported jwt algorithm %q", alg)
	}

	// exp is MANDATORY. A token without it never expires.
	if c.Exp == 0 {
		return errors.New("token has no exp claim")
	}
	if now.Add(-p.ClockSkew).Unix() > c.Exp {
		return errors.New("token expired")
	}
	// nbf, when present, must have passed.
	if c.Nbf != 0 && now.Add(p.ClockSkew).Unix() < c.Nbf {
		return errors.New("token not yet valid")
	}

	// Token type must be an access token. A refresh token presented as a
	// bearer credential must not authenticate an API call.
	typ := c.TokenType
	if typ == "" {
		typ = c.Type
	}
	if typ != "" && !strings.EqualFold(typ, "access") && !strings.EqualFold(typ, "at+jwt") {
		return fmt.Errorf("token type %q is not an access token", typ)
	}
	if p.Production && typ == "" {
		return errors.New("token has no type claim; production requires an explicit access token")
	}

	// Issuer.
	if len(p.AllowedIssuers) > 0 {
		ok := false
		for _, iss := range p.AllowedIssuers {
			if c.Iss == iss {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("issuer %q is not allowed", c.Iss)
		}
	}

	// Audience: RFC 7519 permits a string or an array.
	if p.RequiredAudience != "" {
		if !audienceContains(c.Aud, p.RequiredAudience) {
			return fmt.Errorf("audience does not contain %q", p.RequiredAudience)
		}
	}

	// Subject must be a non-empty UUID. The subject becomes X-User-Id, and
	// every downstream authorization check keys off it.
	subject := c.Sub
	if subject == "" {
		subject = c.UserID
	}
	if !IsUUID(subject) {
		return errors.New("token subject is not a valid UUID")
	}

	// Session id: required in production so revocation can be reasoned about.
	if p.Production && strings.TrimSpace(c.Sid) == "" {
		return errors.New("token has no sid claim; session revocation cannot be enforced")
	}
	return nil
}

func audienceContains(aud any, want string) bool {
	switch v := aud.(type) {
	case string:
		return v == want
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s == want {
				return true
			}
		}
	case []string:
		for _, s := range v {
			if s == want {
				return true
			}
		}
	}
	return false
}

// IsUUID checks the canonical 8-4-4-4-12 hex form. Deliberately strict:
// accepting a loose subject would let a malformed identity reach services
// that treat X-User-Id as authoritative.
func IsUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}
