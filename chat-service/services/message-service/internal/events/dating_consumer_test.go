package events

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Dating backend audit H2: dating-service now relays closures through an
// at-least-once outbox, so the same closure can arrive twice. Applying it
// again is harmless (the store keeps the first closed_at), and each applied
// match closure drops the send gate's cached state.

type fakeReconciler struct {
	byMatch []uuid.UUID
	byPair  [][2]uuid.UUID
	err     error
}

func (f *fakeReconciler) MarkConversationClosedByMatch(_ context.Context, matchID uuid.UUID) error {
	if f.err != nil {
		return f.err
	}
	f.byMatch = append(f.byMatch, matchID)
	return nil
}

func (f *fakeReconciler) MarkConversationsClosedByPair(_ context.Context, a, b uuid.UUID) error {
	if f.err != nil {
		return f.err
	}
	f.byPair = append(f.byPair, [2]uuid.UUID{a, b})
	return nil
}

func datingMessage(t *testing.T, eventType string, payload any) kafka.Message {
	t.Helper()
	p, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	// The shared envelope as dating's outbox stores it (extra fields
	// ignored by the consumer).
	v, err := json.Marshal(map[string]any{
		"event_id": uuid.NewString(), "event_type": eventType, "occurred_at": "2026-10-30T12:00:00Z",
		"payload": json.RawMessage(p),
	})
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{Value: v, Headers: []kafka.Header{{Key: "event_type", Value: []byte(eventType)}}}
}

func TestDatingConsumerAppliesRedeliveredClosuresAndInvalidates(t *testing.T) {
	rec := &fakeReconciler{}
	c := &DatingConsumer{store: rec, log: slog.Default()}
	var invalidated []uuid.UUID
	c.SetMatchStateInvalidator(func(id uuid.UUID) { invalidated = append(invalidated, id) })

	mid := uuid.New()
	msg := datingMessage(t, "dating.match.closed", map[string]string{
		"match_id": mid.String(), "closed_by": uuid.NewString(), "reason": "suspended",
	})
	for i := 0; i < 2; i++ { // delivered, then redelivered
		if permanent, err := c.processMessage(context.Background(), msg); err != nil || permanent {
			t.Fatalf("delivery %d: permanent=%v err=%v", i, permanent, err)
		}
	}
	if len(rec.byMatch) != 2 || rec.byMatch[0] != mid || rec.byMatch[1] != mid {
		t.Fatalf("closures applied = %v, want the match twice (idempotent store)", rec.byMatch)
	}
	if len(invalidated) != 2 || invalidated[0] != mid {
		t.Fatalf("invalidated = %v, want the match on each application", invalidated)
	}

	a, b := uuid.New(), uuid.New()
	blocked := datingMessage(t, "dating.user.blocked", map[string]string{"blocker_id": a.String(), "blocked_id": b.String()})
	if permanent, err := c.processMessage(context.Background(), blocked); err != nil || permanent {
		t.Fatalf("user.blocked: permanent=%v err=%v", permanent, err)
	}
	if len(rec.byPair) != 1 || rec.byPair[0] != [2]uuid.UUID{a, b} {
		t.Fatalf("pair closures = %v", rec.byPair)
	}
}

func TestDatingConsumerKeepsAFailedClosureForRetry(t *testing.T) {
	rec := &fakeReconciler{err: errors.New("db down")}
	c := &DatingConsumer{store: rec, log: slog.Default()}
	var invalidated int
	c.SetMatchStateInvalidator(func(uuid.UUID) { invalidated++ })
	msg := datingMessage(t, "dating.match.closed", map[string]string{"match_id": uuid.NewString()})
	permanent, err := c.processMessage(context.Background(), msg)
	if err == nil || permanent {
		t.Fatalf("a store failure must be retried, not discarded: permanent=%v err=%v", permanent, err)
	}
	if invalidated != 0 {
		t.Fatal("invalidated a closure that was not applied")
	}
}
