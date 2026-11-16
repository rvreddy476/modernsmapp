package events

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/atpost/feed-service/internal/service"
	"github.com/atpost/feed-service/internal/store/scylla"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/google/uuid"
)

// feed-08: a follow backfill that could not write a row fails the event.
// handleUserFollowed used to log the row and return nil, so the event was
// committed and that post never reached the new follower. The backfill
// itself (worker pool, listable posts only) is service.BackfillHomeOnFollow
// and is tested there; this pins what the handler does with its answer.

func followEvent(t *testing.T, follower, followee uuid.UUID) *events.EventEnvelope {
	t.Helper()
	msg := rawEvent(t, events.UserFollowed, map[string]any{"follower_id": follower.String(), "followee_id": followee.String()})
	var envelope events.EventEnvelope
	if err := json.Unmarshal(msg.Value, &envelope); err != nil {
		t.Fatal(err)
	}
	return &envelope
}

func TestUserFollowed_AFailedBackfillRowFailsTheEventAndIsRetried(t *testing.T) {
	follower, followee := uuid.New(), uuid.New()
	rowFailed := errors.New("1 timeline write(s) failed")
	var gotFollower, gotFollowee uuid.UUID
	c := &Consumer{
		timelineStore: &scylla.TimelineStore{},
		backfillHome: func(_ context.Context, a, b uuid.UUID) (service.FollowBackfillResult, error) {
			gotFollower, gotFollowee = a, b
			return service.FollowBackfillResult{Found: 50}, rowFailed
		},
	}
	err := c.handle(context.Background(), followEvent(t, follower, followee))
	if !errors.Is(err, rowFailed) {
		t.Fatalf("handler returned %v; a failed backfill row was swallowed and the event committed", err)
	}
	if sharedkafka.IsPermanent(err) {
		t.Fatal("a failed write was marked permanent: it would be dead-lettered instead of retried")
	}
	if gotFollower != follower || gotFollowee != followee {
		t.Fatalf("backfill ran for (%s, %s), want (follower, followee)", gotFollower, gotFollowee)
	}
}

func TestUserFollowed_BackfillSuccessCommits(t *testing.T) {
	calls := 0
	c := &Consumer{
		timelineStore: &scylla.TimelineStore{},
		backfillHome: func(context.Context, uuid.UUID, uuid.UUID) (service.FollowBackfillResult, error) {
			calls++
			return service.FollowBackfillResult{Found: 3, Written: 2, Skipped: 1}, nil
		},
	}
	if err := c.handle(context.Background(), followEvent(t, uuid.New(), uuid.New())); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("backfill ran %d times, want once", calls)
	}
}
