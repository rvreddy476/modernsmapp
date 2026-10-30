package events

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/atpost/feed-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

// Audit H1: moderator takedowns, copyright blocks and tier-gate edits must
// make every viewer's cached hydration row for the post a miss. The
// consumer writes post:stale:<id>; filterDeletedFromCache reads it.

func rawEnvelope(t *testing.T, eventType string, payload any) kafka.Message {
	t.Helper()
	p, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(events.EventEnvelope{EventID: uuid.New().String(), EventType: eventType, Payload: p})
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{Value: raw}
}

func staleConsumer(t *testing.T) (*Consumer, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	return &Consumer{rdb: redis.NewClient(&redis.Options{Addr: mr.Addr()})}, mr
}

func TestVisibilityChanged_ModerationEventWithoutReasonMarksStale(t *testing.T) {
	c, mr := staleConsumer(t)
	post := uuid.New()
	// The shape post-service publishes for a takedown: the existing payload
	// type, visibility unchanged, and no reason field at all.
	msg := rawEnvelope(t, events.EventPostVisibilityChanged, events.PostVisibilityChangedPayload{
		PostID: post.String(), AuthorID: uuid.New().String(),
		OldVisibility: "public", NewVisibility: "public",
	})
	if err := c.processMessage(context.Background(), msg); err != nil {
		t.Fatalf("takedown event: %v", err)
	}
	if !mr.Exists(service.StaleMarkerKey(post)) {
		t.Fatal("a takedown left no stale marker: cached rows keep serving the post for five minutes")
	}
	if ttl := mr.TTL(service.StaleMarkerKey(post)); ttl <= 0 || ttl > service.StaleMarkerTTL {
		t.Fatalf("stale marker TTL = %v, want (0, %v]", ttl, service.StaleMarkerTTL)
	}
}

func TestVisibilityChanged_WithReasonAndRealChangeMarksStale(t *testing.T) {
	c, mr := staleConsumer(t)
	post := uuid.New()
	payload := map[string]any{
		"post_id": post.String(), "author_id": uuid.New().String(),
		"old_visibility": "public", "new_visibility": "unlisted", "reason": "moderation_takedown",
	}
	if err := c.processMessage(context.Background(), rawEnvelope(t, events.EventPostVisibilityChanged, payload)); err != nil {
		t.Fatalf("event with reason: %v", err)
	}
	if !mr.Exists(service.StaleMarkerKey(post)) {
		t.Fatal("a visibility edit left no stale marker: non-owner viewers keep the cached public row")
	}
}

func TestVisibilityChanged_StaleMarkerFailureIsRetried(t *testing.T) {
	c, mr := staleConsumer(t)
	mr.Close() // Redis down: the marker cannot be written
	msg := rawEnvelope(t, events.EventPostVisibilityChanged, events.PostVisibilityChangedPayload{
		PostID: uuid.New().String(), AuthorID: uuid.New().String(),
		OldVisibility: "public", NewVisibility: "public",
	})
	if err := c.processMessage(context.Background(), msg); err == nil {
		t.Fatal("an unwritten stale marker was swallowed; the event must be retried")
	}
}

func TestCopyrightReview_EveryStateMarksStale(t *testing.T) {
	for _, state := range []string{events.CopyrightReviewPending, events.CopyrightReviewBlocked, events.CopyrightReviewCleared} {
		t.Run(state, func(t *testing.T) {
			c, mr := staleConsumer(t)
			post := uuid.New()
			msg := rawEnvelope(t, events.EventPostCopyrightReview, events.PostCopyrightReviewPayload{
				ReviewID: uuid.New().String(), PostID: post.String(), AuthorID: uuid.New().String(), State: state,
			})
			if err := c.processMessage(context.Background(), msg); err != nil {
				t.Fatal(err)
			}
			if !mr.Exists(service.StaleMarkerKey(post)) {
				t.Fatalf("copyright %s left no stale marker", state)
			}
		})
	}
}

func TestCopyrightReview_MalformedPostIDFails(t *testing.T) {
	c, _ := staleConsumer(t)
	msg := rawEnvelope(t, events.EventPostCopyrightReview, events.PostCopyrightReviewPayload{PostID: "nope", State: "blocked"})
	if err := c.processMessage(context.Background(), msg); err == nil {
		t.Fatal("a malformed copyright event was dropped silently")
	}
}

func TestVisibilityUnchanged(t *testing.T) {
	cases := []struct {
		old, new string
		want     bool
	}{
		{"public", "public", true},
		{"public", "", true},
		{"Public", " public ", true},
		{"public", "unlisted", false},
		{"", "followers", false},
	}
	for _, tc := range cases {
		if got := visibilityUnchanged(tc.old, tc.new); got != tc.want {
			t.Errorf("visibilityUnchanged(%q,%q) = %v, want %v", tc.old, tc.new, got, tc.want)
		}
	}
}
