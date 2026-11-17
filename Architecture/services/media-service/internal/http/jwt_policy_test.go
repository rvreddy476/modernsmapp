package http

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// reels-15: production is RS256 only, exp is mandatory, iss and aud are
// checked when configured, and production refuses to boot holding the
// symmetric secret. Mirrors api-gateway pkg/tokenpolicy and
// chat-service/shared/accessauth.

type rsaFixture struct {
	priv *rsa.PrivateKey
	pem  string
}

func newRSAFixture(t *testing.T) rsaFixture {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return rsaFixture{priv: priv, pem: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))}
}

func (f rsaFixture) sign(t *testing.T, kid string, payload map[string]any) string {
	t.Helper()
	input := mkTok(t, map[string]any{"alg": "RS256", "kid": kid}, payload)
	h := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.priv, crypto.SHA256, h[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

const policySubject = "5f0c7d2e-1111-4a4a-9b9b-0123456789ab"

func goodClaims() map[string]any {
	return map[string]any{
		"sub": policySubject, "exp": time.Now().Add(time.Hour).Unix(),
		"iss": "auth-service", "aud": "atpost-api", "typ": "access", "sid": "s1",
	}
}

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func productionEnv(f rsaFixture) map[string]string {
	return map[string]string{
		"ENV": "prod", "DEPLOY_ENV": "production",
		"JWT_PUBLIC_KEY_PEM": f.pem, "JWT_RS256_KID": "rsa-1",
		"JWT_ISSUER": "auth-service", "JWT_AUDIENCE": "atpost-api",
	}
}

func TestProductionKeySetRefusesHS256(t *testing.T) {
	f := newRSAFixture(t)
	keys, err := LoadJWTKeySet(envOf(productionEnv(f)))
	if err != nil {
		t.Fatalf("production config: %v", err)
	}
	if !keys.DisableHS256 {
		t.Fatal("production key set accepts HS256")
	}
	if _, err := verifyJWTClaims(f.sign(t, "rsa-1", goodClaims()), keys); err != nil {
		t.Fatalf("a good RS256 token was refused: %v", err)
	}
	// Even a verifier that somehow still holds the secret refuses HS256.
	keys.ActiveSecret = "leaked-shared-secret"
	hs := signJWT(t, map[string]any{"alg": "HS256"}, goodClaims(), "leaked-shared-secret")
	if _, err := verifyJWTClaims(hs, keys); err == nil {
		t.Fatal("production accepted an HS256 token")
	}
}

func TestTokenWithoutExpIsRefused(t *testing.T) {
	f := newRSAFixture(t)
	keys, err := LoadJWTKeySet(envOf(productionEnv(f)))
	if err != nil {
		t.Fatal(err)
	}
	c := goodClaims()
	delete(c, "exp")
	if _, err := verifyJWTClaims(f.sign(t, "rsa-1", c), keys); err == nil {
		t.Fatal("an RS256 token without exp was accepted")
	}
	// Development HS256 too: no exp means no token.
	dev := JWTKeySet{ActiveKID: "v1", ActiveSecret: "s"}
	if _, err := verifyJWTClaims(signJWT(t, map[string]any{"alg": "HS256"}, map[string]any{"sub": policySubject}, "s"), dev); err == nil {
		t.Fatal("an HS256 token without exp was accepted")
	}
}

func TestIssuerAudienceAndTypeAreChecked(t *testing.T) {
	f := newRSAFixture(t)
	keys, err := LoadJWTKeySet(envOf(productionEnv(f)))
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"foreign aud":      func(c map[string]any) { c["aud"] = "other-api" },
		"no aud":           func(c map[string]any) { delete(c, "aud") },
		"foreign iss":      func(c map[string]any) { c["iss"] = "someone-else" },
		"no iss":           func(c map[string]any) { delete(c, "iss") },
		"refresh token":    func(c map[string]any) { c["typ"] = "refresh" },
		"expired":          func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() },
		"aud array lacks":  func(c map[string]any) { c["aud"] = []string{"a", "b"} },
		"unknown kid only": nil,
	} {
		c := goodClaims()
		kid := "rsa-1"
		if mutate == nil {
			kid = "rsa-9"
		} else {
			mutate(c)
		}
		if _, err := verifyJWTClaims(f.sign(t, kid, c), keys); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	c := goodClaims()
	c["aud"] = []string{"x", "atpost-api"}
	if _, err := verifyJWTClaims(f.sign(t, "rsa-1", c), keys); err != nil {
		t.Errorf("aud array naming the audience was refused: %v", err)
	}
}

// The acceptance from the finding, through the real middleware: with
// production config an HS256 token, an RS256 token with no exp and a
// foreign aud each answer 401 on a user route.
func TestProductionAuthMiddlewareRefusesWeakTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newRSAFixture(t)
	keys, err := LoadJWTKeySet(envOf(productionEnv(f)))
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.POST("/v1/media/init", AuthMiddlewareWithKeys(keys), func(c *gin.Context) { c.String(http.StatusOK, c.GetHeader("X-User-Id")) })
	call := func(tok string) int {
		req := httptest.NewRequest(http.MethodPost, "/v1/media/init", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := call(f.sign(t, "rsa-1", goodClaims())); got != http.StatusOK {
		t.Fatalf("good RS256 token: %d", got)
	}
	noExp := goodClaims()
	delete(noExp, "exp")
	foreign := goodClaims()
	foreign["aud"] = "other-api"
	for name, tok := range map[string]string{
		"HS256":       signJWT(t, map[string]any{"alg": "HS256"}, goodClaims(), "any-secret"),
		"no exp":      f.sign(t, "rsa-1", noExp),
		"foreign aud": f.sign(t, "rsa-1", foreign),
	} {
		if got := call(tok); got != http.StatusUnauthorized {
			t.Errorf("%s: got %d, want 401", name, got)
		}
	}
}

func TestLoadJWTKeySetProductionRefusals(t *testing.T) {
	f := newRSAFixture(t)
	for name, tweak := range map[string]func(map[string]string){
		"no public key":     func(e map[string]string) { delete(e, "JWT_PUBLIC_KEY_PEM") },
		"no issuer":         func(e map[string]string) { delete(e, "JWT_ISSUER") },
		"no audience":       func(e map[string]string) { delete(e, "JWT_AUDIENCE") },
		"holds JWT_SECRET":  func(e map[string]string) { e["JWT_SECRET"] = "x" },
		"holds prev secret": func(e map[string]string) { e["JWT_SECRET_PREVIOUS"] = "x" },
		"APP_ENV production": func(e map[string]string) {
			delete(e, "ENV")
			delete(e, "DEPLOY_ENV")
			e["APP_ENV"] = "production"
			delete(e, "JWT_ISSUER")
		},
		"prev key without kid": func(e map[string]string) { e["JWT_PUBLIC_KEY_PEM_PREVIOUS"] = f.pem },
	} {
		env := productionEnv(f)
		tweak(env)
		if _, err := LoadJWTKeySet(envOf(env)); !errors.Is(err, ErrJWTConfig) {
			t.Errorf("%s: err = %v, want ErrJWTConfig", name, err)
		}
	}
}

func TestLoadJWTKeySetDevelopment(t *testing.T) {
	// Local/dev as today: HS256 with JWT_SECRET, RS256 beside it.
	f := newRSAFixture(t)
	keys, err := LoadJWTKeySet(envOf(map[string]string{"ENV": "local", "JWT_SECRET": "dev-secret", "JWT_PUBLIC_KEY_PEM": f.pem}))
	if err != nil {
		t.Fatalf("dev config refused: %v", err)
	}
	if keys.DisableHS256 || keys.ActiveKID != "v1" || len(keys.RSAKeys) != 1 {
		t.Fatalf("dev key set: hs256 off=%v kid=%q rsa=%d", keys.DisableHS256, keys.ActiveKID, len(keys.RSAKeys))
	}
	hs := signJWT(t, map[string]any{"alg": "HS256"}, map[string]any{"sub": policySubject, "exp": time.Now().Add(time.Hour).Unix()}, "dev-secret")
	if _, err := verifyJWTClaims(hs, keys); err != nil {
		t.Fatalf("dev HS256 token refused: %v", err)
	}
	if _, err := LoadJWTKeySet(envOf(map[string]string{"ENV": "local"})); !errors.Is(err, ErrJWTConfig) {
		t.Fatalf("dev without JWT_SECRET: err = %v, want ErrJWTConfig", err)
	}

	// The switch: RS256 only outside production, no JWT_SECRET needed.
	keys, err = LoadJWTKeySet(envOf(map[string]string{"ENV": "local", "JWT_DISABLE_HS256": "true", "JWT_SECRET": "dev-secret", "JWT_PUBLIC_KEY_PEM": f.pem}))
	if err != nil {
		t.Fatalf("RS256-only dev config refused: %v", err)
	}
	if _, err := verifyJWTClaims(hs, keys); err == nil {
		t.Fatal("JWT_DISABLE_HS256=true still accepted an HS256 token")
	}
	if _, err := LoadJWTKeySet(envOf(map[string]string{"ENV": "local", "JWT_DISABLE_HS256": "true"})); !errors.Is(err, ErrJWTConfig) {
		t.Fatal("RS256-only without a public key was accepted")
	}
}

func TestRSAKeysFromEnvRotation(t *testing.T) {
	cur, prev := newRSAFixture(t), newRSAFixture(t)
	env := productionEnv(cur)
	env["JWT_PUBLIC_KEY_PEM_PREVIOUS"] = prev.pem
	env["JWT_RS256_KID_PREVIOUS"] = "rsa-0"
	keys, err := LoadJWTKeySet(envOf(env))
	if err != nil {
		t.Fatal(err)
	}
	for kid, f := range map[string]rsaFixture{"rsa-1": cur, "rsa-0": prev} {
		if _, err := verifyJWTClaims(f.sign(t, kid, goodClaims()), keys); err != nil {
			t.Errorf("kid %s refused during rotation: %v", kid, err)
		}
	}
	env["JWT_RS256_KID_PREVIOUS"] = "rsa-1"
	if _, err := LoadJWTKeySet(envOf(env)); err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Errorf("same kid for both keys: err = %v", err)
	}
}

func TestEmptySecretIsNoKey(t *testing.T) {
	keys := JWTKeySet{ActiveKID: "v1"}
	tok := signJWT(t, map[string]any{"alg": "HS256"}, map[string]any{"sub": policySubject, "exp": time.Now().Add(time.Hour).Unix()}, "")
	if _, err := verifyJWTClaims(tok, keys); err == nil {
		t.Fatal("a token MACed with an empty secret verified")
	}
}
