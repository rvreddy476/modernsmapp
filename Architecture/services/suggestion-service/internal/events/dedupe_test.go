package events

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"errors"
	"sync"

	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Module 3 LB-2 / CLB-1 — one logical effect from an at-least-once delivery.
//
// graph-service publishes safety events from a durable outbox: it publishes,
// THEN marks the row published, so a crash between those two steps redelivers.
// That ordering is deliberate — the alternative loses events, and a lost block
// leaves a user reachable by someone they blocked. The cost is that the
// consumer has to recognise a replay, which nothing previously did.
//
// CLB-1 changed WHEN the replay marker is written. It used to be claimed before
// the handler ran, which meant a failed handler marked the event applied and
// suppressed the redelivery that would have repaired it. These tests pin the
// new order: Seen() reads, MarkApplied() writes, and only after the effect.
//
// The authoritative de-duplication for the block effect is the PostgreSQL
// consumer inbox row written inside the effect's transaction; that is proved
// against a live broker and a live database in consumer_live_test.go.

// memoryClaimStore implements the same contract Redis provides: Has is a
// read, and ClaimIfAbsent is one indivisible check-and-set, so two callers
// cannot both win. A Get-then-Set ClaimIfAbsent would fail
// TestConcurrentReplicasCannotBothWinTheMarker, which is the point of testing
// against the interface rather than a mock that records calls.
type memoryClaimStore struct {
	mu   sync.Mutex
	keys map[string]int64
	// failWith, when set, simulates the store being unreachable.
	failWith error
}

func newMemoryClaimStore() *memoryClaimStore {
	return &memoryClaimStore{keys: map[string]int64{}}
}

func (m *memoryClaimStore) Has(_ context.Context, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failWith != nil {
		return false, m.failWith
	}
	_, exists := m.keys[key]
	return exists, nil
}

func (m *memoryClaimStore) ClaimIfAbsent(_ context.Context, key string, value int64, _ time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failWith != nil {
		return false, m.failWith
	}
	if _, exists := m.keys[key]; exists {
		return false, nil
	}
	m.keys[key] = value
	return true, nil
}

func newTestDeduper(t *testing.T) (*GraphEventDeduper, *memoryClaimStore) {
	t.Helper()
	store := newMemoryClaimStore()
	return newDeduperWithStore(store, time.Hour), store
}

