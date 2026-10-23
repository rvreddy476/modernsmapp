package events

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/atpost/shared/events"
	"github.com/segmentio/kafka-go"
)

// Members-only posts (membership_gate.go). A post gated to a creator tier
// must never be retrievable through search and its text must never be
// stored in posts_v1. Payloads are built as maps so these tests do not
// depend on the shared payload structs carrying tier_required_id.

const testTier = "7d1c2a3e-1111-4222-8333-944455556666"

// gateEligibilityMsg is a public+approved eligibility transition; tier ""
// leaves tier_required_id off the payload entirely (the omitempty shape).
func gateEligibilityMsg(t *testing.T, id, tier string, rev int64) kafka.Message {
	t.Helper()
	p := map[string]any{
		"post_id":       id,
		"author_id":     "author-1",
		"visibility":    "public",
		"review_status": "approved",
		"search_rev":    rev,
		"text":          "members only secret #perk",
		"title":         "Members only secret",
		"content_type":  "post",
		"created_at":    time.Now().UTC().Add(-time.Hour),
		"changed_at":    time.Now().UTC(),
	}
	if tier != "" {
		p["tier_required_id"] = tier
	}
	return msg(t, events.PostSearchEligibilityChanged, p)
}

func gatePostCreatedMsg(t *testing.T, id, tier string, rev int64) kafka.Message {
	t.Helper()
	p := map[string]any{
		"post_id":       id,
		"author_id":     "author-1",
		"text":          "members only secret #perk",
		"title":         "Members only secret",
		"visibility":    "public",
		"review_status": "approved",
		"content_type":  "post",
		"search_rev":    rev,
		"created_at":    time.Now().UTC(),
	}
	if tier != "" {
		p["tier_required_id"] = tier
	}
	return msg(t, events.PostCreated, p)
}

// assertNoGatedContent fails if the stored document (live or tombstone)
// is retrievable or still holds the post's text or title.
func assertNoGatedContent(t *testing.T, f *fakeOS, id, when string) {
	t.Helper()
	if f.indexed(id) {
		t.Fatalf("%s: members-only post is publicly retrievable", when)
	}
	if f.tombstoneLeaksContent(id) {
		t.Fatalf("%s: members-only post's text/hashtags are stored in the index", when)
	}
	if d := f.doc(id); d != nil {
		if v, _ := d["title"].(string); v != "" {
			t.Fatalf("%s: members-only post's title is stored in the index: %q", when, v)
		}
	}
}

