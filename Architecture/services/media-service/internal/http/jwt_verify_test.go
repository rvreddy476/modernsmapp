package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

// End-to-end verifyJWT exercise — pins the kid/alg/expiry contract
// alongside the secretFor truth table that jwt_keyset_test.go covers.
func TestVerifyJWTValidHS256(t *testing.T) {
	keys := JWTKeySet{ActiveKID: "v1", ActiveSecret: "secret"}
	tok := signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"},
		map[string]any{"sub": "user-1", "exp": time.Now().Add(time.Hour).Unix()},
		keys.ActiveSecret)
	userID, err := verifyJWT(tok, keys)
	if err != nil {
		t.Fatalf("valid HS256 token rejected: %v", err)
	}
	if userID != "user-1" {
		t.Fatalf("userID=%q want user-1", userID)
	}
}

func TestVerifyJWTRejectsMissingAlg(t *testing.T) {
	keys := JWTKeySet{ActiveKID: "v1", ActiveSecret: "secret"}
	tok := signJWT(t, map[string]any{"kid": "v1"},
		map[string]any{"sub": "user-1", "exp": time.Now().Add(time.Hour).Unix()},
		keys.ActiveSecret)
	if _, err := verifyJWT(tok, keys); err == nil {
		t.Fatal("token with missing alg should be rejected")
	}
}

func TestVerifyJWTRejectsAlgConfusion(t *testing.T) {
	keys := JWTKeySet{ActiveKID: "v1", ActiveSecret: "secret"}
	// alg = "none" — classic confusion vector. Signature ignored.
	tok := signJWT(t, map[string]any{"alg": "none", "kid": "v1"},
		map[string]any{"sub": "user-1", "exp": time.Now().Add(time.Hour).Unix()},
		keys.ActiveSecret)
	if _, err := verifyJWT(tok, keys); err == nil {
		t.Fatal(`token with alg="none" should be rejected`)
	}
}

func TestVerifyJWTRejectsUnknownKid(t *testing.T) {
	keys := JWTKeySet{ActiveKID: "v1", ActiveSecret: "secret"}
	tok := signJWT(t, map[string]any{"alg": "HS256", "kid": "v9"},
		map[string]any{"sub": "user-1", "exp": time.Now().Add(time.Hour).Unix()},
		keys.ActiveSecret)
	if _, err := verifyJWT(tok, keys); err == nil {
		t.Fatal("token with unknown kid should be rejected")
	}
}

func TestVerifyJWTRotationWindow(t *testing.T) {
	// Token signed by the previous secret + previous kid — the rotation
	// window must accept it.
	keys := JWTKeySet{
		ActiveKID:      "v2",
		ActiveSecret:   "new",
		PreviousKID:    "v1",
		PreviousSecret: "old",
	}
	tok := signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"},
		map[string]any{"sub": "user-1", "exp": time.Now().Add(time.Hour).Unix()},
		"old")
	userID, err := verifyJWT(tok, keys)
	if err != nil {
		t.Fatalf("previous-kid token in rotation window rejected: %v", err)
	}
	if userID != "user-1" {
		t.Fatalf("userID=%q want user-1", userID)
	}
}

func TestVerifyJWTRejectsExpired(t *testing.T) {
	keys := JWTKeySet{ActiveKID: "v1", ActiveSecret: "secret"}
	tok := signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"},
		map[string]any{"sub": "user-1", "exp": time.Now().Add(-time.Minute).Unix()},
		keys.ActiveSecret)
	if _, err := verifyJWT(tok, keys); err == nil {
		t.Fatal("expired token should be rejected")
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
