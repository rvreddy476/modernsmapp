package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Audit M1/M8 (30 Oct 2026): expired idempotency keys are purged and old
// completed delivery intents are compacted, in batches until one is short.

type stateRetentionFake struct {
	ConversationStore
	keyCuts, intentCuts []time.Time
	keyRuns             []int64 // what each purge batch returns
	intentRuns          []int64
}

func (f *stateRetentionFake) PurgeExpiredIdempotencyKeys(_ context.Context, before time.Time, _ int) (int64, error) {
	f.keyCuts = append(f.keyCuts, before)
	n := f.keyRuns[0]
	f.keyRuns = f.keyRuns[1:]
	return n, nil
}

func (f *stateRetentionFake) CompactCompletedDeliveryIntents(_ context.Context, before time.Time, _ int) (int64, error) {
	f.intentCuts = append(f.intentCuts, before)
	n := f.intentRuns[0]
	f.intentRuns = f.intentRuns[1:]
	return n, nil
}

func TestStateRetentionPurgesKeysAndCompactsOldIntents(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	f := &stateRetentionFake{keyRuns: []int64{10, 10, 3}, intentRuns: []int64{4}}
	svc := quietService(f, nil)
	cfg := StateRetention{Batch: 10}.withDefaults()
	svc.runStateRetention(context.Background(), f, cfg, now)
	if len(f.keyCuts) != 3 {
		t.Fatalf("key purge ran %d batches; want to drain until a short batch", len(f.keyCuts))
	}
	if !f.keyCuts[0].Equal(now) {
		t.Fatalf("keys purged before %v, want now", f.keyCuts[0])
	}
	if len(f.intentCuts) != 1 || !f.intentCuts[0].Equal(now.Add(-7*24*time.Hour)) {
		t.Fatalf("intents compacted before %v; want 7 days ago", f.intentCuts)
	}

	// A pass is bounded: a backlog is spread over several intervals.
	f = &stateRetentionFake{keyRuns: []int64{10, 10, 10, 10}, intentRuns: []int64{0}}
	svc.runStateRetention(context.Background(), f, StateRetention{Batch: 10, MaxBatches: 2}.withDefaults(), now)
	if len(f.keyCuts) != 2 {
		t.Fatalf("one pass ran %d batches, want MaxBatches = 2", len(f.keyCuts))
	}
}

type scheduledCapFake struct {
	ConversationStore
	MessengerExtrasStore
}

func (scheduledCapFake) CheckMembership(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return true, nil
}

func (scheduledCapFake) CreateScheduledMessage(context.Context, *postgres.ScheduledMessage) (*postgres.ScheduledMessage, error) {
	return nil, postgres.ErrScheduledMessageLimit
}

// M2: the per-user cap answers as a validation error (400), not a 503.
func TestScheduledMessageCapIsAValidationError(t *testing.T) {
	svc := quietService(scheduledCapFake{}, nil)
	_, err := svc.CreateScheduledMessage(context.Background(), uuid.New(), uuid.New(), "text", "later", nil, time.Now().Add(time.Hour))
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "100") {
		t.Fatalf("message %q does not name the limit", err)
	}
}
