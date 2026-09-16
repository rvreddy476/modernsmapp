package relationship

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// fakeTerminator records every teardown request and can be made to fail, so
// the retry/permanent split is exercised without Kafka or Postgres.
type fakeTerminator struct {
	mu    sync.Mutex
	calls [][2]uuid.UUID
	ended int
	err   error
}

func (f *fakeTerminator) EndDirectCallsBetween(_ context.Context, a, b uuid.UUID, _ string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	f.calls = append(f.calls, [2]uuid.UUID{a, b})
	return f.ended, nil
}

func (f *fakeTerminator) pairs() [][2]uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][2]uuid.UUID, len(f.calls))
	copy(out, f.calls)
	return out
}

func newTestHandler(term *fakeTerminator) *Handler {
	return NewHandler(term, "permission_revoked", nil)
}

func blockedEnvelope(blocker, blocked uuid.UUID) json.RawMessage {
	b, _ := json.Marshal(map[string]string{
		"blocker_id": blocker.String(),
		"blocked_id": blocked.String(),
	})
	return b
}

func matchClosedEnvelope(userA, userB uuid.UUID) json.RawMessage {
	b, _ := json.Marshal(map[string]string{
		"match_id":  uuid.NewString(),
		"closed_by": userA.String(),
		"user_a":    userA.String(),
		"user_b":    userB.String(),
	})
	return b
}

// A block landing mid-call must reach the teardown with the right pair.
func TestHandlerUserBlockedEndsPairCalls(t *testing.T) {
	term := &fakeTerminator{ended: 1}
	h := newTestHandler(term)

	blocker, blocked := uuid.New(), uuid.New()
	if err := h.Handle(context.Background(), EventUserBlocked, blockedEnvelope(blocker, blocked)); err != nil {
		t.Fatalf("UserBlocked handling failed: %v", err)
	}

	pairs := term.pairs()
	if len(pairs) != 1 {
		t.Fatalf("expected exactly one teardown, got %d", len(pairs))
	}
	if pairs[0][0] != blocker || pairs[0][1] != blocked {
		t.Fatalf("teardown pair mismatch: got %v, want (%s,%s)", pairs[0], blocker, blocked)
	}
}

// An unmatch (dating.match.closed) is the main way a dating user cuts contact
// and must tear the call down exactly like a block.
func TestHandlerDatingMatchClosedEndsPairCalls(t *testing.T) {
	term := &fakeTerminator{ended: 1}
	h := newTestHandler(term)

	userA, userB := uuid.New(), uuid.New()
	if err := h.Handle(context.Background(), EventDatingMatchClosed, matchClosedEnvelope(userA, userB)); err != nil {
		t.Fatalf("dating.match.closed handling failed: %v", err)
	}

	pairs := term.pairs()
	if len(pairs) != 1 {
		t.Fatalf("expected exactly one teardown, got %d", len(pairs))
	}
	if pairs[0][0] != userA || pairs[0][1] != userB {
		t.Fatalf("teardown pair mismatch: got %v, want (%s,%s)", pairs[0], userA, userB)
	}
}

// MUTATION GUARD. If the consumer stops acting on its events — the exact
// mutation "make the teardown consumer ignore its event" — Handles() and the
// dispatch must both notice. Without this, a silently-ignored revocation looks
// identical to a healthy consumer: it acks and commits.
func TestHandlerActsOnBothRevocationEvents(t *testing.T) {
	for _, eventType := range []string{EventUserBlocked, EventDatingMatchClosed} {
		if !Handles(eventType) {
			t.Fatalf("%s must be handled by the teardown consumer", eventType)
		}
	}

	userA, userB := uuid.New(), uuid.New()
	cases := []struct {
		eventType string
		payload   json.RawMessage
	}{
		{EventUserBlocked, blockedEnvelope(userA, userB)},
		{EventDatingMatchClosed, matchClosedEnvelope(userA, userB)},
	}
	for _, tc := range cases {
		term := &fakeTerminator{ended: 1}
		h := newTestHandler(term)
		if err := h.Handle(context.Background(), tc.eventType, tc.payload); err != nil {
			t.Fatalf("%s: %v", tc.eventType, err)
		}
		if got := len(term.pairs()); got != 1 {
			t.Fatalf("%s did not reach the teardown (%d calls) — the consumer is ignoring its event",
				tc.eventType, got)
		}
	}
}

// Events this service does not act on must be a silent no-op, not an error:
// both topics carry plenty of other traffic.
func TestHandlerIgnoresUnrelatedEvents(t *testing.T) {
	term := &fakeTerminator{}
	h := newTestHandler(term)

	for _, eventType := range []string{"ConnectionAccepted", "dating.match.formed", "UserUnblocked"} {
		if Handles(eventType) {
			t.Fatalf("%s must not be treated as a revocation", eventType)
		}
		if err := h.Handle(context.Background(), eventType, json.RawMessage(`{}`)); err != nil {
			t.Fatalf("%s should be ignored, got %v", eventType, err)
		}
	}
	if got := len(term.pairs()); got != 0 {
		t.Fatalf("unrelated events triggered %d teardowns", got)
	}
}

// Undecodable / nonsensical payloads are permanent: they must be skipped so one
// poison message cannot block every later revocation on the partition.
func TestHandlerRejectsUnusablePayloadsAsPermanent(t *testing.T) {
	shared := uuid.New()
	cases := []struct {
		name      string
		eventType string
		payload   string
	}{
		{"malformed json", EventUserBlocked, `{"blocker_id":`},
		{"bad uuid", EventUserBlocked, `{"blocker_id":"nope","blocked_id":"also-nope"}`},
		{"nil uuid", EventUserBlocked,
			fmt.Sprintf(`{"blocker_id":"00000000-0000-0000-0000-000000000000","blocked_id":%q}`, shared)},
		{"self pair", EventDatingMatchClosed,
			fmt.Sprintf(`{"user_a":%q,"user_b":%q}`, shared, shared)},
		{"missing pair", EventDatingMatchClosed, `{"match_id":"x"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term := &fakeTerminator{}
			h := newTestHandler(term)
			err := h.Handle(context.Background(), tc.eventType, json.RawMessage(tc.payload))
			if !errors.Is(err, ErrPermanent) {
				t.Fatalf("expected ErrPermanent, got %v", err)
			}
			if got := len(term.pairs()); got != 0 {
				t.Fatalf("unusable payload still triggered %d teardowns", got)
			}
			// Permanent errors must not stall the partition.
			if !h.HandleUntilDurable(context.Background(), tc.eventType, json.RawMessage(tc.payload)) {
				t.Fatal("permanent error must be skipped, not retried forever")
			}
		})
	}
}

// A teardown that FAILS must not be acked: the offset has to stay put so the
// revocation is retried. A missed teardown is a safety failure, not a lost
// metric.
func TestHandlerHoldsOffsetWhenTeardownFails(t *testing.T) {
	term := &fakeTerminator{err: errors.New("database down")}
	h := newTestHandler(term)

	err := h.Handle(context.Background(), EventUserBlocked, blockedEnvelope(uuid.New(), uuid.New()))
	if err == nil {
		t.Fatal("a failed teardown must return an error so the event is redelivered")
	}
	if errors.Is(err, ErrPermanent) {
		t.Fatal("a transient store failure must NOT be classified permanent — the teardown would be dropped")
	}

	// HandleUntilDurable must refuse to ack while the failure persists; it
	// returns false only on cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if h.HandleUntilDurable(ctx, EventUserBlocked, blockedEnvelope(uuid.New(), uuid.New())) {
		t.Fatal("a still-failing teardown must not report durable")
	}
}
