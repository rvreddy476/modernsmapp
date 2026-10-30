package postgres

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// post.visibility_changed for every narrowing (visibility_events.go). The
// SQL halves run in CI (visibility_events_integration_test.go); these pin
// the decisions and the wire shape feed-service consumes.

func TestModerationNarrows(t *testing.T) {
	cases := []struct {
		prev, next string
		want       bool
	}{
		{"approved", "rejected", true},
		{"approved", "needs_changes", true},
		{"approved", "approved", false},
		{"flagged", "rejected", false},  // already hidden from every viewer
		{"pending", "rejected", false},  // never visible
		{"rejected", "approved", false}, // a widening
		{"needs_changes", "rejected", false},
	}
	for _, c := range cases {
		if got := moderationNarrows(c.prev, c.next); got != c.want {
			t.Errorf("moderationNarrows(%q, %q) = %v, want %v", c.prev, c.next, got, c.want)
		}
	}
}

func TestRestrictionNarrows(t *testing.T) {
	if !restrictionNarrows(true, RestrictionStateActive) {
		t.Error("a hold placed must emit")
	}
	if restrictionNarrows(false, RestrictionStateActive) {
		t.Error("a replayed / unchanged hold must not emit again")
	}
	if restrictionNarrows(true, RestrictionStateReleased) {
		t.Error("a release widens and owes nothing")
	}
	for src, want := range map[string]string{
		RestrictionSourceCopyright:   VisibilityReasonCopyright,
		RestrictionSourceTitleReview: VisibilityReasonCopyright,
		RestrictionSourceSafety:      VisibilityReasonModeration,
	} {
		if got := restrictionReason(src); got != want {
			t.Errorf("restrictionReason(%q) = %q, want %q", src, got, want)
		}
	}
}

func TestGateNarrows(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	if !gateNarrows(nil, &a) {
		t.Error("a gate added must emit")
	}
	if !gateNarrows(&a, &b) {
		t.Error("a gate changed to another tier must emit")
	}
	if gateNarrows(&a, &a) {
		t.Error("the same gate again changes nothing")
	}
	if gateNarrows(&a, nil) || gateNarrows(nil, nil) {
		t.Error("a cleared or absent gate widens and owes nothing")
	}
}

func TestVisibilityEventOwed(t *testing.T) {
	at := time.Now().Add(time.Hour)
	if !visibilityEventOwed(false, nil) {
		t.Error("a live published post owes the event")
	}
	if visibilityEventOwed(true, nil) {
		t.Error("a deleted post owes nothing (PostDeleted covers it)")
	}
	if visibilityEventOwed(false, &at) {
		t.Error("a scheduled post was never fanned out and owes nothing")
	}
}

// The wire shape: the shared payload's fields, flattened, plus reason. A
// consumer decoding into events.PostVisibilityChangedPayload reads every
// field it knew; reason is the one addition.
func TestPostVisibilityChangedEventWireShape(t *testing.T) {
	post, author := uuid.New(), uuid.New()
	created := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	ev := narrowedVisibilityEvent(post, author, "long_video", "public", created, now, "chan-1", VisibilityReasonCopyright)
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"post_id": post.String(), "author_id": author.String(), "content_type": "long_video",
		"old_visibility": "public", "new_visibility": "public",
		"created_at": "2026-10-01T09:00:00Z", "changed_at": "2026-10-30T12:00:00Z",
		"channel_id": "chan-1", "reason": "copyright",
	}
	if len(m) != len(want) {
		t.Fatalf("payload has %d keys, want %d: %s", len(m), len(want), raw)
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v, want %v", k, m[k], v)
		}
	}
	var shared events.PostVisibilityChangedPayload
	if err := json.Unmarshal(raw, &shared); err != nil || shared.PostID != post.String() || shared.NewVisibility != "public" {
		t.Fatalf("the shared payload type no longer decodes the event: %+v %v", shared, err)
	}
}
