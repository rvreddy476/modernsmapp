package accessauth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
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
