package accessauth

import (
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"testing"
	"time"
)

func TestProductionClaimAndSignatureContract(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	base := map[string]any{
		"sub": "11111111-1111-4111-8111-111111111111",
		"exp": now.Add(time.Hour).Unix(), "nbf": now.Unix(),
		"iss": "auth-service", "aud": "atpost-api", "sid": "session-1", "typ": "access",
	}
	keys := KeySet{RSAKeys: map[string]*rsa.PublicKey{"rsa-1": &privateKey.PublicKey}}
	policy := Policy{Production: true, AllowedIssuers: []string{"auth-service"}, RequiredAudience: "atpost-api", ClockSkew: time.Minute}

	valid := signRS256(t, privateKey, base)
	if _, err := Verify(valid, keys, policy, now); err != nil {
		t.Fatalf("valid production access token rejected: %v", err)
	}

	mutations := map[string]func(map[string]any){
		"missing exp":     func(c map[string]any) { delete(c, "exp") },
		"wrong issuer":    func(c map[string]any) { c["iss"] = "other" },
		"wrong audience":  func(c map[string]any) { c["aud"] = "other" },
		"missing sid":     func(c map[string]any) { delete(c, "sid") },
		"refresh token":   func(c map[string]any) { c["typ"] = "refresh" },
		"invalid subject": func(c map[string]any) { c["sub"] = "user-1" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			claims := cloneClaims(base)
			mutate(claims)
			if _, err := Verify(signRS256(t, privateKey, claims), keys, policy, now); err == nil {
				t.Fatal("unsafe token was accepted")
			}
		})
	}
}

// Production verifies with the public key only. A symmetric token is refused
// even when the verifier happens to hold a matching HS256 secret, and an
// RS256 token signed by any key other than the configured one is refused.
func TestProductionRefusesHS256AndForeignRS256(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	foreignKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	claims := map[string]any{
		"sub": "11111111-1111-4111-8111-111111111111",
		"exp": now.Add(time.Hour).Unix(), "nbf": now.Unix(),
		"iss": "auth-service", "aud": "atpost-api", "sid": "session-1", "typ": "access",
	}
	keys := KeySet{
		ActiveKID: "v1", ActiveSecret: "shared-secret-that-must-not-count",
		RSAKeys: map[string]*rsa.PublicKey{"rsa-1": &privateKey.PublicKey},
	}
	policy := Policy{Production: true, AllowedIssuers: []string{"auth-service"}, RequiredAudience: "atpost-api", ClockSkew: time.Minute}

	if _, err := Verify(signRS256(t, privateKey, claims), keys, policy, now); err != nil {
		t.Fatalf("token signed by the configured key rejected: %v", err)
	}
	if _, err := Verify(signHS256(t, keys.ActiveSecret, "v1", claims), keys, policy, now); err == nil {
		t.Fatal("HS256 token accepted in production")
	}
	if _, err := Verify(signRS256(t, foreignKey, claims), keys, policy, now); err == nil {
		t.Fatal("RS256 token signed by a foreign key accepted")
	}
	// Outside production the same HS256 token is fine when the policy says so.
	dev := Policy{AllowHS256: true, ClockSkew: time.Minute}
	if _, err := Verify(signHS256(t, keys.ActiveSecret, "v1", claims), keys, dev, now); err != nil {
		t.Fatalf("HS256 token rejected in development: %v", err)
	}
}

// A production pod must not hold the symmetric secret at all: LoadFromEnv
// refuses to boot with JWT_SECRET set beside the public key.
func TestLoadFromEnvRefusesSymmetricSecretInProduction(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	publicPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))

	for _, key := range []string{"ENVIRONMENT", "ENV", "JWT_SECRET", "JWT_SECRET_PREVIOUS", "JWT_KID", "JWT_KID_PREVIOUS"} {
		t.Setenv(key, "")
	}
	t.Setenv("APP_ENV", "production")
	t.Setenv("JWT_ISSUER", "auth-service")
	t.Setenv("JWT_AUDIENCE", "atpost-api")
	t.Setenv("JWT_PUBLIC_KEY_PEM", publicPEM)

	keys, policy, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("public-key-only production config refused: %v", err)
	}
	if !policy.Production || policy.AllowHS256 || keys.ActiveSecret != "" {
		t.Fatalf("production policy not applied: production=%v allowHS256=%v secretHeld=%v", policy.Production, policy.AllowHS256, keys.ActiveSecret != "")
	}

	t.Setenv("JWT_SECRET", "a-symmetric-secret")
	if _, _, err := LoadFromEnv(); err == nil {
		t.Fatal("production boot accepted with JWT_SECRET set")
	}
	t.Setenv("JWT_SECRET", "")
	t.Setenv("JWT_SECRET_PREVIOUS", "an-old-symmetric-secret")
	if _, _, err := LoadFromEnv(); err == nil {
		t.Fatal("production boot accepted with JWT_SECRET_PREVIOUS set")
	}

	// Development keeps HS256 so the compose stack (auth-service signs with
	// the shared secret there) still verifies.
	t.Setenv("APP_ENV", "development")
	t.Setenv("JWT_SECRET", "local-dev-secret")
	t.Setenv("JWT_SECRET_PREVIOUS", "")
	_, policy, err = LoadFromEnv()
	if err != nil {
		t.Fatalf("development config refused: %v", err)
	}
	if policy.Production || !policy.AllowHS256 {
		t.Fatalf("development policy wrong: production=%v allowHS256=%v", policy.Production, policy.AllowHS256)
	}
}

func signHS256(t *testing.T, secret, kid string, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]any{"alg": "HS256", "kid": kid, "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func signRS256(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "rsa-1", "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func cloneClaims(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
