package roomauth

import (
	"strings"
	"testing"
	"time"
)

func validClaims(now time.Time) Claims {
	return Claims{
		Version:        1,
		Subject:        "11111111-1111-1111-1111-111111111111",
		ConversationID: "22222222-2222-2222-2222-222222222222",
		Audience:       Audience,
		ExpiresAt:      now.Add(TTL).Unix(),
		Nonce:          "abcd1234",
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	now := time.Now()
	token, err := Sign(validClaims(now), "secret-1")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := Verify(token, "secret-1", now)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("subject lost: %q", claims.Subject)
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	now := time.Now()
	token, _ := Sign(validClaims(now), "secret-1")
	if _, err := Verify(token, "secret-2", now); err == nil {
		t.Fatal("wrong secret must fail verification")
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	now := time.Now()
	claims := validClaims(now)
	claims.ExpiresAt = now.Add(-time.Second).Unix()
	token, _ := Sign(claims, "secret-1")
	if _, err := Verify(token, "secret-1", now); err == nil {
		t.Fatal("expired entitlement must fail verification")
	}
}

func TestVerifyRejectsTamperedPayload(t *testing.T) {
	now := time.Now()
	token, _ := Sign(validClaims(now), "secret-1")
	parts := strings.SplitN(token, ".", 2)
	// Re-sign a DIFFERENT conversation with a different secret, then splice
	// the original signature — both directions must fail.
	other := validClaims(now)
	other.ConversationID = "33333333-3333-3333-3333-333333333333"
	forged, _ := Sign(other, "attacker-secret")
	forgedParts := strings.SplitN(forged, ".", 2)
	if _, err := Verify(forgedParts[0]+"."+parts[1], "secret-1", now); err == nil {
		t.Fatal("spliced payload must fail verification")
	}
}

func TestVerifyRejectsWrongAudienceAndVersion(t *testing.T) {
	now := time.Now()
	claims := validClaims(now)
	claims.Audience = "someone-else"
	token, _ := Sign(claims, "secret-1")
	if _, err := Verify(token, "secret-1", now); err == nil {
		t.Fatal("wrong audience must fail")
	}
	claims = validClaims(now)
	claims.Version = 2
	token, _ = Sign(claims, "secret-1")
	if _, err := Verify(token, "secret-1", now); err == nil {
		t.Fatal("unknown version must fail")
	}
}

func TestVerifyRejectsBlankIdentity(t *testing.T) {
	now := time.Now()
	claims := validClaims(now)
	claims.Subject = ""
	token, _ := Sign(claims, "secret-1")
	if _, err := Verify(token, "secret-1", now); err == nil {
		t.Fatal("blank subject must fail")
	}
}

// P0-4: the deny marker must outlive every token minted before the removal,
// and both services must derive the same key for one (conversation, user).
func TestDenyMarkerContract(t *testing.T) {
	if DenyTTL <= TTL {
		t.Fatal("DenyTTL must exceed the token TTL or a pre-removal token outlives its revocation")
	}
	if DenyKey("c1", "u1") != "chatdeny:c1:u1" {
		t.Fatalf("deny key shape changed: %s", DenyKey("c1", "u1"))
	}
	if DenyKey("c1", "u1") == DenyKey("c1", "u2") || DenyKey("c1", "u1") == DenyKey("c2", "u1") {
		t.Fatal("deny keys must be distinct per conversation and user")
	}
}

// Re-verification P0-4: the generation comparison is the whole design — a
// marker kills every token of an equal-or-older membership generation, a
// rejoined member's newer generation outranks it, an unparsable marker
// denies, and a legacy gen-less token (0) never outranks any marker.
func TestDeniedByMarkerGenerations(t *testing.T) {
	if !DeniedByMarker("1000", 1000) {
		t.Fatal("a token of the sever generation itself must be denied")
	}
	if !DeniedByMarker("1000", 999) {
		t.Fatal("a pre-removal token must be denied")
	}
	if DeniedByMarker("1000", 1001) {
		t.Fatal("a post-rejoin token (newer generation) must be admitted")
	}
	if !DeniedByMarker("garbage", 5000) {
		t.Fatal("an unparsable marker must deny — fail closed")
	}
	if !DeniedByMarker("1000", 0) {
		t.Fatal("a gen-less legacy token must never outrank a marker")
	}
}

// The generation claim must survive the sign/verify roundtrip untouched.
func TestGenClaimRoundtrip(t *testing.T) {
	now := time.Now()
	claims := validClaims(now)
	claims.Gen = 123456789
	token, err := Sign(claims, "secret-1")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Verify(token, "secret-1", now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Gen != claims.Gen {
		t.Fatalf("gen claim lost in transit: %d != %d", got.Gen, claims.Gen)
	}
}