// graphEvent builds the exact wire format graph-service's outbox publisher
// emits: the canonical envelope with the outbox row id as EventID, plus the
// per-pair sequence.
func graphEvent(t *testing.T, eventID uuid.UUID, pairSeq int64, blocker, blocked uuid.UUID) []byte {
	t.Helper()
	payload, err := json.Marshal(sharedevents.UserBlockedPayload{
		BlockerID: blocker.String(),
		BlockedID: blocked.String(),
		BlockedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	actor := blocker.String()
	env := sharedevents.NewEnvelope(context.Background(), sharedevents.UserBlocked, &actor, payload)
	env.EventID = eventID.String()

	body, err := json.Marshal(struct {
		sharedevents.EventEnvelope
		PairSeq int64 `json:"pair_seq"`
	}{EventEnvelope: env, PairSeq: pairSeq})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return body
}

// THE CLB-1 PROPERTY: observing an event must not mark it.
//
// If Seen() wrote the marker, a handler that then failed would have its
// redelivery — the only repair path — suppressed. This is the exact inversion
// the closure review rejected.
func TestSeenDoesNotMarkTheEventApplied(t *testing.T) {
	d, _ := newTestDeduper(t)
	ctx := context.Background()
	msg := graphEvent(t, uuid.New(), 1, uuid.New(), uuid.New())

	// The consumer checks, the handler fails, no mark is written.
	for i := 0; i < 5; i++ {
		seen, err := d.Seen(ctx, msg)
		if err != nil {
			t.Fatalf("check %d: %v", i, err)
		}
		if seen {
			t.Fatalf("delivery %d was reported as already applied after only being "+
				"OBSERVED. A handler failure would leave the event marked and every "+
				"redelivery suppressed — the block would be lost permanently.", i)
		}
	}
}

// A crash-after-publish replay carries the SAME outbox row id, and must be
// recognised once the effect has actually committed.
func TestReplayIsRecognisedOnlyAfterTheEffectIsMarked(t *testing.T) {
	d, _ := newTestDeduper(t)
	ctx := context.Background()
	rowID := uuid.New()
	msg := graphEvent(t, rowID, 1, uuid.New(), uuid.New())

	seen, err := d.Seen(ctx, msg)
	if err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	if seen {
		t.Fatal("the FIRST delivery was treated as a replay; the safety effect would never be applied")
	}

	// The durable effect committed; now — and only now — it is marked.
	if err := d.MarkApplied(ctx, msg); err != nil {
		t.Fatalf("mark applied: %v", err)
	}

	// The relay crashed before marking the row published and redelivers it.
	// Byte-identical, because the publisher derives EventID from the row id
	// rather than generating a fresh uuid per publish.
	for i := 0; i < 5; i++ {
		again, err := d.Seen(ctx, msg)
		if err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
		if !again {
			t.Fatalf("replay %d was NOT recognised: the safety effect is applied "+
				"more than once for a single state transition", i)
		}
	}
}

// Distinct events must NOT be collapsed. A deduper that suppresses real events
// is worse than none: the second block would silently never be applied.
func TestDistinctEventsAreAllProcessed(t *testing.T) {
	d, _ := newTestDeduper(t)
	ctx := context.Background()
	blocker, blocked := uuid.New(), uuid.New()

	for i := 1; i <= 5; i++ {
		msg := graphEvent(t, uuid.New(), int64(i), blocker, blocked)
		seen, err := d.Seen(ctx, msg)
		if err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
		if seen {
			t.Fatalf("event %d (a distinct outbox row) was suppressed as a replay", i)
		}
		if err := d.MarkApplied(ctx, msg); err != nil {
			t.Fatalf("event %d mark: %v", i, err)
		}
	}
}

// The marker write is still one atomic check-and-set, so two replicas that
// both applied the (idempotent) effect converge on one marker rather than
// racing it into an inconsistent state.
func TestConcurrentReplicasCannotBothWinTheMarker(t *testing.T) {
	store := newMemoryClaimStore()
	ctx := context.Background()

	const replicas = 8
	results := make(chan bool, replicas)
	start := make(chan struct{})
	for i := 0; i < replicas; i++ {
		go func() {
			<-start
			won, err := store.ClaimIfAbsent(ctx, "suggestion:applied:x", 1, time.Hour)
			if err != nil {
				t.Errorf("claim: %v", err)
			}
			results <- won
		}()
	}
	close(start)

	winners := 0
	for i := 0; i < replicas; i++ {
		if <-results {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("%d replicas each won the applied marker; want exactly 1", winners)
	}
}

// FAIL OPEN. If the claim store is unreachable the event must be PROCESSED,
// not skipped. Duplicate safety work is wasted effort; skipped safety work is
// the harm.
func TestClaimStoreFailureProcessesRatherThanSkips(t *testing.T) {
	d, store := newTestDeduper(t)
	ctx := context.Background()
	msg := graphEvent(t, uuid.New(), 1, uuid.New(), uuid.New())

	store.failWith = errors.New("redis unreachable")

	seen, err := d.Seen(ctx, msg)
	if err == nil {
		t.Error("a claim-store failure was not reported; it should be visible in logs")
	}
	if seen {
		t.Fatal("a claim-store failure caused the event to be SKIPPED. A block would " +
			"silently never be applied during a cache incident.")
	}
}

// A nil Redis client (a deployment without a cache) must process everything.
func TestNilRedisProcessesEverything(t *testing.T) {
	d := NewGraphEventDeduper(nil, time.Hour)
	msg := graphEvent(t, uuid.New(), 1, uuid.New(), uuid.New())
	seen, err := d.Seen(context.Background(), msg)
	if err != nil {
		t.Fatalf("nil redis should be a no-op, got %v", err)
	}
	if seen {
		t.Fatal("a deployment with no Redis skipped an event")
	}
	if err := d.MarkApplied(context.Background(), msg); err != nil {
		t.Fatalf("nil redis mark should be a no-op, got %v", err)
	}
}

// An event with no id cannot be recognised as a replay, so it must be
// processed rather than dropped.
func TestEventWithoutAnIDIsProcessed(t *testing.T) {
	d, _ := newTestDeduper(t)
	seen, err := d.Seen(context.Background(), []byte(`{"event_type":"UserBlocked"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seen {
		t.Fatal("an event with no id was skipped; it cannot be a known replay")
	}
}
