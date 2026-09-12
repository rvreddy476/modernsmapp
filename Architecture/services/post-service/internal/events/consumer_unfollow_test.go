package events

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Tube channel subscriptions (2026-09-12): subscribe is a subset of follow
// from both directions. graph-service's UserUnfollowed must drop the
// subscription to the followee's channel, keyed (owner = followee,
// subscriber = follower).

type fakeRemover struct {
	calls   [][2]uuid.UUID // owner, subscriber
	deleted bool
	err     error
}

func (f *fakeRemover) DeleteSubscriptionByOwner(_ context.Context, owner, subscriber uuid.UUID) (bool, error) {
	f.calls = append(f.calls, [2]uuid.UUID{owner, subscriber})
	return f.deleted, f.err
}

func unfollowMessage(t *testing.T, follower, followee uuid.UUID) kafka.Message {
	t.Helper()
	payload, _ := json.Marshal(events.UserUnfollowedPayload{FollowerID: follower.String(), FolloweeID: followee.String(), OccurredAt: time.Now()})
	env, _ := json.Marshal(events.EventEnvelope{EventType: events.UserUnfollowed, Payload: payload})
	return kafka.Message{Value: env}
}

func TestUserUnfollowedRemovesTheSubscription(t *testing.T) {
	follower, followee := uuid.New(), uuid.New()
	remover := &fakeRemover{deleted: true}
	c := (&Consumer{}).WithSubscriptionStore(remover)

	if !c.handleUntilDurable(context.Background(), unfollowMessage(t, follower, followee)) {
		t.Fatal("handled=false for a successful unfollow")
	}
	if len(remover.calls) != 1 {
		t.Fatalf("store calls = %d, want 1", len(remover.calls))
	}
	// Owner is the FOLLOWEE (whose channel it is), subscriber the FOLLOWER.
	if remover.calls[0] != [2]uuid.UUID{followee, follower} {
		t.Fatalf("called with (owner=%s, subscriber=%s), want (owner=%s, subscriber=%s)",
			remover.calls[0][0], remover.calls[0][1], followee, follower)
	}
}

// A store failure holds the offset (the loop retries until the context
// ends) rather than skipping the event.
func TestUserUnfollowedStoreFailureHoldsTheOffset(t *testing.T) {
	remover := &fakeRemover{err: errors.New("db down")}
	c := (&Consumer{}).WithSubscriptionStore(remover)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if c.handleUntilDurable(ctx, unfollowMessage(t, uuid.New(), uuid.New())) {
		t.Fatal("a failed unfollow was reported handled; the offset would have advanced past it")
	}
	if len(remover.calls) == 0 {
		t.Fatal("store never called")
	}
}

// An instance without the store wired (the identity-topic consumer) leaves
// the event alone rather than failing on a nil store.
func TestUserUnfollowedIgnoredWithoutSubscriptionStore(t *testing.T) {
	c := &Consumer{}
	if !c.handleUntilDurable(context.Background(), unfollowMessage(t, uuid.New(), uuid.New())) {
		t.Fatal("unwired consumer must skip UserUnfollowed cleanly")
	}
}

func TestUserUnfollowedBadIDIsAnError(t *testing.T) {
	c := (&Consumer{}).WithSubscriptionStore(&fakeRemover{})
	if err := c.handleUserUnfollowed(context.Background(), json.RawMessage(`{"follower_id":"nope","followee_id":"x"}`)); err == nil {
		t.Fatal("malformed ids accepted")
	}
}
