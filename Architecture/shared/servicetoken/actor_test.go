package servicetoken

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const testActor = "7b1e4c1a-2f3d-4e5f-8a9b-0c1d2e3f4a5b"

func TestActorRoundTrips(t *testing.T) {
	h := newHarness(t)
	tok, err := h.commerce.Mint(AudiencePayments, "commerce", []string{OpIntentCreate}, []string{RefOrder}, time.Minute, WithActor(testActor))
	if err != nil {
		t.Fatal(err)
	}
	v, err := h.v.Verify(tok, OpIntentCreate, RefOrder)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if v.Actor != testActor {
		t.Fatalf("actor = %q, want %q", v.Actor, testActor)
	}
}

// A token minted without an actor carries no "act" member at all, so a
// verifier built before the claim existed sees byte-identical claims.
func TestTokenWithoutActorUnchanged(t *testing.T) {
	h := newHarness(t)
	tok, err := h.commerce.Mint(AudiencePayments, "commerce", []string{OpIntentCreate}, []string{RefOrder}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := base64.RawURLEncoding.DecodeString(strings.Split(tok, ".")[1])
	if strings.Contains(string(body), `"act"`) {
		t.Fatalf("token without an actor must not carry an act member: %s", body)
	}
	v, err := h.v.Verify(tok, OpIntentCreate, RefOrder)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if v.Actor != "" {
		t.Fatalf("actor = %q, want empty", v.Actor)
	}
}

// A token hand-rolled in the pre-actor claim shape (no act field in the JSON)
// still verifies.
func TestLegacyClaimShapeVerifies(t *testing.T) {
	h := newHarness(t)
	now := time.Now()
	legacy := map[string]any{
		"iss": "commerce-service", "sub": "c", "aud": AudiencePayments,
		"exp": now.Add(time.Minute).Unix(), "nbf": now.Add(-time.Second).Unix(), "iat": now.Unix(),
		"jti": "j", "scope": []string{OpIntentCreate}, "ref_types": []string{RefOrder},
	}
	cb, _ := json.Marshal(legacy)
	var c Claims
	_ = json.Unmarshal(cb, &c)
	tok := handRoll(t, h.commerce, c, "c1")
	if _, err := h.v.Verify(tok, OpIntentCreate, RefOrder); err != nil {
		t.Fatalf("legacy-shaped token refused: %v", err)
	}
}

func TestActorTamperingRefused(t *testing.T) {
	h := newHarness(t)
	tok, _ := h.commerce.Mint(AudiencePayments, "c", []string{OpIntentCreate}, []string{RefOrder}, time.Minute, WithActor(testActor))
	parts := strings.Split(tok, ".")
	body, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c Claims
	_ = json.Unmarshal(body, &c)
	for _, forged := range []string{"00000000-0000-0000-0000-000000000001", ""} {
		c.Actor = forged
		nb, _ := json.Marshal(c)
		tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString(nb) + "." + parts[2]
		if _, err := h.v.Verify(tampered, OpIntentCreate, RefOrder); err != ErrBadSignature {
			t.Fatalf("actor %q: got %v, want ErrBadSignature", forged, err)
		}
	}
}
