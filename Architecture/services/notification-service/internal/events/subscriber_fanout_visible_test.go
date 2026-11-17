package events

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// The "post became visible" trigger (lv-1, lv-17, reels-30): an approved,
// published, recent upload in a notifying audience becomes (or wakes) its
// one fan-out job; anything else is ignored.
func TestEnqueueOnVisible_Gating(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	prev := fanoutNow
	fanoutNow = func() time.Time { return now }
	t.Cleanup(func() { fanoutNow = prev })

	postID, authorID, channelID := uuid.New(), uuid.New(), uuid.New()
	base := func() sharedevents.PostSearchEligibilityChangedPayload {
		return sharedevents.PostSearchEligibilityChangedPayload{
			PostID: postID.String(), AuthorID: authorID.String(),
			ContentType: "long_video", Visibility: "public", ReviewStatus: "approved",
			CreatedAt: now.Add(-2 * time.Hour), SearchRev: 3,
		}
	}
	cases := []struct {
		name    string
		mutate  func(*sharedevents.PostSearchEligibilityChangedPayload)
		channel uuid.UUID
		wantJob bool
	}{
		{"ready flip, public", func(*sharedevents.PostSearchEligibilityChangedPayload) {}, channelID, true},
		{"followers only", func(e *sharedevents.PostSearchEligibilityChangedPayload) { e.Visibility = "followers" }, channelID, true},
		{"flick", func(e *sharedevents.PostSearchEligibilityChangedPayload) { e.ContentType = "flick" }, channelID, true},
		{"still pending", func(e *sharedevents.PostSearchEligibilityChangedPayload) { e.ReviewStatus = "pending" }, channelID, false},
		{"rejected", func(e *sharedevents.PostSearchEligibilityChangedPayload) { e.ReviewStatus = "rejected" }, channelID, false},
		{"restricted", func(e *sharedevents.PostSearchEligibilityChangedPayload) { e.Restricted = true }, channelID, false},
		{"deleted", func(e *sharedevents.PostSearchEligibilityChangedPayload) { e.Deleted = true }, channelID, false},
		{"scheduled", func(e *sharedevents.PostSearchEligibilityChangedPayload) { e.Scheduled = true }, channelID, false},
		{"private", func(e *sharedevents.PostSearchEligibilityChangedPayload) { e.Visibility = "private" }, channelID, false},
		{"unlisted", func(e *sharedevents.PostSearchEligibilityChangedPayload) { e.Visibility = "unlisted" }, channelID, false},
		{"text post", func(e *sharedevents.PostSearchEligibilityChangedPayload) { e.ContentType = "post" }, channelID, false},
		{"old video made public", func(e *sharedevents.PostSearchEligibilityChangedPayload) { e.CreatedAt = now.Add(-7 * 24 * time.Hour) }, channelID, false},
		{"no created_at", func(e *sharedevents.PostSearchEligibilityChangedPayload) { e.CreatedAt = time.Time{} }, channelID, false},
		{"no channel", func(*sharedevents.PostSearchEligibilityChangedPayload) {}, uuid.Nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := base()
			c.mutate(&e)
			f := &fakeFanout{channel: c.channel}
			if err := (&Consumer{fanout: f}).enqueueOnVisible(context.Background(), e); err != nil {
				t.Fatalf("enqueue: %v", err)
			}
			if got := len(f.enqueued) == 1; got != c.wantJob {
				t.Fatalf("job = %v, want %v", got, c.wantJob)
			}
			if c.wantJob {
				j := f.enqueued[0]
				if j.PostID != postID || j.ChannelID != channelID || j.AuthorID != authorID || j.NotifType == "" || j.DeepLink == "" {
					t.Fatalf("job: %+v", j)
				}
			}
		})
	}
}

// Both triggers for one upload land on the SAME job key, which is what lets
// the store's one-row-per-post rule deliver exactly once.
func TestUploadTriggers_ShareOneJobKey(t *testing.T) {
	now := time.Now()
	postID, authorID, channelID := uuid.New(), uuid.New(), uuid.New()
	f := &fakeFanout{channel: channelID}
	c := &Consumer{fanout: f}

	created := sharedevents.PostCreatedPayload{
		PostID: postID.String(), AuthorID: authorID.String(), ContentType: "long_video",
		Visibility: "public", ChannelID: channelID.String(), CreatedAt: now,
	}
	visible := sharedevents.PostSearchEligibilityChangedPayload{
		PostID: postID.String(), AuthorID: authorID.String(), ContentType: "long_video",
		Visibility: "public", ReviewStatus: "approved", CreatedAt: now, SearchRev: 2,
	}
	for _, msg := range []struct {
		typ     string
		payload any
	}{{sharedevents.PostCreated, created}, {sharedevents.PostSearchEligibilityChanged, visible}} {
		raw, _ := json.Marshal(msg.payload)
		env, _ := json.Marshal(sharedevents.EventEnvelope{EventID: uuid.New().String(), EventType: msg.typ, Payload: raw})
		if err := c.processMessage(context.Background(), kafka.Message{Value: env}); err != nil {
			t.Fatalf("%s: %v", msg.typ, err)
		}
	}
	if len(f.enqueued) != 2 {
		t.Fatalf("enqueued %d, want one per trigger", len(f.enqueued))
	}
	a, b := f.enqueued[0], f.enqueued[1]
	if a.PostID != b.PostID || a.NotifType != b.NotifType || a.DeepLink != b.DeepLink {
		t.Fatalf("triggers disagree on the job: %+v vs %+v", a, b)
	}
}

// A channel lookup that fails while handling the trigger must not lose it:
// for a post first published private it is the only trigger there will
// be. The job is enqueued without a channel for the worker to resolve with
// backoff; a clean "no channel" answer still enqueues nothing.
func TestEnqueueOnVisible_ChannelLookupFailureKeepsTrigger(t *testing.T) {
	postID, authorID := uuid.New(), uuid.New()
	e := sharedevents.PostSearchEligibilityChangedPayload{
		PostID: postID.String(), AuthorID: authorID.String(),
		ContentType: "long_video", Visibility: "public", ReviewStatus: "approved",
		CreatedAt: time.Now().Add(-time.Hour), SearchRev: 4,
	}
	f := &fakeFanout{channel: uuid.New(), lookupErr: errors.New("post-service 503")}
	if err := (&Consumer{fanout: f}).enqueueOnVisible(context.Background(), e); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if len(f.enqueued) != 1 {
		t.Fatalf("a failed channel lookup dropped the trigger: %d jobs", len(f.enqueued))
	}
	if j := f.enqueued[0]; j.PostID != postID || j.AuthorID != authorID || j.ChannelID != uuid.Nil || j.NotifType == "" {
		t.Fatalf("job: %+v", j)
	}
}
