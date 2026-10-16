package consumers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

var (
	fdNow      = time.Date(2026, 10, 15, 9, 30, 0, 0, time.UTC)
	fdFollower = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	fdCreator  = uuid.MustParse("22222222-2222-4222-8222-222222222222")
)

func fdClock() time.Time { return fdNow }

func fdEnvelope(t *testing.T, eventID, eventType string, occurred time.Time, payload any) events.EventEnvelope {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return events.EventEnvelope{EventID: eventID, EventType: eventType, OccurredAt: occurred, Payload: raw}
}

func boolPtr(b bool) *bool { return &b }

func TestFollowerDeltasFollowCountsForTheFollowee(t *testing.T) {
	at := time.Date(2026, 10, 14, 23, 59, 0, 0, time.UTC)
	env := fdEnvelope(t, "e1", events.UserFollowed, fdNow, events.UserFollowedPayload{
		FollowerID: fdFollower.String(), FolloweeID: fdCreator.String(), CreatedAt: at,
	})
	deltas, relevant, err := followerDeltasFor(env, fdClock)
	if err != nil || !relevant {
		t.Fatalf("relevant=%v err=%v", relevant, err)
	}
	if len(deltas) != 1 || deltas[0].CreatorID != fdCreator || deltas[0].Follows != 1 || deltas[0].Unfollows != 0 {
		t.Fatalf("deltas = %+v, want one follow for the followee", deltas)
	}
	if !deltas[0].Day.Equal(at) {
		t.Fatalf("day = %v, want the payload's created_at %v", deltas[0].Day, at)
	}
}

func TestFollowerDeltasUnfollowHonoursEdgeRemoved(t *testing.T) {
	for _, tc := range []struct {
		name    string
		removed *bool
		want    int64
	}{
		{"edge removed", boolPtr(true), 1},
		{"repeat unfollow, no edge", boolPtr(false), 0},
		{"producer predates the flag", nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := fdEnvelope(t, "e2", events.UserUnfollowed, fdNow, events.UserUnfollowedPayload{
				FollowerID: fdFollower.String(), FolloweeID: fdCreator.String(),
				OccurredAt: fdNow, EdgeRemoved: tc.removed,
			})
			deltas, relevant, err := followerDeltasFor(env, fdClock)
			if err != nil || !relevant {
				t.Fatalf("relevant=%v err=%v", relevant, err)
			}
			var unfollows int64
			for _, d := range deltas {
				if d.CreatorID != fdCreator || d.Follows != 0 {
					t.Fatalf("unexpected delta %+v", d)
				}
				unfollows += d.Unfollows
			}
			if unfollows != tc.want {
				t.Fatalf("unfollows = %d, want %d", unfollows, tc.want)
			}
		})
	}
}

