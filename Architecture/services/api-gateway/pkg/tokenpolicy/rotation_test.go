package tokenpolicy

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

// Audit M2: RS256 key rotation. The gateway accepts the current key and an
// optional previous one, picks by kid, and still refuses an unknown kid.

func rotationKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return priv, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func rotationToken(t *testing.T, kid string, priv *rsa.PrivateKey) string {
	t.Helper()
	c := validClaims()
	header := map[string]any{"alg": "RS256", "typ": "JWT"}
	if kid != "" {
		header["kid"] = kid
	}
	hb, _ := json.Marshal(header)
	pb, _ := json.Marshal(map[string]any{
		"sub": c.Sub, "exp": c.Exp, "iss": c.Iss, "aud": c.Aud, "sid": c.Sid, "typ": c.TokenType,
	})
	input := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(pb)
	h := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, h[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestRSAKeysFromEnv_RotationWindowAcceptsBothKidsOnly(t *testing.T) {
	oldPriv, oldPEM := rotationKey(t)
	newPriv, newPEM := rotationKey(t)
	rsaKeys, err := RSAKeysFromEnv(envMap(map[string]string{
		EnvRSAPublicKey:         newPEM,
		EnvRSAKID:               "rsa-2",
		EnvRSAPublicKeyPrevious: oldPEM,
		EnvRSAKIDPrevious:       "rsa-1",
	}))
	if err != nil {
		t.Fatalf("RSAKeysFromEnv: %v", err)
	}
	keys := KeySet{RSAKeys: rsaKeys}
	now := time.Now()

	if _, err := Verify(rotationToken(t, "rsa-2", newPriv), keys, prodPolicy(), now); err != nil {
		t.Errorf("token from the new key rejected: %v", err)
	}
	if _, err := Verify(rotationToken(t, "rsa-1", oldPriv), keys, prodPolicy(), now); err != nil {
		t.Errorf("token from the previous key rejected during rotation: %v", err)
	}
	// Selection is by kid: a key never verifies under the other's kid.
	if _, err := Verify(rotationToken(t, "rsa-1", newPriv), keys, prodPolicy(), now); err == nil {
		t.Error("new key accepted under the previous kid")
	}
	if _, err := Verify(rotationToken(t, "rsa-2", oldPriv), keys, prodPolicy(), now); err == nil {
		t.Error("previous key accepted under the new kid")
	}
	// Unknown kid stays rejected, and with two keys a missing kid is too.
	if _, err := Verify(rotationToken(t, "rsa-9", newPriv), keys, prodPolicy(), now); err == nil || !strings.Contains(err.Error(), "unknown kid") {
		t.Errorf("unknown kid: err=%v, want unknown kid", err)
	}
	if _, err := Verify(rotationToken(t, "", newPriv), keys, prodPolicy(), now); err == nil {
		t.Error("kid-less token accepted with two keys configured")
	}
}

func TestRSAKeysFromEnv_SingleKeyUnchanged(t *testing.T) {
	priv, pubPEM := rotationKey(t)
	rsaKeys, err := RSAKeysFromEnv(envMap(map[string]string{EnvRSAPublicKey: pubPEM}))
	if err != nil {
		t.Fatal(err)
	}
	if len(rsaKeys) != 1 || rsaKeys[DefaultRSAKID] == nil {
		t.Fatalf("want one key under %q, got %v", DefaultRSAKID, rsaKeys)
	}
	if _, err := Verify(rotationToken(t, DefaultRSAKID, priv), KeySet{RSAKeys: rsaKeys}, prodPolicy(), time.Now()); err != nil {
		t.Fatalf("single key: %v", err)
	}
	none, err := RSAKeysFromEnv(envMap(nil))
	if err != nil || none != nil {
		t.Fatalf("no key configured: keys=%v err=%v, want nil, nil", none, err)
	}
}

func TestRSAKeysFromEnv_RefusesAmbiguousConfig(t *testing.T) {
	_, a := rotationKey(t)
	_, b := rotationKey(t)
	cases := map[string]map[string]string{
		"previous key without kid": {EnvRSAPublicKey: a, EnvRSAKID: "rsa-2", EnvRSAPublicKeyPrevious: b},
		"previous kid without key": {EnvRSAPublicKey: a, EnvRSAKID: "rsa-2", EnvRSAKIDPrevious: "rsa-1"},
		"previous kid equals current": {
			EnvRSAPublicKey: a, EnvRSAKID: "rsa-1", EnvRSAPublicKeyPrevious: b, EnvRSAKIDPrevious: "rsa-1",
		},
		"previous kid equals default current": {
			EnvRSAPublicKey: a, EnvRSAPublicKeyPrevious: b, EnvRSAKIDPrevious: DefaultRSAKID,
		},
		"previous without current": {EnvRSAPublicKeyPrevious: b, EnvRSAKIDPrevious: "rsa-1"},
		"unparseable previous":     {EnvRSAPublicKey: a, EnvRSAKID: "rsa-2", EnvRSAPublicKeyPrevious: "not a pem", EnvRSAKIDPrevious: "rsa-1"},
		"unparseable current":      {EnvRSAPublicKey: "not a pem"},
	}
	for name, env := range cases {
		if keys, err := RSAKeysFromEnv(envMap(env)); err == nil {
			t.Errorf("%s: accepted (%d keys), want an error", name, len(keys))
		}
	}
}