func TestPayloadIsMembersOnly_ReadsTheRawField(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		want    bool
	}{
		{"absent", `{"post_id":"p"}`, false},
		{"null", `{"post_id":"p","tier_required_id":null}`, false},
		{"empty", `{"post_id":"p","tier_required_id":""}`, false},
		{"blank", `{"post_id":"p","tier_required_id":"   "}`, false},
		{"uuid", `{"post_id":"p","tier_required_id":"` + testTier + `"}`, true},
		// No producer sends these; an unknown shape fails closed.
		{"number", `{"post_id":"p","tier_required_id":42}`, true},
		{"object", `{"post_id":"p","tier_required_id":{"id":"x"}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := payloadIsMembersOnly(json.RawMessage(tc.payload))
			if err != nil {
				t.Fatalf("payloadIsMembersOnly: %v", err)
			}
			if got != tc.want {
				t.Fatalf("payloadIsMembersOnly(%s) = %v, want %v", tc.payload, got, tc.want)
			}
		})
	}
}

// A post that is otherwise public+approved but gated is never indexed,
// and no document carrying its text is written.
func TestEligibility_MembersOnlyPostIsNotEligible(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f)

	if err := c.processMessage(context.Background(), gateEligibilityMsg(t, "p1", testTier, 1)); err != nil {
		t.Fatal(err)
	}
	assertNoGatedContent(t, f, "p1", "gated eligibility at rev 1")
}

// Setting the gate removes an indexed post; clearing it (a newer event
// with no tier) indexes it again.
func TestEligibility_GateSetRemovesAndGateClearedReindexes(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f)
	ctx := context.Background()

	if err := c.processMessage(ctx, gateEligibilityMsg(t, "p1", "", 1)); err != nil {
		t.Fatal(err)
	}
	if !f.indexed("p1") {
		t.Fatal("precondition: un-gated public+approved post is indexed")
	}

	if err := c.processMessage(ctx, gateEligibilityMsg(t, "p1", testTier, 2)); err != nil {
		t.Fatal(err)
	}
	assertNoGatedContent(t, f, "p1", "gate set at rev 2")
	if !f.tombstoned("p1") || f.rev("p1") != 2 {
		t.Fatalf("gate set must leave a removal marker at rev 2 (tombstoned=%v rev=%d)",
			f.tombstoned("p1"), f.rev("p1"))
	}

	if err := c.processMessage(ctx, gateEligibilityMsg(t, "p1", "", 3)); err != nil {
		t.Fatal(err)
	}
	if !f.indexed("p1") {
		t.Fatal("gate cleared at rev 3: the post must be indexed again")
	}
}

// The revision barrier holds in both directions: an older un-gated event
// cannot resurrect a gated post, and an older gated event cannot remove a
// post whose gate was cleared later.
func TestEligibility_StaleEventsRespectTheGateRevision(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f)
	ctx := context.Background()

	if err := c.processMessage(ctx, gateEligibilityMsg(t, "p1", "", 1)); err != nil {
		t.Fatal(err)
	}
	if err := c.processMessage(ctx, gateEligibilityMsg(t, "p1", testTier, 3)); err != nil {
		t.Fatal(err)
	}
	// A stale un-gated transition (rev 2) redelivered after the gate.
	if err := c.processMessage(ctx, gateEligibilityMsg(t, "p1", "", 2)); err != nil {
		t.Fatal(err)
	}
	assertNoGatedContent(t, f, "p1", "stale un-gated rev 2 after gate at rev 3")

	// Gate cleared at rev 4; then the rev-3 gate is replayed.
	if err := c.processMessage(ctx, gateEligibilityMsg(t, "p1", "", 4)); err != nil {
		t.Fatal(err)
	}
	if err := c.processMessage(ctx, gateEligibilityMsg(t, "p1", testTier, 3)); err != nil {
		t.Fatal(err)
	}
	if !f.indexed("p1") || f.rev("p1") != 4 {
		t.Fatalf("a stale gate (rev 3) must not remove a post un-gated at rev 4 (indexed=%v rev=%d)",
			f.indexed("p1"), f.rev("p1"))
	}
}

// PostCreated for a members-only post is not indexed.
func TestPostCreated_MembersOnlyIsNotIndexed(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f)
	ctx := context.Background()

	if err := c.processMessage(ctx, gatePostCreatedMsg(t, "p1", testTier, 1)); err != nil {
		t.Fatal(err)
	}
	assertNoGatedContent(t, f, "p1", "gated PostCreated")

	// Gate cleared later: indexed normally.
	if err := c.processMessage(ctx, gateEligibilityMsg(t, "p1", "", 2)); err != nil {
		t.Fatal(err)
	}
	if !f.indexed("p1") {
		t.Fatal("gate cleared at rev 2 after a gated creation: the post must be indexed")
	}

	// The gated creation replayed afterwards is stale and changes nothing.
	if err := c.processMessage(ctx, gatePostCreatedMsg(t, "p1", testTier, 1)); err != nil {
		t.Fatal(err)
	}
	if !f.indexed("p1") || f.rev("p1") != 2 {
		t.Fatalf("a replayed gated PostCreated (rev 1) must not undo the rev-2 clear (indexed=%v rev=%d)",
			f.indexed("p1"), f.rev("p1"))
	}
}

// A gated PostCreated at a newer revision removes a document an older
// event left behind, so the text does not stay in the index.
func TestPostCreated_MembersOnlyRemovesAnOlderDocument(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f)
	ctx := context.Background()

	if err := c.processMessage(ctx, gateEligibilityMsg(t, "p1", "", 1)); err != nil {
		t.Fatal(err)
	}
	if !f.indexed("p1") {
		t.Fatal("precondition: indexed at rev 1")
	}
	if err := c.processMessage(ctx, gatePostCreatedMsg(t, "p1", testTier, 2)); err != nil {
		t.Fatal(err)
	}
	assertNoGatedContent(t, f, "p1", "gated PostCreated at rev 2 over an indexed rev 1")
}

// An un-gated PostCreated still indexes (the gate is not over-broad).
func TestPostCreated_UngatedStillIndexes(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f)

	if err := c.processMessage(context.Background(), gatePostCreatedMsg(t, "p1", "", 1)); err != nil {
		t.Fatal(err)
	}
	if !f.indexed("p1") {
		t.Fatal("an un-gated public+approved PostCreated must be indexed")
	}
}
