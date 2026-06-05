package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

// End-to-end parseAndValidateJWTWithKeys exercise — kid/alg/expiry
// contract sibling to the secretFor truth table in jwt_keyset_test.go.

func TestParseAndValidateJWTValidHS256(t *testing.T) {
	keys := JWTKeySet{ActiveKID: "v1", ActiveSecret: "secret"}
	tok := signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"},
		map[string]any{"sub": "user-1", "exp": time.Now().Add(time.Hour).Unix()},
		keys.ActiveSecret)
	got, err := parseAndValidateJWTWithKeys(tok, keys)
	if err != nil {
		t.Fatalf("valid HS256 token rejected: %v", err)
	}
	if got != "user-1" {
		t.Fatalf("userID=%q want user-1", got)
	}
}

func TestParseAndValidateJWTRejectsAlgConfusion(t *testing.T) {
	keys := JWTKeySet{ActiveKID: "v1", ActiveSecret: "secret"}
	tok := signJWT(t, map[string]any{"alg": "none", "kid": "v1"},
		map[string]any{"sub": "user-1", "exp": time.Now().Add(time.Hour).Unix()},
		keys.ActiveSecret)
	if _, err := parseAndValidateJWTWithKeys(tok, keys); err == nil {
		t.Fatal(`token with alg="none" should be rejected`)
	}
}

func TestParseAndValidateJWTRejectsUnknownKid(t *testing.T) {
	keys := JWTKeySet{ActiveKID: "v1", ActiveSecret: "secret"}
	tok := signJWT(t, map[string]any{"alg": "HS256", "kid": "v9"},
		map[string]any{"sub": "user-1", "exp": time.Now().Add(time.Hour).Unix()},
		keys.ActiveSecret)
	if _, err := parseAndValidateJWTWithKeys(tok, keys); err == nil {
		t.Fatal("token with unknown kid should be rejected")
	}
}

func TestParseAndValidateJWTRotationWindow(t *testing.T) {
	keys := JWTKeySet{
		ActiveKID:      "v2",
		ActiveSecret:   "new",
		PreviousKID:    "v1",
		PreviousSecret: "old",
	}
	tok := signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"},
		map[string]any{"sub": "user-1", "exp": time.Now().Add(time.Hour).Unix()},
		"old")
	got, err := parseAndValidateJWTWithKeys(tok, keys)
	if err != nil {
		t.Fatalf("previous-kid token in rotation window rejected: %v", err)
	}
	if got != "user-1" {
		t.Fatalf("userID=%q want user-1", got)
	}
}

func TestParseAndValidateJWTRejectsExpired(t *testing.T) {
	keys := JWTKeySet{ActiveKID: "v1", ActiveSecret: "secret"}
	tok := signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"},
		map[string]any{"sub": "user-1", "exp": time.Now().Add(-time.Minute).Unix()},
		keys.ActiveSecret)
	if _, err := parseAndValidateJWTWithKeys(tok, keys); err == nil {
		t.Fatal("expired token should be rejected")
	}
}

func TestParseAndValidateJWTRejectsBadSignature(t *testing.T) {
	keys := JWTKeySet{ActiveKID: "v1", ActiveSecret: "secret"}
	tok := signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"},
		map[string]any{"sub": "user-1", "exp": time.Now().Add(time.Hour).Unix()},
		"wrong-secret")
	if _, err := parseAndValidateJWTWithKeys(tok, keys); err == nil {
		t.Fatal("token signed with the wrong secret should be rejected")
	}
}

func signJWT(t *testing.T, header, payload map[string]any, secret string) string {
	t.Helper()
	hb, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	pb, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	encH := base64.RawURLEncoding.EncodeToString(hb)
	encP := base64.RawURLEncoding.EncodeToString(pb)
	input := encH + "." + encP
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
