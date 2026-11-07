package http

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

const prevTestRSAKID = "rsa-0"

var prevTestKey = func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
}()

func signRS256With(t *testing.T, key *rsa.PrivateKey, kid string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, validProdClaims())
	tok.Header["kid"] = kid
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func rotatingKeys() JWTKeySet {
	k := prodKeys()
	k.RSAPreviousPublic = &prevTestKey.PublicKey
	k.RSAPreviousKID = prevTestRSAKID
	return k
}

// auth-service verified against one RSA key, so during a rotation whichever
// side switched first broke the other: tokens signed with the outgoing key
// answered 401 here while the gateway still accepted them.
func TestRS256RotationAcceptsThePreviousKeyByItsKid(t *testing.T) {
	keys := rotatingKeys()
	if code := callProtected(t, keys, signRS256With(t, prevTestKey, prevTestRSAKID)); code != http.StatusOK {
		t.Fatalf("token signed with the previous key: %d, want 200", code)
	}
	if code := callProtected(t, keys, signRS256With(t, prodTestKey, prodTestRSAKID)); code != http.StatusOK {
		t.Fatalf("token signed with the current key: %d, want 200", code)
	}
}

// Keys are selected by kid only: the previous key under the current kid, or
// the current key under the previous kid, is a bad signature.
func TestRS256RotationNeverCrossesKids(t *testing.T) {
	keys := rotatingKeys()
	cases := map[string]string{
		"previous key, current kid": signRS256With(t, prevTestKey, prodTestRSAKID),
		"current key, previous kid": signRS256With(t, prodTestKey, prevTestRSAKID),
		"previous key, unknown kid": signRS256With(t, prevTestKey, "rsa-9"),
	}
	for name, tok := range cases {
		if code := callProtected(t, keys, tok); code != http.StatusUnauthorized {
			t.Fatalf("%s: %d, want 401", name, code)
		}
	}
	// With no rotation configured, the previous kid is unknown.
	if code := callProtected(t, prodKeys(), signRS256With(t, prevTestKey, prevTestRSAKID)); code != http.StatusUnauthorized {
		t.Fatalf("previous key accepted with no rotation configured: %d", code)
	}
}

func publicPEM(t *testing.T, pub *rsa.PublicKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// The configuration rules match the gateway's RSAKeysFromEnv.
func TestPreviousRSAPublicKeyConfig(t *testing.T) {
	cur := &prodTestKey.PublicKey
	good := publicPEM(t, &prevTestKey.PublicKey)

	if k, err := PreviousRSAPublicKey("", "", "rsa-1", cur); k != nil || err != nil {
		t.Fatalf("unset: (%v, %v), want no rotation", k, err)
	}
	k, err := PreviousRSAPublicKey(good, prevTestRSAKID, "rsa-1", cur)
	if err != nil || k == nil || k.N.Cmp(prevTestKey.PublicKey.N) != 0 {
		t.Fatalf("valid pair: (%v, %v)", k, err)
	}
	escaped := strings.ReplaceAll(good, "\n", `\n`)
	if k, err := PreviousRSAPublicKey(escaped, prevTestRSAKID, "rsa-1", cur); err != nil || k == nil {
		t.Fatalf("escaped newlines: (%v, %v)", k, err)
	}
	pkcs1 := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&prevTestKey.PublicKey)}))
	if k, err := PreviousRSAPublicKey(pkcs1, prevTestRSAKID, "rsa-1", cur); err != nil || k == nil {
		t.Fatalf("PKCS1 key: (%v, %v)", k, err)
	}
	refused := map[string]func() error{
		"key without kid":      func() error { _, err := PreviousRSAPublicKey(good, "", "rsa-1", cur); return err },
		"kid without key":      func() error { _, err := PreviousRSAPublicKey("", prevTestRSAKID, "rsa-1", cur); return err },
		"kid equal to current": func() error { _, err := PreviousRSAPublicKey(good, "rsa-1", "rsa-1", cur); return err },
		"no current RS256 key": func() error { _, err := PreviousRSAPublicKey(good, prevTestRSAKID, "rsa-1", nil); return err },
		"not a key":            func() error { _, err := PreviousRSAPublicKey("garbage", prevTestRSAKID, "rsa-1", cur); return err },
	}
	for name, f := range refused {
		if f() == nil {
			t.Fatalf("%s: accepted, want a boot error", name)
		}
	}
}
