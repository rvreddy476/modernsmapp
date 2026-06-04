package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

// C7 — kid-based secret resolution underlies the zero-downtime rotation
// story for JWT_SECRET at the api-gateway. Same contract as the identity
// platform package; tested here so the gateway-tier copy can't silently
// drift.
func TestJWTKeySetSecretFor(t *testing.T) {
	full := jwtKeySet{
		activeKID:      "v2",
		activeSecret:   "new",
		previousKID:    "v1",
		previousSecret: "old",
	}
	cases := []struct {
		name    string
		keys    jwtKeySet
		kid     string
		wantOK  bool
		wantSec string
	}{
		{"empty kid falls back to active", full, "", true, "new"},
		{"matching active kid picks active", full, "v2", true, "new"},
		{"matching previous kid picks previous", full, "v1", true, "old"},
		{"unknown kid is rejected", full, "v9", false, ""},
		{"single-key set accepts legacy (no kid)", jwtKeySet{activeSecret: "only"}, "", true, "only"},
		{"single-key set rejects unknown kid", jwtKeySet{activeSecret: "only"}, "v1", false, ""},
		{"empty previous secret disables previous", jwtKeySet{activeKID: "v2", activeSecret: "new", previousKID: "v1"}, "v1", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.keys.secretFor(tc.kid)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v", ok, tc.wantOK)
			}
			if ok && got != tc.wantSec {
				t.Fatalf("secret=%q want %q", got, tc.wantSec)
			}
		})
	}
}

func TestVerifyJWTRequiresHS256Alg(t *testing.T) {
	keys := jwtKeySet{activeKID: "v1", activeSecret: "secret"}
	payload := map[string]any{
		"sub": "user-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	}

	valid := signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"}, payload, keys.activeSecret)
	userID, _, _, err := verifyJWT(valid, keys)
	if err != nil {
		t.Fatalf("valid HS256 token rejected: %v", err)
	}
	if userID != "user-1" {
		t.Fatalf("userID=%q want user-1", userID)
	}

	missingAlg := signJWT(t, map[string]any{"kid": "v1"}, payload, keys.activeSecret)
	if _, _, _, err := verifyJWT(missingAlg, keys); err == nil {
		t.Fatal("token with missing alg should be rejected")
	}
}

func signJWT(t *testing.T, header, payload map[string]any, secret string) string {
	t.Helper()
	headerBytes, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	encHeader := base64.RawURLEncoding.EncodeToString(headerBytes)
	encPayload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	input := encHeader + "." + encPayload
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