func TestFollowerDeltasBlockCountsSeveredFollows(t *testing.T) {
	blocker, blocked := fdCreator, fdFollower
	for _, tc := range []struct {
		name             string
		forward, reverse bool
		want             map[uuid.UUID]int64
		relevant         bool
	}{
		{"blocked was a follower", false, true, map[uuid.UUID]int64{blocker: 1}, true},
		{"blocker was following", true, false, map[uuid.UUID]int64{blocked: 1}, true},
		{"mutual", true, true, map[uuid.UUID]int64{blocker: 1, blocked: 1}, true},
		{"no follow severed", false, false, map[uuid.UUID]int64{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := fdEnvelope(t, "e3", events.UserBlocked, fdNow, events.UserBlockedPayload{
				BlockerID: blocker.String(), BlockedID: blocked.String(), BlockedAt: fdNow,
				RemovedFollowForward: tc.forward, RemovedFollowReverse: tc.reverse,
			})
			deltas, relevant, err := followerDeltasFor(env, fdClock)
			if err != nil || relevant != tc.relevant {
				t.Fatalf("relevant=%v err=%v", relevant, err)
			}
			got := map[uuid.UUID]int64{}
			for _, d := range deltas {
				if d.Follows != 0 {
					t.Fatalf("a block is never a follow: %+v", d)
				}
				got[d.CreatorID] += d.Unfollows
			}
			if len(got) != len(tc.want) {
				t.Fatalf("deltas = %v, want %v", got, tc.want)
			}
			for id, n := range tc.want {
				if got[id] != n {
					t.Fatalf("deltas = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestFollowerDeltasIgnoresRequestsAndOtherEvents(t *testing.T) {
	for _, eventType := range []string{events.GraphFollowRequested, events.GraphFollowRequestAccepted, events.PostCreated, events.UserUnblocked} {
		env := fdEnvelope(t, "e4", eventType, fdNow, map[string]string{
			"requester_id": fdFollower.String(), "target_id": fdCreator.String(),
		})
		deltas, relevant, err := followerDeltasFor(env, fdClock)
		if err != nil || relevant || len(deltas) != 0 {
			t.Fatalf("%s: relevant=%v deltas=%v err=%v; only the established edge (UserFollowed) counts", eventType, relevant, deltas, err)
		}
	}
}

func TestFollowerDeltasInvalidIDsArePermanent(t *testing.T) {
	env := fdEnvelope(t, "e5", events.UserFollowed, fdNow, events.UserFollowedPayload{FollowerID: "nope", FolloweeID: fdCreator.String()})
	_, _, err := followerDeltasFor(env, fdClock)
	var permanent permanentFollowerError
	if !errors.As(err, &permanent) {
		t.Fatalf("err = %v, want permanent", err)
	}
	env = events.EventEnvelope{EventID: "e6", EventType: events.UserUnfollowed, Payload: json.RawMessage(`{"follower_id":`)}
	if _, _, err := followerDeltasFor(env, fdClock); !errors.As(err, &permanent) {
		t.Fatalf("undecodable payload err = %v, want permanent", err)
	}
}

func TestFollowerDeltasSelfFollowChangesNothing(t *testing.T) {
	env := fdEnvelope(t, "e7", events.UserFollowed, fdNow, events.UserFollowedPayload{FollowerID: fdCreator.String(), FolloweeID: fdCreator.String()})
	deltas, relevant, err := followerDeltasFor(env, fdClock)
	if err != nil || !relevant || len(deltas) != 0 {
		t.Fatalf("relevant=%v deltas=%v err=%v", relevant, deltas, err)
	}
}

func TestFollowerDeltasDayFallsBackToEnvelopeThenNow(t *testing.T) {
	envTime := time.Date(2026, 10, 13, 5, 0, 0, 0, time.FixedZone("IST", 5*3600+1800))
	env := fdEnvelope(t, "e8", events.UserFollowed, envTime, events.UserFollowedPayload{FollowerID: fdFollower.String(), FolloweeID: fdCreator.String()})
	deltas, _, _ := followerDeltasFor(env, fdClock)
	if !deltas[0].Day.Equal(envTime) || deltas[0].Day.Location() != time.UTC {
		t.Fatalf("day = %v, want the envelope time in UTC", deltas[0].Day)
	}
	env = fdEnvelope(t, "e9", events.UserFollowed, time.Time{}, events.UserFollowedPayload{FollowerID: fdFollower.String(), FolloweeID: fdCreator.String()})
	deltas, _, _ = followerDeltasFor(env, fdClock)
	if !deltas[0].Day.Equal(fdNow) {
		t.Fatalf("day = %v, want now", deltas[0].Day)
	}
}

// fakeFollowerStore applies the receipt rule the database enforces, so
// the consumer's idempotency contract is exercised end to end in memory.
type fakeFollowerStore struct {
	receipts map[string]bool
	net      map[uuid.UUID]int64
	failNext error
}

func newFakeFollowerStore() *fakeFollowerStore {
	return &fakeFollowerStore{receipts: map[string]bool{}, net: map[uuid.UUID]int64{}}
}

func (f *fakeFollowerStore) ApplyFollowerEvent(_ context.Context, eventID, _ string, deltas []pgstore.FollowerDelta) (bool, error) {
	if err := f.failNext; err != nil {
		f.failNext = nil
		return false, err
	}
	if f.receipts[eventID] {
		return false, nil
	}
	f.receipts[eventID] = true
	for _, d := range deltas {
		f.net[d.CreatorID] += d.Follows - d.Unfollows
	}
	return true, nil
}

func (f *fakeFollowerStore) PruneFollowerReceipts(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func fdMessage(t *testing.T, env events.EventEnvelope) []byte {
	t.Helper()
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestFollowerDeltaConsumerRedeliveryDoesNotDoubleCount(t *testing.T) {
	store := newFakeFollowerStore()
	c := NewFollowerDeltaConsumer(store)
	c.now = fdClock
	ctx := context.Background()

	follow := func(id string, follower uuid.UUID) []byte {
		return fdMessage(t, fdEnvelope(t, id, events.UserFollowed, fdNow, events.UserFollowedPayload{
			FollowerID: follower.String(), FolloweeID: fdCreator.String(), CreatedAt: fdNow,
		}))
	}
	unfollow := func(id string, follower uuid.UUID, removed bool) []byte {
		return fdMessage(t, fdEnvelope(t, id, events.UserUnfollowed, fdNow, events.UserUnfollowedPayload{
			FollowerID: follower.String(), FolloweeID: fdCreator.String(), OccurredAt: fdNow, EdgeRemoved: &removed,
		}))
	}
	a, b, d := uuid.New(), uuid.New(), uuid.New()
	for _, msg := range [][]byte{
		follow("f-a", a), follow("f-a", a), // redelivered
		follow("f-b", b),
		follow("f-d", d),
		unfollow("u-a", a, true), unfollow("u-a", a, true), // redelivered
		unfollow("u-a-again", a, false), // repeat tap: no edge left
	} {
		if err := c.apply(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	if got := store.net[fdCreator]; got != 2 {
		t.Fatalf("net followers = %d, want 3 follows - 1 unfollow = 2", got)
	}
}

func TestFollowerDeltaConsumerDurableFailureIsRetried(t *testing.T) {
	store := newFakeFollowerStore()
	store.failNext = errors.New("database down")
	c := NewFollowerDeltaConsumer(store)
	msg := fdMessage(t, fdEnvelope(t, "f-1", events.UserFollowed, fdNow, events.UserFollowedPayload{
		FollowerID: fdFollower.String(), FolloweeID: fdCreator.String(), CreatedAt: fdNow,
	}))
	err := c.apply(context.Background(), msg)
	var permanent permanentFollowerError
	if err == nil || errors.As(err, &permanent) {
		t.Fatalf("err = %v, want a retryable error so the offset is held", err)
	}
	if err := c.apply(context.Background(), msg); err != nil || store.net[fdCreator] != 1 {
		t.Fatalf("retry err=%v net=%d", err, store.net[fdCreator])
	}
}

func TestFollowerDeltaConsumerNeedsAnEventID(t *testing.T) {
	c := NewFollowerDeltaConsumer(newFakeFollowerStore())
	msg := fdMessage(t, fdEnvelope(t, "", events.UserFollowed, fdNow, events.UserFollowedPayload{
		FollowerID: fdFollower.String(), FolloweeID: fdCreator.String(),
	}))
	var permanent permanentFollowerError
	if err := c.apply(context.Background(), msg); !errors.As(err, &permanent) {
		t.Fatalf("err = %v, want permanent: without an id a redelivery cannot be recognised", err)
	}
	// An event this consumer does not count needs no id.
	other := fdMessage(t, events.EventEnvelope{EventType: events.PostCreated, Payload: json.RawMessage(`{}`)})
	if err := c.apply(context.Background(), other); err != nil {
		t.Fatal(err)
	}
}
