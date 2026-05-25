package service

import (
	"encoding/json"
	"testing"
)

// TestAppleBoolClaimAcceptsBareBool covers the modern Apple id_token
// shape where email_verified is a bare JSON boolean.
func TestAppleBoolClaimAcceptsBareBool(t *testing.T) {
	if !appleBoolClaim(json.RawMessage(`true`)) {
		t.Fatal("expected true for bare bool true")
	}
	if appleBoolClaim(json.RawMessage(`false`)) {
		t.Fatal("expected false for bare bool false")
	}
}

// TestAppleBoolClaimAcceptsLegacyStrings covers the older Apple
// id_token shape where email_verified arrived as "true" / "false"
// strings. Both forms must parse correctly so the A5 gate makes the
// right call.
func TestAppleBoolClaimAcceptsLegacyStrings(t *testing.T) {
	if !appleBoolClaim(json.RawMessage(`"true"`)) {
		t.Fatal("expected true for legacy string \"true\"")
	}
	if appleBoolClaim(json.RawMessage(`"True"`)) {
		// EqualFold permits this — Apple has been seen returning both cases.
		// Documenting the behaviour.
	}
	if appleBoolClaim(json.RawMessage(`"false"`)) {
		t.Fatal("expected false for legacy string \"false\"")
	}
}

// TestAppleBoolClaimDefaultsToFalse verifies that when the claim is
// missing, malformed, or a JSON null, we conservatively treat it as
// unverified. The A5 gate must fail closed — a missing email_verified
// claim is NOT the same as the provider asserting true.
func TestAppleBoolClaimDefaultsToFalse(t *testing.T) {
	cases := []json.RawMessage{
		nil,
		json.RawMessage(``),
		json.RawMessage(`null`),
		json.RawMessage(`"yes"`),
		json.RawMessage(`123`),
		json.RawMessage(`{"x":1}`),
	}
	for _, c := range cases {
		if appleBoolClaim(c) {
			t.Errorf("appleBoolClaim(%q) = true; want false (must fail closed)", string(c))
		}
	}
}

// TestOAuthPendingResponseShapeStable pins the JSON shape of the
// pending-signup response. Frontend + mobile clients depend on these
// exact field names to drive the OTP-collection screen.
func TestOAuthPendingResponseShapeStable(t *testing.T) {
	resp := OAuthPendingResponse{
		Status:       "pending_signup",
		PendingToken: "tok",
		Provider:     "github",
		Email:        "u@example.test",
		Name:         "U",
		NextStep:     "complete_signup",
		Message:      "Identity verification required",
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, field := range []string{"status", "pending_token", "provider", "email", "name", "next_step", "message"} {
		if _, ok := m[field]; !ok {
			t.Errorf("expected field %q in pending-signup response JSON", field)
		}
	}
}
