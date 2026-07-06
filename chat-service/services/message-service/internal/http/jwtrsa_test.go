package http

import (
	"crypto"
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

// signRS256 mints an RS256 JWT for tests.
func signRS256(t *testing.T, header, payload map[string]any, priv *rsa.PrivateKey) string {
	t.Helper()
	hb, _ := json.Marshal(header)
	pb, _ := json.Marshal(payload)
	input := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(pb)
	h := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, h[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestParseAndValidateJWT_RS256_RoundTripAndIsolation(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	// Round-trip the public key through PEM to exercise the real parse path.
	pubDER, _ := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	pub, err := ParseRSAPublicKeyPEM(pubPEM)
	if err != nil {
		t.Fatalf("parse pub: %v", err)
	}

	keys := JWTKeySet{
		ActiveKID:    "v1",
		ActiveSecret: "hs-secret", // HS256 still configured in parallel
		RSAKeys:      map[string]*rsa.PublicKey{"rsa-1": pub},
	}
	payload := map[string]any{"sub": "u-rsa", "exp": time.Now().Add(time.Hour).Unix()}

	// Valid RS256 token verifies.
	tok := signRS256(t, map[string]any{"alg": "RS256", "kid": "rsa-1"}, payload, priv)
	uid, err := parseAndValidateJWTWithKeys(tok, keys)
	if err != nil || uid != "u-rsa" {
		t.Fatalf("RS256 verify failed: uid=%q err=%v", uid, err)
	}

	// Token signed by a DIFFERENT key must be rejected (no minting by others).
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	bad := signRS256(t, map[string]any{"alg": "RS256", "kid": "rsa-1"}, payload, other)
	if _, err := parseAndValidateJWTWithKeys(bad, keys); err == nil {
		t.Fatal("token signed by foreign key was accepted")
	}

	// HS256 still works alongside RS256 (no forced logout of old tokens).
	hsTok := signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"},
		map[string]any{"sub": "u-hs", "exp": time.Now().Add(time.Hour).Unix()}, "hs-secret")
	if uid, err := parseAndValidateJWTWithKeys(hsTok, keys); err != nil || uid != "u-hs" {
		t.Fatalf("HS256 token rejected after RS256 added: uid=%q err=%v", uid, err)
	}

	// `none`/unknown alg still rejected.
	noneTok := signRS256(t, map[string]any{"alg": "none", "kid": "rsa-1"}, payload, priv)
	if _, err := parseAndValidateJWTWithKeys(noneTok, keys); err == nil {
		t.Fatal("alg=none was accepted")
	}

	// RS256 token with an unknown kid rejected (multi-key set → no fallback).
	multi := JWTKeySet{
		ActiveSecret: "hs-secret",
		RSAKeys:      map[string]*rsa.PublicKey{"rsa-1": pub, "rsa-2": pub},
	}
	unknownKid := signRS256(t, map[string]any{"alg": "RS256", "kid": "rsa-9"}, payload, priv)
	if _, err := parseAndValidateJWTWithKeys(unknownKid, multi); err == nil {
		t.Fatal("RS256 token with unknown kid was accepted")
	}
}

func TestParseAndValidateJWT_RS256_OnlyDeployment(t *testing.T) {
	// Final-hardening mode: JWT_SECRET removed, only the RSA public key
	// remains. RS256 tokens must verify; HS256 tokens must be rejected.
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	keys := JWTKeySet{RSAKeys: map[string]*rsa.PublicKey{"rsa-1": &priv.PublicKey}}
	payload := map[string]any{"sub": "u-rsa", "exp": time.Now().Add(time.Hour).Unix()}

	tok := signRS256(t, map[string]any{"alg": "RS256", "kid": "rsa-1"}, payload, priv)
	uid, err := parseAndValidateJWTWithKeys(tok, keys)
	if err != nil || uid != "u-rsa" {
		t.Fatalf("RS256 verify failed in RSA-only mode: uid=%q err=%v", uid, err)
	}

	hsTok := signJWT(t, map[string]any{"alg": "HS256"}, payload, "any-secret")
	if _, err := parseAndValidateJWTWithKeys(hsTok, keys); err == nil {
		t.Fatal("HS256 token accepted with no HS256 secret configured")
	}
}
