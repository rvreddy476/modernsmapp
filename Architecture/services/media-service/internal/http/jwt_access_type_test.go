package http

import (
	"testing"
	"time"
)

// reels-15 remainder: a refresh token must never authenticate a call. A
// typed refresh token was already refused; a token with NO type was let
// through. auth-service stamps every access token typ "access" and its
// refresh tokens are opaque, so in RS256-only mode an untyped token is
// refused too.
func TestRS256OnlyRefusesAnUntypedToken(t *testing.T) {
	f := newRSAFixture(t)
	for name, env := range map[string]map[string]string{
		"production": productionEnv(f),
		"JWT_DISABLE_HS256 outside production": {
			"ENV": "local", "JWT_DISABLE_HS256": "true", "JWT_PUBLIC_KEY_PEM": f.pem, "JWT_RS256_KID": "rsa-1",
		},
	} {
		keys, err := LoadJWTKeySet(envOf(env))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !keys.RequireAccessType {
			t.Fatalf("%s: RS256-only key set does not require the access type", name)
		}
		untyped := goodClaims()
		delete(untyped, "typ")
		if _, err := verifyJWTClaims(f.sign(t, "rsa-1", untyped), keys); err == nil {
			t.Fatalf("%s: an untyped token was accepted", name)
		}
		for _, typ := range []string{"access", "at+jwt"} {
			c := goodClaims()
			c["typ"] = typ
			if _, err := verifyJWTClaims(f.sign(t, "rsa-1", c), keys); err != nil {
				t.Fatalf("%s: typ %q refused: %v", name, typ, err)
			}
		}
		// The legacy `type` spelling still counts.
		legacy := goodClaims()
		delete(legacy, "typ")
		legacy["type"] = "access"
		if _, err := verifyJWTClaims(f.sign(t, "rsa-1", legacy), keys); err != nil {
			t.Fatalf("%s: type access refused: %v", name, err)
		}
	}
}

// Development with HS256 on keeps accepting an untyped dev token.
func TestHS256DevelopmentKeepsUntypedTokens(t *testing.T) {
	keys, err := LoadJWTKeySet(envOf(map[string]string{"ENV": "local", "JWT_SECRET": "dev-secret"}))
	if err != nil {
		t.Fatal(err)
	}
	if keys.RequireAccessType {
		t.Fatal("HS256 development requires a type")
	}
	tok := signJWT(t, map[string]any{"alg": "HS256"},
		map[string]any{"sub": policySubject, "exp": time.Now().Add(time.Hour).Unix()}, "dev-secret")
	if _, err := verifyJWTClaims(tok, keys); err != nil {
		t.Fatalf("dev token refused: %v", err)
	}
}
