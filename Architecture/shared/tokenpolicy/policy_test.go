package tokenpolicy

import (
	"strings"
	"testing"
	"time"
)

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

func TestValidateClaims_RejectsNonUUIDSubject(t *testing.T) {
	c := validClaims()
	c.Sub = "not-a-uuid"
	if err := ValidateClaims(c, "RS256", prodPolicy(), time.Now()); err == nil {
		t.Fatal("non-UUID subject was accepted")
	}
}

func TestValidateClaims_RejectsRefreshTokens(t *testing.T) {
	c := validClaims()
	c.TokenType = "refresh"
	if err := ValidateClaims(c, "RS256", prodPolicy(), time.Now()); err == nil {
		t.Fatal("refresh token was accepted as access token")
	}
}

func TestValidateClaims_RejectsHS256InProduction(t *testing.T) {
	c := validClaims()
	if err := ValidateClaims(c, "HS256", prodPolicy(), time.Now()); err == nil {
		t.Fatal("HS256 was accepted under production policy")
	}
}
