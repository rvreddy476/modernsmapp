package events

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// 10 Nov 2026: once a closure is durable, every conversation it applied to
// gets the match_closed frame, so an open Pulse thread can leave.

type reportingReconciler struct {
	fakeReconciler
	closedByMatch map[uuid.UUID][]postgres.ClosedDatingConversation
	closedByPair  []postgres.ClosedDatingConversation
	reportErr     error
}

func (r *reportingReconciler) CloseDatingConversationsByMatch(_ context.Context, matchID uuid.UUID) ([]postgres.ClosedDatingConversation, error) {
	if r.reportErr != nil {
		return nil, r.reportErr
	}
	r.byMatch = append(r.byMatch, matchID)
	return r.closedByMatch[matchID], nil
}

func (r *reportingReconciler) CloseDatingConversationsByPair(_ context.Context, a, b uuid.UUID) ([]postgres.ClosedDatingConversation, error) {
	if r.reportErr != nil {
		return nil, r.reportErr
	}
	r.byPair = append(r.byPair, [2]uuid.UUID{a, b})
	return r.closedByPair, nil
}

func TestDatingConsumerNotifiesEachClosedConversation(t *testing.T) {
	mid, conv := uuid.New(), uuid.New()
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	pairConv1, pairConv2 := uuid.New(), uuid.New()
	rec := &reportingReconciler{
		closedByMatch: map[uuid.UUID][]postgres.ClosedDatingConversation{mid: {{ConversationID: conv, MatchID: mid, ClosedAt: at}}},
		closedByPair:  []postgres.ClosedDatingConversation{{ConversationID: pairConv1}, {ConversationID: pairConv2}},
	}
	c := &DatingConsumer{store: rec, log: slog.Default()}
	var notified []postgres.ClosedDatingConversation
	c.SetMatchClosedNotifier(func(_ context.Context, cl postgres.ClosedDatingConversation) { notified = append(notified, cl) })

	for _, typ := range []string{"dating.match.closed", "dating.match.expired"} {
		notified = nil
		msg := datingMessage(t, typ, map[string]string{"match_id": mid.String()})
		if permanent, err := c.processMessage(context.Background(), msg); err != nil || permanent {
			t.Fatalf("%s: permanent=%v err=%v", typ, permanent, err)
		}
		if len(notified) != 1 || notified[0].ConversationID != conv || notified[0].MatchID != mid || !notified[0].ClosedAt.Equal(at) {
			t.Fatalf("%s: notified %+v, want the match's conversation", typ, notified)
		}
	}
	if len(rec.fakeReconciler.byMatch) != 2 {
		t.Fatalf("closures went around the reporting store: %v", rec.byMatch)
	}

	notified = nil
	a, b := uuid.New(), uuid.New()
	blocked := datingMessage(t, "dating.user.blocked", map[string]string{"blocker_id": a.String(), "blocked_id": b.String()})
	if permanent, err := c.processMessage(context.Background(), blocked); err != nil || permanent {
		t.Fatalf("user.blocked: permanent=%v err=%v", permanent, err)
	}
	if len(notified) != 2 || notified[0].ConversationID != pairConv1 || notified[1].ConversationID != pairConv2 {
		t.Fatalf("user.blocked notified %+v, want both closed conversations", notified)
	}
}

func TestDatingConsumerNotifiesNothingForAFailedClosure(t *testing.T) {
	rec := &reportingReconciler{reportErr: errors.New("db down")}
	c := &DatingConsumer{store: rec, log: slog.Default()}
	notified := 0
	c.SetMatchClosedNotifier(func(context.Context, postgres.ClosedDatingConversation) { notified++ })
	for _, msg := range []kafka.Message{
		datingMessage(t, "dating.match.closed", map[string]string{"match_id": uuid.NewString()}),
		datingMessage(t, "dating.user.blocked", map[string]string{"blocker_id": uuid.NewString(), "blocked_id": uuid.NewString()}),
	} {
		if permanent, err := c.processMessage(context.Background(), msg); err == nil || permanent {
			t.Fatalf("a store failure must be retried: permanent=%v err=%v", permanent, err)
		}
	}
	if notified != 0 {
		t.Fatalf("notified %d times for closures that were not applied", notified)
	}
}
