package events

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Founder rule (2026-10-21): every account gets its channel when it is
// created. UserRegistered on identity.events.v1 drives it.

type fakeChannelMaker struct {
	calls []struct {
		user                  uuid.UUID
		displayName, username string
	}
	err error
}

func (f *fakeChannelMaker) EnsureDefaultChannel(_ context.Context, userID uuid.UUID, displayName, username string) (*postgres.Channel, bool, error) {
	f.calls = append(f.calls, struct {
		user                  uuid.UUID
		displayName, username string
	}{userID, displayName, username})
	if f.err != nil {
		return nil, false, f.err
	}
	return &postgres.Channel{UserID: userID}, true, nil
}

func registeredMessage(t *testing.T, p events.UserRegisteredPayload) kafka.Message {
	t.Helper()
	payload, _ := json.Marshal(p)
	env, _ := json.Marshal(events.EventEnvelope{EventType: events.UserRegistered, Payload: payload})
	return kafka.Message{Value: env}
}

func TestUserRegisteredCreatesTheDefaultChannel(t *testing.T) {
	user := uuid.New()
	maker := &fakeChannelMaker{}
	c := (&Consumer{}).WithDefaultChannels(maker)
	msg := registeredMessage(t, events.UserRegisteredPayload{UserID: user.String(), Username: "raghu.v", FirstName: "Raghu", LastName: "Varan", CreatedAt: time.Now()})

	if !c.handleUntilDurable(context.Background(), msg) {
		t.Fatal("handled=false for a successful registration")
	}
	if len(maker.calls) != 1 {
		t.Fatalf("channel calls = %d, want 1", len(maker.calls))
	}
	got := maker.calls[0]
	if got.user != user || got.displayName != "Raghu Varan" || got.username != "raghu.v" {
		t.Fatalf("called with %+v", got)
	}
}

// A failed create holds the offset rather than leaving the account
// without its channel.
func TestUserRegisteredFailureHoldsTheOffset(t *testing.T) {
	maker := &fakeChannelMaker{err: errors.New("db down")}
	c := (&Consumer{}).WithDefaultChannels(maker)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if c.handleUntilDurable(ctx, registeredMessage(t, events.UserRegisteredPayload{UserID: uuid.NewString()})) {
		t.Fatal("a failed default channel was reported handled; the offset would have advanced past it")
	}
}

// A malformed id can never succeed: skipped, never blocking the partition.
func TestUserRegisteredBadIDIsSkipped(t *testing.T) {
	maker := &fakeChannelMaker{}
	c := (&Consumer{}).WithDefaultChannels(maker)
	if !c.handleUntilDurable(context.Background(), registeredMessage(t, events.UserRegisteredPayload{UserID: "not-a-uuid"})) {
		t.Fatal("a bad id must be skipped, not retried forever")
	}
	if len(maker.calls) != 0 {
		t.Fatal("no channel for an unparseable id")
	}
}

func TestUserRegisteredIgnoredWithoutMaker(t *testing.T) {
	if !(&Consumer{}).handleUntilDurable(context.Background(), registeredMessage(t, events.UserRegisteredPayload{UserID: uuid.NewString()})) {
		t.Fatal("an unwired instance must pass the event by")
	}
}
