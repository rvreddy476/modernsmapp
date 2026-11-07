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

// Key rotation: chat accepts the current RS256 key and the previous one, each
// only under its own kid (the gateway's JWT_PUBLIC_KEY_PEM_PREVIOUS /
// JWT_RS256_KID_PREVIOUS pair), and production still refuses a symmetric
// secret beside them.
func TestLoadFromEnvAcceptsPreviousRSAKeyForRotation(t *testing.T) {
	current, currentPEM := newRSAKeyPEM(t)
	previous, previousPEM := newRSAKeyPEM(t)
	foreign, _ := newRSAKeyPEM(t)

	for _, key := range []string{"ENVIRONMENT", "ENV", "JWT_SECRET", "JWT_SECRET_PREVIOUS", "JWT_KID", "JWT_KID_PREVIOUS"} {
		t.Setenv(key, "")
	}
	t.Setenv("APP_ENV", "production")
	t.Setenv("JWT_ISSUER", "auth-service")
	t.Setenv("JWT_AUDIENCE", "atpost-api")
	t.Setenv(EnvRSAPublicKey, currentPEM)
	t.Setenv(EnvRSAKID, "rsa-2")
	t.Setenv(EnvRSAPublicKeyPrevious, previousPEM)
	t.Setenv(EnvRSAKIDPrevious, "rsa-1")

	keys, policy, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("rotation config refused: %v", err)
	}
	now := time.Unix(1_800_000_000, 0)
	claims := map[string]any{
		"sub": "11111111-1111-4111-8111-111111111111",
		"exp": now.Add(time.Hour).Unix(), "nbf": now.Unix(),
		"iss": "auth-service", "aud": "atpost-api", "sid": "session-1", "typ": "access",
	}
	if _, err := Verify(signRS256KID(t, current, "rsa-2", claims), keys, policy, now); err != nil {
		t.Fatalf("token signed by the current key rejected: %v", err)
	}
	if _, err := Verify(signRS256KID(t, previous, "rsa-1", claims), keys, policy, now); err != nil {
		t.Fatalf("outstanding token signed by the previous key rejected: %v", err)
	}
	refused := map[string]string{
		"previous key under the current kid": signRS256KID(t, previous, "rsa-2", claims),
		"foreign key under the previous kid": signRS256KID(t, foreign, "rsa-1", claims),
		"unknown kid":                        signRS256KID(t, current, "rsa-9", claims),
		"no kid with two keys":               signRS256KID(t, current, "", claims),
	}
	for name, token := range refused {
		if _, err := Verify(token, keys, policy, now); err == nil {
			t.Errorf("%s: token accepted", name)
		}
	}

	t.Setenv("JWT_SECRET", "a-symmetric-secret")
	if _, _, err := LoadFromEnv(); err == nil {
		t.Fatal("production boot accepted JWT_SECRET beside the rotation keys")
	}
}

func TestRSAKeysFromEnvRefusesAmbiguousRotation(t *testing.T) {
	_, currentPEM := newRSAKeyPEM(t)
	_, previousPEM := newRSAKeyPEM(t)
	cases := map[string]map[string]string{
		"previous key without its kid": {EnvRSAPublicKey: currentPEM, EnvRSAPublicKeyPrevious: previousPEM},
		"previous kid without its key": {EnvRSAPublicKey: currentPEM, EnvRSAKIDPrevious: "rsa-0"},
		"previous kid equals current":  {EnvRSAPublicKey: currentPEM, EnvRSAKID: "rsa-1", EnvRSAPublicKeyPrevious: previousPEM, EnvRSAKIDPrevious: "rsa-1"},
		"previous with no current key": {EnvRSAPublicKeyPrevious: previousPEM, EnvRSAKIDPrevious: "rsa-0"},
		"unparseable previous key":     {EnvRSAPublicKey: currentPEM, EnvRSAPublicKeyPrevious: "not a pem", EnvRSAKIDPrevious: "rsa-0"},
	}
	for name, env := range cases {
		if _, err := RSAKeysFromEnv(func(k string) string { return env[k] }); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	keys, err := RSAKeysFromEnv(func(k string) string { return map[string]string{EnvRSAPublicKey: currentPEM}[k] })
	if err != nil || len(keys) != 1 || keys[DefaultRSAKID] == nil {
		t.Fatalf("single key: keys=%v err=%v, want only %q", keys, err, DefaultRSAKID)
	}
}

func newRSAKeyPEM(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
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
	return signRS256KID(t, key, "rsa-1", claims)
}

// signRS256KID signs with kid in the header; an empty kid leaves it out.
func signRS256KID(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	head := map[string]any{"alg": "RS256", "typ": "JWT"}
	if kid != "" {
		head["kid"] = kid
	}
	header, _ := json.Marshal(head)
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
