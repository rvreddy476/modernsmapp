package events

import (
	"context"
	"testing"

	"github.com/atpost/feed-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// post-service's contract (2026-10-30): post.visibility_changed carries a
// reason. Only an audience change re-derives the post's timeline rows; a
// moderation, copyright or membership-gate change (old == new) only marks
// the post stale for every viewer. A missing reason falls back to old/new.

func TestVisibilityChanged_ReasonDecidesReFanout(t *testing.T) {
	cases := []struct {
		name, reason, old, new string
		refanout               bool
	}{
		{"audience edit", events.VisibilityChangeAudience, "public", "unlisted", true},
		{"moderation takedown", events.VisibilityChangeModeration, "public", "public", false},
		{"copyright block", events.VisibilityChangeCopyright, "public", "public", false},
		{"membership gate", events.VisibilityChangeMembershipGate, "followers", "followers", false},
		{"unknown future reason", "legal_hold", "public", "public", false},
		{"unknown reason even with a change", "legal_hold", "public", "unlisted", false},
		{"no reason, unchanged", "", "public", "public", false},
		{"no reason, changed (older producer)", "", "public", "followers", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, mr := staleConsumer(t)
			refanned := 0
			c.rederive = func(context.Context, events.PostVisibilityChangedPayload) error {
				refanned++
				return nil
			}
			post := uuid.New()
			msg := rawEnvelope(t, events.EventPostVisibilityChanged, events.PostVisibilityChangedPayload{
				PostID: post.String(), AuthorID: uuid.New().String(),
				OldVisibility: tc.old, NewVisibility: tc.new, Reason: tc.reason,
			})
			if err := c.processMessage(context.Background(), msg); err != nil {
				t.Fatal(err)
			}
			if !mr.Exists(service.StaleMarkerKey(post)) {
				t.Fatal("no stale marker: every viewer's cached row must be re-checked")
			}
			if got := refanned == 1; got != tc.refanout {
				t.Fatalf("re-fan-out ran %d time(s), want refanout=%v", refanned, tc.refanout)
			}
		})
	}
}

func TestRederiveAudience(t *testing.T) {
	if !rederiveAudience("audience", "public", "unlisted") || !rederiveAudience("Audience", "public", "public") {
		t.Fatal("an audience change must re-derive the rows")
	}
	if rederiveAudience("audience", "public", "") {
		t.Fatal("an audience change with no new visibility has nothing to fan out to")
	}
	for _, r := range []string{"moderation", "copyright", "membership_gate", "anything"} {
		if rederiveAudience(r, "public", "unlisted") {
			t.Fatalf("reason %q re-fanned-out", r)
		}
	}
	if rederiveAudience("", "public", "public") || !rederiveAudience("", "public", "followers") {
		t.Fatal("no reason must fall back to old != new")
	}
}
