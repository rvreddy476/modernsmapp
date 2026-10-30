package http

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/pkg/accesstoken"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	prodTestIssuer   = "https://auth.example.test"
	prodTestAudience = "atpost-api"
	prodTestRSAKID   = "rsa-1"
	prodTestSecret   = "shared-platform-secret"
)

var prodTestKey = func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
}()

func prodKeys() JWTKeySet {
	return JWTKeySet{
		ActiveKID: "v1", ActiveSecret: prodTestSecret,
		RSAPublic: &prodTestKey.PublicKey, RSAKID: prodTestRSAKID,
		Production: true, Issuer: prodTestIssuer, Audience: prodTestAudience,
	}
}

func callProtected(t *testing.T, keys JWTKeySet, token string) int {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/p", authMiddleware(keys, nil), func(c *gin.Context) { c.Status(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/p", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// validProdClaims is the claim set the real minter produces.
func validProdClaims() jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"sub": uuid.NewString(), "sid": uuid.NewString(), "typ": accesstoken.AccessTokenType,
		"iss": prodTestIssuer, "aud": prodTestAudience,
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(15 * time.Minute).Unix(),
	}
}

func signRS256(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = prodTestRSAKID
	s, err := tok.SignedString(prodTestKey)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func signHS256(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok.Header["kid"] = "v1"
	s, err := tok.SignedString([]byte(prodTestSecret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The real mint path's token is accepted in production.
func TestProdMiddlewareAcceptsRealMintedToken(t *testing.T) {
	cfg := accesstoken.Config{Issuer: prodTestIssuer, Audience: prodTestAudience, TTL: 15 * time.Minute, RS256KID: prodTestRSAKID}
	tok, err := accesstoken.Mint(cfg, prodTestKey, uuid.New(), uuid.New(), "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if code := callProtected(t, prodKeys(), tok); code != http.StatusOK {
		t.Fatalf("real minted RS256 token refused in production: %d", code)
	}
	if code := callProtected(t, prodKeys(), signRS256(t, validProdClaims())); code != http.StatusOK {
		t.Fatalf("valid RS256 claim set refused: %d", code)
	}
}

// Was scratchpad probe TestProbeHS256AcceptedWithRS256AndNoAudIss: an HS256
// token made with the platform-shared JWT_SECRET, carrying admin_mfa=true and
// a fresh step_up_at, was accepted with RS256 configured.
func TestProdMiddlewareRefusesHS256(t *testing.T) {
	forged := validProdClaims()
	forged["admin_mfa"] = true
	forged["step_up_at"] = time.Now().Unix()
	if code := callProtected(t, prodKeys(), signHS256(t, forged)); code != http.StatusUnauthorized {
		t.Fatalf("HS256 token accepted in production: %d", code)
	}
}

func TestProdMiddlewareClaimRules(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(jwt.MapClaims)
	}{
		{"foreign issuer", func(c jwt.MapClaims) { c["iss"] = "attacker" }},
		{"missing issuer", func(c jwt.MapClaims) { delete(c, "iss") }},
		{"foreign audience", func(c jwt.MapClaims) { c["aud"] = "staging-api" }},
		{"missing audience", func(c jwt.MapClaims) { delete(c, "aud") }},
		{"missing typ", func(c jwt.MapClaims) { delete(c, "typ") }},
		{"refresh typ", func(c jwt.MapClaims) { c["typ"] = "refresh" }},
		{"missing sid", func(c jwt.MapClaims) { delete(c, "sid") }},
		{"non-uuid sid", func(c jwt.MapClaims) { c["sid"] = "x" }},
		{"non-uuid sub", func(c jwt.MapClaims) { c["sub"] = "admin" }},
		{"missing exp", func(c jwt.MapClaims) { delete(c, "exp") }},
		{"expired", func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-5 * time.Minute).Unix() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validProdClaims()
			tc.mutate(c)
			if code := callProtected(t, prodKeys(), signRS256(t, c)); code != http.StatusUnauthorized {
				t.Fatalf("RS256 token with %s accepted in production: %d", tc.name, code)
			}
		})
	}
}

// A production key set missing its issuer or audience refuses everything
// instead of skipping the check.
func TestProdMiddlewareMisconfiguredFailsClosed(t *testing.T) {
	for _, mut := range []func(*JWTKeySet){
		func(k *JWTKeySet) { k.Issuer = "" },
		func(k *JWTKeySet) { k.Audience = "" },
	} {
		k := prodKeys()
		mut(&k)
		c := validProdClaims()
		c["iss"], c["aud"] = k.Issuer, k.Audience
		if code := callProtected(t, k, signRS256(t, c)); code != http.StatusUnauthorized {
			t.Fatalf("misconfigured production verifier accepted a token: %d", code)
		}
	}
}

// Outside production the HS256 path local tooling relies on still works.
func TestNonProdMiddlewareStillAcceptsHS256(t *testing.T) {
	k := prodKeys()
	k.Production = false
	if code := callProtected(t, k, signHS256(t, validProdClaims())); code != http.StatusOK {
		t.Fatalf("non-production HS256 refused: %d", code)
	}
}
