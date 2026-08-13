package tokenpolicy

import (
	"strings"
	"testing"
	"time"
)

// Module 3 SR-1 (AC-SR-1) — edge identity integrity.
//
// This suite lives in internal/tokenpolicy rather than cmd/server because
// the root .gitignore contains a bare `server` rule: every NEW file under
// cmd/server/ is invisible to git. The previous location meant a commit
// would have shipped main.go calling symbols whose definitions were never
// tracked, producing a clean-checkout build failure. Codex caught that;
// the package move is the fix, not a force-add.

func prodPolicy() Policy {
	return Policy{
		Production:       true,
		AllowedIssuers:   []string{"auth-service"},
		RequiredAudience: "atpost-api",
		AllowHS256:       false,
		ClockSkew:        60 * time.Second,
	}
}

func validClaims() Claims {
	return Claims{
		Sub:       "3f2504e0-4f89-11d3-9a0c-0305e82c3301",
		Exp:       time.Now().Add(10 * time.Minute).Unix(),
		Iss:       "auth-service",
		Aud:       "atpost-api",
		Sid:       "sess-1",
		TokenType: "access",
	}
}

func TestValidateClaims_AcceptsWellFormedProductionToken(t *testing.T) {
	if err := ValidateClaims(validClaims(), "RS256", prodPolicy(), time.Now()); err != nil {
		t.Fatalf("a fully-formed RS256 token must be accepted: %v", err)
	}
}

// The defect that mattered most: `if claims.Exp != 0` meant a token minted
// without exp never expired.
func TestValidateClaims_RejectsMissingExp(t *testing.T) {
	c := validClaims()
	c.Exp = 0
	err := ValidateClaims(c, "RS256", prodPolicy(), time.Now())
	if err == nil {
		t.Fatal("a token with no exp claim was accepted; it would never expire")
	}
	if !strings.Contains(err.Error(), "exp") {
		t.Fatalf("error should name the missing claim, got %v", err)
	}
}

func TestValidateClaims_RejectsExpiredAndNotYetValid(t *testing.T) {
	expired := validClaims()
	expired.Exp = time.Now().Add(-10 * time.Minute).Unix()
	if err := ValidateClaims(expired, "RS256", prodPolicy(), time.Now()); err == nil {
		t.Fatal("an expired token was accepted")
	}
	future := validClaims()
	future.Nbf = time.Now().Add(10 * time.Minute).Unix()
	if err := ValidateClaims(future, "RS256", prodPolicy(), time.Now()); err == nil {
		t.Fatal("a token whose nbf is in the future was accepted")
	}
}

