package consumers

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/shared/events"
	"github.com/atpost/shared/postclassify"
	"github.com/google/uuid"
)

// Audit M6: the server-measured duration reaches the ownership projection
// on the two events analytics already consumes. It is what ingest judges
// a view against, so every route in is pinned.

func TestPostCreatedProjectsTheMeasuredDuration(t *testing.T) {
	cases := []struct {
		name    string
		ms, sec int
		want    int64
	}{
		{"duration_ms wins", 61_500, 61, 61_500},
		{"duration_seconds is the fallback", 0, 61, 61_000},
		{"no duration is unknown", 0, 0, 0},
		{"a negative duration is unknown", -5, -1, 0},
		{"beyond twelve hours is unknown", 13 * 60 * 60 * 1000, 13 * 60 * 60, 0},
		{"exactly twelve hours is kept", 12 * 60 * 60 * 1000, 0, 12 * 60 * 60 * 1000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &recordingStore{}
			consumer := NewContentOwnershipConsumer(store)
			value := envelopeFor(t, events.PostCreated, events.PostCreatedPayload{
				PostID: uuid.New().String(), AuthorID: uuid.New().String(),
				ContentType: postclassify.LongVideo, CreatedAt: time.Now().UTC(),
				DurationMs: tc.ms, DurationSeconds: tc.sec,
			})
			if err := consumer.apply(context.Background(), value); err != nil {
				t.Fatalf("apply: %v", err)
			}
			if len(store.upserts) != 1 || store.upserts[0].MediaDurationMS != tc.want {
				t.Fatalf("upserts=%+v want media duration %d", store.upserts, tc.want)
			}
		})
	}
}

// A video published while still processing arrives with no duration on
// PostCreated; the eligibility event post-service emits when transcode
// flips it to approved carries the measured one.
func TestEligibilityEventProjectsTheMeasuredDuration(t *testing.T) {
	post, author := uuid.New(), uuid.New()
	for name, tc := range map[string]struct {
		ms   int
		want int64
	}{
		"measured":         {ms: 7_200_000, want: 7_200_000},
		"absent":           {ms: 0, want: 0},
		"beyond the bound": {ms: 13 * 60 * 60 * 1000, want: 0},
	} {
		t.Run(name, func(t *testing.T) {
			store := &recordingStore{}
			consumer := NewContentOwnershipConsumer(store)
			value := envelopeFor(t, events.PostSearchEligibilityChanged, events.PostSearchEligibilityChangedPayload{
				PostID: post.String(), AuthorID: author.String(),
				Visibility: "public", ReviewStatus: "approved", SearchRev: 2,
				DurationMs: tc.ms, ChangedAt: time.Now().UTC(),
			})
			if err := consumer.apply(context.Background(), value); err != nil {
				t.Fatalf("apply: %v", err)
			}
			if len(store.eligibility) != 1 || store.eligibility[0].MediaDurationMS != tc.want {
				t.Fatalf("eligibility=%+v want media duration %d", store.eligibility, tc.want)
			}
		})
	}
}
