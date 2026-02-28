package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestParseAndValidateJWT_ValidToken(t *testing.T) {
	secret := []byte("test-secret")
	token := buildHS256Token(t, secret, map[string]any{
		"sub": "7d16ea6b-8799-4289-a4dc-fd77fb2d9dd8",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})

	userID, err := parseAndValidateJWT(token, secret)
	if err != nil {
		t.Fatalf("expected token to be valid, got error: %v", err)
	}
	if userID != "7d16ea6b-8799-4289-a4dc-fd77fb2d9dd8" {
		t.Fatalf("unexpected user id: %s", userID)
	}
}

func TestParseAndValidateJWT_InvalidSignature(t *testing.T) {
	secret := []byte("test-secret")
	otherSecret := []byte("other-secret")
	token := buildHS256Token(t, otherSecret, map[string]any{
		"sub": "7d16ea6b-8799-4289-a4dc-fd77fb2d9dd8",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})

	if _, err := parseAndValidateJWT(token, secret); err == nil {
		t.Fatal("expected invalid signature error")
	}
}

func TestParseAndValidateJWT_ExpiredToken(t *testing.T) {
	secret := []byte("test-secret")
	token := buildHS256Token(t, secret, map[string]any{
		"sub": "7d16ea6b-8799-4289-a4dc-fd77fb2d9dd8",
		"exp": time.Now().Add(-1 * time.Minute).Unix(),
	})

	if _, err := parseAndValidateJWT(token, secret); err == nil {
		t.Fatal("expected expired token error")
	}
}

func buildHS256Token(t *testing.T, secret []byte, payload map[string]any) string {
	t.Helper()

	header := map[string]any{
		"alg": "HS256",
		"typ": "JWT",
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	encodedHeader := base64.RawURLEncoding.EncodeToString(headerJSON)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := encodedHeader + "." + encodedPayload

	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(signingInput))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return signingInput + "." + signature
}
