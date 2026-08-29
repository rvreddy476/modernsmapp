package tokenpolicy

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Policy is the set of claim requirements the edge and service token verifier enforces.
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
	// Authorization header or the cookie.
	QueryTokenPaths []string
}

// ProductionEnvVars are the variables consulted, in order, to decide whether
// this process is running in production.
var ProductionEnvVars = []string{"APP_ENV", "ENVIRONMENT", "ENV"}

// IsProductionEnv reports whether this process should apply production rules.
func IsProductionEnv() bool {
	for _, key := range ProductionEnvVars {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
		case "production", "prod":
			return true
		case "":
			continue
		default:
			return false
		}
	}
	return false
}

// LoadFromEnv builds the policy and FAILS CLOSED in production.
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

	// HS256: allowed only outside production, and only when explicitly not disabled.
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
			for _, path := range p.QueryTokenPaths {
				if path == "/" || path == "" {
					return p, errors.New("GATEWAY_QUERY_TOKEN_PATHS must not contain a catch-all prefix")
				}
			}
		}
	}
	return p, nil
}

// QueryTokenAllowed reports whether a token may be read from the query string
// for this request path.
func (p Policy) QueryTokenAllowed(path string) bool {
	for _, allowed := range p.QueryTokenPaths {
		if path == allowed {
			return true
		}
		prefix := strings.TrimSuffix(allowed, "/")
		if strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// Claims is the claim subset the edge and service policy inspects.
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

// ValidateClaims enforces the policy. Every failure is a hard reject.
func ValidateClaims(c Claims, alg string, p Policy, now time.Time) error {
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

	// exp is MANDATORY.
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

	// Token type must be an access token.
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

	// Subject must be a non-empty UUID.
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

// IsUUID checks the canonical 8-4-4-4-12 hex form.
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
