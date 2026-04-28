package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// TestRedactGatedPost asserts that the redaction strips the heavy
// body fields (text, rich_text, media, poll) but leaves the metadata
// the frontend needs to render a "subscribe to view" preview card —
// title, author, content_type, cover, counts, and the
// tier_required_id itself.
func TestRedactGatedPost(t *testing.T) {
	tierID := uuid.New()
	authorID := uuid.New()
	coverID := uuid.New()

	p := &postgres.Post{
		ID:             uuid.New(),
		AuthorID:       authorID,
		Title:          "Behind the scenes",
		Text:           "Long-form members-only content the public must not see",
		ContentType:    "long_video",
		RichText:       json.RawMessage(`{"blocks":[{"type":"p","text":"secret"}]}`),
		Media:          []postgres.PostMedia{{MediaID: uuid.New(), Kind: "video"}},
		Poll:           &postgres.PollData{Question: "secret poll"},
		CoverMediaID:   &coverID,
		TierRequiredID: &tierID,
	}

	RedactGatedPost(p)

	if p.Text != "" {
		t.Errorf("Text should be redacted, got %q", p.Text)
	}
	if p.RichText != nil {
		t.Errorf("RichText should be nil, got %s", string(p.RichText))
	}
	if p.Media != nil {
		t.Errorf("Media should be nil, got %v", p.Media)
	}
	if p.Poll != nil {
		t.Errorf("Poll should be nil, got %v", p.Poll)
	}
	if !p.BodyRedacted {
		t.Errorf("BodyRedacted should be true")
	}
	// Preview metadata must survive redaction.
	if p.Title == "" {
		t.Errorf("Title should survive redaction")
	}
	if p.AuthorID != authorID {
		t.Errorf("AuthorID should survive redaction")
	}
	if p.CoverMediaID == nil || *p.CoverMediaID != coverID {
		t.Errorf("CoverMediaID should survive redaction")
	}
	if p.ContentType != "long_video" {
		t.Errorf("ContentType should survive redaction")
	}
	if p.TierRequiredID == nil || *p.TierRequiredID != tierID {
		t.Errorf("TierRequiredID should survive redaction so frontend can show the gate")
	}
}

// TestBuildEntitlementCacheKey — keys are namespaced by
// (subscriber, creator, requiredTier). NULL required maps to a
// literal "*" segment so it can't collide with a real tier UUID.
// Re-asserting the format because the Phase-2 Kafka invalidator
// relies on the SCAN pattern matching every key under a (sub, cre)
// pair.
func TestBuildEntitlementCacheKey(t *testing.T) {
	sub := uuid.New()
	cre := uuid.New()
	tier := uuid.New()

	withTier := BuildEntitlementCacheKey(sub, cre, &tier)
	withoutTier := BuildEntitlementCacheKey(sub, cre, nil)

	if !strings.HasPrefix(withTier, "ent:") {
		t.Errorf("expected ent: prefix, got %q", withTier)
	}
	wantWith := "ent:" + sub.String() + ":" + cre.String() + ":" + tier.String()
	if withTier != wantWith {
		t.Errorf("with-tier key: got %q, want %q", withTier, wantWith)
	}
	wantWithout := "ent:" + sub.String() + ":" + cre.String() + ":*"
	if withoutTier != wantWithout {
		t.Errorf("no-tier key: got %q, want %q", withoutTier, wantWithout)
	}
	if withTier == withoutTier {
		t.Errorf("nil-tier and tier-specific keys must not collide")
	}
}

// TestEntitlementCacheRoundTrip — every (allowed, reason) pair
// produced by Encode must come back unchanged from Decode. Asserts
// the wire format invariant the cache relies on.
func TestEntitlementCacheRoundTrip(t *testing.T) {
	cases := []struct {
		allowed bool
		reason  string
	}{
		{true, ""},
		{true, "self"},
		{true, "tier_below_required_but_allowed_anyway"}, // future weirdness
		{false, "no_active_subscription"},
		{false, "tier_below_required"},
		{false, "required_tier_not_found"},
		{false, ""},
	}
	for _, tc := range cases {
		v := EncodeEntitlementCacheValue(tc.allowed, tc.reason)
		got, ok := DecodeEntitlementCacheValue(v)
		if !ok {
			t.Errorf("Decode rejected our own Encode output %q", v)
			continue
		}
		if got.Allowed != tc.allowed {
			t.Errorf("allowed mismatch for %q: got %v, want %v", v, got.Allowed, tc.allowed)
		}
		if got.Reason != tc.reason {
			t.Errorf("reason mismatch for %q: got %q, want %q", v, got.Reason, tc.reason)
		}
	}
}

// TestEntitlementCacheRejectsGarbage — a malformed value (Redis
// poisoning, version skew, partial write) must be rejected with
// ok=false so the caller falls through to the live HTTP path. The
// alternative — trusting whatever bytes happened to be there — is
// a security gap we don't want.
func TestEntitlementCacheRejectsGarbage(t *testing.T) {
	garbage := []string{
		"",
		"yes|allowed",
		"true|allowed",
		"2|something",
		"abc",
		"|reason_only",
	}
	for _, g := range garbage {
		if _, ok := DecodeEntitlementCacheValue(g); ok {
			t.Errorf("expected garbage %q to be rejected, was accepted", g)
		}
	}
}
