//go:build integration

package consumers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/atpost/analytics-service/internal/service"
	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/analytics-service/internal/testsupport"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// The graph wire shape, through the consumer, into the database, out of
// creator/me's followers_delta. No broker: apply() is what Start runs per
// record, and redelivery is the same bytes applied twice.
func TestFollowerEventsReachCreatorMe(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.Pool(t, "consumers")
	if _, err := pool.Exec(ctx, `TRUNCATE analytics.creator_follower_daily, analytics.follower_event_receipts`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO analytics.follower_tracking (id) VALUES (1) ON CONFLICT (id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	creator, quiet := uuid.New(), uuid.New()
	consumer := NewFollowerDeltaConsumer(pgstore.New(pool))

	message := func(eventType string, payload any) []byte {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		actor := uuid.NewString()
		env := events.NewEnvelope(ctx, eventType, &actor, raw)
		body, err := json.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	removed, notRemoved := true, false
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	followA := message(events.UserFollowed, events.UserFollowedPayload{FollowerID: a.String(), FolloweeID: creator.String(), CreatedAt: now})
	records := [][]byte{
		followA, followA, // redelivered
		message(events.UserFollowed, events.UserFollowedPayload{FollowerID: b.String(), FolloweeID: creator.String(), CreatedAt: now}),
		message(events.UserFollowed, events.UserFollowedPayload{FollowerID: c.String(), FolloweeID: creator.String(), CreatedAt: now.AddDate(0, 0, -20)}),
		message(events.GraphFollowRequested, events.FollowRequestedPayload{RequesterID: uuid.NewString(), TargetID: creator.String(), CreatedAt: now}),
		message(events.UserUnfollowed, events.UserUnfollowedPayload{FollowerID: a.String(), FolloweeID: creator.String(), OccurredAt: now, EdgeRemoved: &removed}),
		message(events.UserUnfollowed, events.UserUnfollowedPayload{FollowerID: a.String(), FolloweeID: creator.String(), OccurredAt: now, EdgeRemoved: &notRemoved}),
		message(events.UserBlocked, events.UserBlockedPayload{BlockerID: creator.String(), BlockedID: b.String(), BlockedAt: now, RemovedFollowReverse: true}),
	}
	for _, r := range records {
		if err := consumer.apply(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	creators := service.NewCreatorService(pgstore.NewAggregateStore(pool)).WithFollowerDeltas(pgstore.NewAggregateStore(pool))
	for period, want := range map[string]int64{
		"7d":  2 - 1 - 1, // a, b followed; a unfollowed; b severed by the block
		"28d": 3 - 1 - 1, // plus c, 20 days ago
		"90d": 3 - 1 - 1, // nothing older
	} {
		stats, err := creators.GetStats(ctx, creator, period)
		if err != nil {
			t.Fatal(err)
		}
		if stats.FollowersDelta == nil || *stats.FollowersDelta != want {
			t.Fatalf("%s followers_delta = %v, want %d", period, stats.FollowersDelta, want)
		}
	}
	stats, err := creators.GetStats(ctx, quiet, "28d")
	if err != nil {
		t.Fatal(err)
	}
	if stats.FollowersDelta == nil || *stats.FollowersDelta != 0 {
		t.Fatalf("quiet creator followers_delta = %v, want 0", stats.FollowersDelta)
	}
}
