package http

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

func mkTok(t *testing.T, header, payload map[string]any) string {
	t.Helper()
	hb, _ := json.Marshal(header)
	pb, _ := json.Marshal(payload)
	return base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(pb)
}

func TestMediaVerifyJWT_DualMode(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	pubDER, _ := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	pub, err := ParseRSAPublicKeyPEM(string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})))
	if err != nil {
		t.Fatalf("parse pub: %v", err)
	}
	keys := JWTKeySet{
		ActiveKID:    "v1",
		ActiveSecret: "hs-secret",
		RSAKeys:      map[string]*rsa.PublicKey{"rsa-1": pub},
	}
	exp := time.Now().Add(time.Hour).Unix()

	// RS256 token verifies.
	input := mkTok(t, map[string]any{"alg": "RS256", "kid": "rsa-1"}, map[string]any{"user_id": "u1", "exp": exp})
	h := sha256.Sum256([]byte(input))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, h[:])
	rsTok := input + "." + base64.RawURLEncoding.EncodeToString(sig)
	if uid, err := verifyJWT(rsTok, keys); err != nil || uid != "u1" {
		t.Fatalf("RS256 verify: uid=%q err=%v", uid, err)
	}

	// HS256 token still verifies.
	hin := mkTok(t, map[string]any{"alg": "HS256", "kid": "v1"}, map[string]any{"user_id": "u2", "exp": exp})
	mac := hmac.New(sha256.New, []byte("hs-secret"))
	mac.Write([]byte(hin))
	hsTok := hin + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if uid, err := verifyJWT(hsTok, keys); err != nil || uid != "u2" {
		t.Fatalf("HS256 verify: uid=%q err=%v", uid, err)
	}

	// Foreign-key RS256 token rejected.
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	h2 := sha256.Sum256([]byte(input))
	badSig, _ := rsa.SignPKCS1v15(rand.Reader, other, crypto.SHA256, h2[:])
	if _, err := verifyJWT(input+"."+base64.RawURLEncoding.EncodeToString(badSig), keys); err == nil {
		t.Fatal("foreign-key RS256 token accepted")
	}

	// alg=none rejected.
	if _, err := verifyJWT(mkTok(t, map[string]any{"alg": "none"}, map[string]any{"user_id": "x", "exp": exp})+".", keys); err == nil {
		t.Fatal("alg=none accepted")
	}
}