func TestValidateClaims_RejectsWrongIssuerAndAudience(t *testing.T) {
	cases := map[string]func(*Claims){
		"wrong issuer":     func(c *Claims) { c.Iss = "https://evil.example.com" },
		"missing issuer":   func(c *Claims) { c.Iss = "" },
		"wrong audience":   func(c *Claims) { c.Aud = "some-other-api" },
		"missing audience": func(c *Claims) { c.Aud = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := validClaims()
			mutate(&c)
			if err := ValidateClaims(c, "RS256", prodPolicy(), time.Now()); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}

func TestValidateClaims_AudienceArrayForm(t *testing.T) {
	c := validClaims()
	c.Aud = []any{"other", "atpost-api"}
	if err := ValidateClaims(c, "RS256", prodPolicy(), time.Now()); err != nil {
		t.Fatalf("array-form audience containing the required value must pass: %v", err)
	}
	c.Aud = []any{"other", "another"}
	if err := ValidateClaims(c, "RS256", prodPolicy(), time.Now()); err == nil {
		t.Fatal("array-form audience without the required value must fail")
	}
}

// The subject becomes X-User-Id, which every downstream authorization
// decision keys off.
func TestValidateClaims_RejectsNonUUIDSubject(t *testing.T) {
	for name, sub := range map[string]string{
		"empty":      "",
		"not a uuid": "admin",
		"sql-ish":    "1 OR 1=1",
		"short":      "3f2504e0-4f89-11d3-9a0c",
		"bad chars":  "zzzzzzzz-4f89-11d3-9a0c-0305e82c3301",
		"no hyphens": "3f2504e04f8911d39a0c0305e82c3301",
		"path-ish":   "../../etc/passwd",
		"whitespace": "   ",
	} {
		t.Run(name, func(t *testing.T) {
			c := validClaims()
			c.Sub = sub
			c.UserID = ""
			if err := ValidateClaims(c, "RS256", prodPolicy(), time.Now()); err == nil {
				t.Fatalf("subject %q was accepted as an identity", sub)
			}
		})
	}
}

// AC-SR-1.2: a refresh token must not authenticate an API call.
func TestValidateClaims_RejectsNonAccessTokenType(t *testing.T) {
	for _, typ := range []string{"refresh", "id", "rt+jwt"} {
		c := validClaims()
		c.TokenType = typ
		if err := ValidateClaims(c, "RS256", prodPolicy(), time.Now()); err == nil {
			t.Fatalf("token type %q was accepted as an access token", typ)
		}
	}
}

func TestValidateClaims_ProductionRequiresTypeAndSid(t *testing.T) {
	noType := validClaims()
	noType.TokenType, noType.Type = "", ""
	if err := ValidateClaims(noType, "RS256", prodPolicy(), time.Now()); err == nil {
		t.Fatal("production must require an explicit access-token type")
	}
	noSid := validClaims()
	noSid.Sid = ""
	if err := ValidateClaims(noSid, "RS256", prodPolicy(), time.Now()); err == nil {
		t.Fatal("production must require sid so revocation can be reasoned about")
	}
}

// A verifier holding a shared secret can MINT identities, not just check them.
func TestValidateClaims_RejectsHS256InProduction(t *testing.T) {
	if err := ValidateClaims(validClaims(), "HS256", prodPolicy(), time.Now()); err == nil {
		t.Fatal("HS256 was accepted in production; any holder of the shared secret " +
			"could mint platform identities")
	}
	dev := prodPolicy()
	dev.Production = false
	dev.AllowHS256 = true
	if err := ValidateClaims(validClaims(), "HS256", dev, time.Now()); err != nil {
		t.Fatalf("HS256 must still work in development: %v", err)
	}
}

func TestValidateClaims_RejectsUnknownAlgorithms(t *testing.T) {
	for _, alg := range []string{"none", "None", "HS512", "ES256", ""} {
		if err := ValidateClaims(validClaims(), alg, prodPolicy(), time.Now()); err == nil {
			t.Fatalf("algorithm %q was accepted", alg)
		}
	}
}

// AC-SR-1.5: query tokens rejected on REST paths AND on near-prefix paths.
//
// Raw prefix matching made "/v1/ws" also match "/v1/ws-evil", so allowing
// one realtime path silently opened every sibling route an attacker names.
func TestQueryTokenAllowed_ExactOrSegmentBoundaryOnly(t *testing.T) {
	p := Policy{QueryTokenPaths: []string{"/v1/ws", "/v1/live/stream"}}

	for _, path := range []string{"/v1/ws", "/v1/ws/connect", "/v1/live/stream", "/v1/live/stream/abc"} {
		if !p.QueryTokenAllowed(path) {
			t.Errorf("%s should permit a query token", path)
		}
	}
	for _, path := range []string{
		"/v1/ws-evil", // the near-prefix bypass
		"/v1/wsx",
		"/v1/ws-anything/x",
		"/v1/live/streaming", // near-prefix on the second entry
		"/v1/profiles/me",
		"/v1/graph/follow",
		"/v1/users/me",
		"/",
		"/v1/posts",
	} {
		if p.QueryTokenAllowed(path) {
			t.Errorf("%s must NOT permit a query token", path)
		}
	}

	if (Policy{}).QueryTokenAllowed("/v1/ws") {
		t.Error("with no allowlist configured, no path may carry a query token")
	}
}

// AC-SR-1.3: production refuses unsafe configuration at startup.
func TestLoadFromEnv_ProductionRefusesUnsafeConfig(t *testing.T) {
	cases := map[string]map[string]string{
		"no issuer": {
			"APP_ENV": "production", "JWT_AUDIENCE": "atpost-api", "JWT_PUBLIC_KEY_PEM": "x",
		},
		"no audience": {
			"APP_ENV": "production", "JWT_ISSUER": "auth-service", "JWT_PUBLIC_KEY_PEM": "x",
		},
		"no rsa key": {
			"APP_ENV": "production", "JWT_ISSUER": "auth-service", "JWT_AUDIENCE": "atpost-api",
		},
		"catch-all query token path": {
			"APP_ENV": "production", "JWT_ISSUER": "auth-service", "JWT_AUDIENCE": "atpost-api",
			"JWT_PUBLIC_KEY_PEM": "x", "GATEWAY_QUERY_TOKEN_PATHS": "/",
		},
	}
	for name, envs := range cases {
		t.Run(name, func(t *testing.T) {
			for k, v := range envs {
				t.Setenv(k, v)
			}
			if _, err := LoadFromEnv(); err == nil {
				t.Fatalf("%s must refuse to start", name)
			}
		})
	}
}

func TestLoadFromEnv_ProductionNeverAllowsHS256(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("JWT_ISSUER", "auth-service")
	t.Setenv("JWT_AUDIENCE", "atpost-api")
	t.Setenv("JWT_PUBLIC_KEY_PEM", "x")

	p, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("a fully configured production policy must load: %v", err)
	}
	if p.AllowHS256 {
		t.Fatal("production must never accept HS256")
	}
	if !p.Production {
		t.Fatal("APP_ENV=production must set Production")
	}
}

func TestLoadFromEnv_DevelopmentStaysWorkable(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	p, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("development must not require production key material: %v", err)
	}
	if !p.AllowHS256 {
		t.Fatal("development should still accept HS256 so local work is unaffected")
	}
}

func TestIsUUID(t *testing.T) {
	if !IsUUID("3f2504e0-4f89-11d3-9a0c-0305e82c3301") || !IsUUID("3F2504E0-4F89-11D3-9A0C-0305E82C3301") {
		t.Error("a canonical UUID was rejected")
	}
	for _, bad := range []string{"", "x", strings.Repeat("a", 36), "3f2504e0_4f89_11d3_9a0c_0305e82c3301"} {
		if IsUUID(bad) {
			t.Errorf("%q accepted as a UUID", bad)
		}
	}
}
