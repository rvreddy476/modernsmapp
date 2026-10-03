package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Dating mechanic M5 — the first-move gate and the opening-answer bypass,
// on the real SendMessage path with the dating fixture's fake stores.

func (s *datingSendStore) SetDatingFirstMovers(_ context.Context, conversationID uuid.UUID, movers []uuid.UUID) error {
	m := s.meta[conversationID]
	if m == nil || m.LastMessageAt != nil {
		return nil
	}
	m.FirstMovers = movers
	return nil
}

func (s *datingSendStore) DatingConversationByMatch(_ context.Context, matchID uuid.UUID) (uuid.UUID, error) {
	for id, m := range s.meta {
		if m.MatchID != nil && *m.MatchID == matchID {
			return id, nil
		}
	}
	return uuid.Nil, postgres.ErrDatingConversationNotFound
}

// firstMoveFixture is a dating conversation in which only userA moves first.
func firstMoveFixture(t *testing.T) *datingFixture {
	t.Helper()
	f := newDatingFixture(t, "", true)
	f.store.meta[f.convID].FirstMovers = []uuid.UUID{f.userA}
	return f
}

func (f *datingFixture) landFirstMessage() {
	now := time.Now()
	f.store.meta[f.convID].LastMessageAt = &now
}

func TestFirstMove_OnlyTheFirstMoverMaySendFirst(t *testing.T) {
	f := firstMoveFixture(t)
	if _, err := f.svc.SendMessage(context.Background(), f.userB, f.convID, "text", "hi", nil, nil, "m5-b-early"); !errors.Is(err, ErrFirstMovePending) {
		t.Fatalf("the waiting member sent first: err=%v, want ErrFirstMovePending", err)
	}
	f.send(t, f.userA, "m5-a-first")
	f.landFirstMessage()
	// Once the first message has landed, both may write.
	f.send(t, f.userB, "m5-b-reply")
}

func TestFirstMove_NoRuleMeansAnyoneMaySendFirst(t *testing.T) {
	f := newDatingFixture(t, "", true)
	f.send(t, f.userB, "m5-open")
}

func TestFirstMove_OpeningAnswerIsTheWaitingMembersOnlyWayIn(t *testing.T) {
	f := firstMoveFixture(t)
	ctx := context.Background()
	if _, err := f.svc.SendDatingOpeningAnswer(ctx, f.matchID, f.userB, "“Perfect Sunday?” Long walk, then dosa.", "m5-answer"); err != nil {
		t.Fatalf("opening answer: %v", err)
	}
	// The first mover never needs (or gets) the route.
	if _, err := f.svc.SendDatingOpeningAnswer(ctx, f.matchID, f.userA, "x", "m5-answer-a"); !errors.Is(err, ErrOpeningAnswerNotAllowed) {
		t.Fatalf("first mover used the opening answer route: err=%v", err)
	}
	// Not after the first message has landed.
	f.landFirstMessage()
	if _, err := f.svc.SendDatingOpeningAnswer(ctx, f.matchID, f.userB, "again", "m5-answer-2"); !errors.Is(err, ErrOpeningAnswerNotAllowed) {
		t.Fatalf("a second opening answer was accepted: err=%v", err)
	}
}

func TestFirstMove_OpeningAnswerRefusedWithoutARuleOrOnAClosedMatch(t *testing.T) {
	open := newDatingFixture(t, "", true)
	if _, err := open.svc.SendDatingOpeningAnswer(context.Background(), open.matchID, open.userB, "x", "m5-norule"); !errors.Is(err, ErrOpeningAnswerNotAllowed) {
		t.Fatalf("opening answer without a first-move rule: err=%v", err)
	}
	closed := firstMoveFixture(t)
	now := time.Now()
	closed.store.meta[closed.convID].ClosedAt = &now
	if _, err := closed.svc.SendDatingOpeningAnswer(context.Background(), closed.matchID, closed.userB, "x", "m5-closed"); !errors.Is(err, ErrOpeningAnswerNotAllowed) {
		t.Fatalf("opening answer on a closed match: err=%v", err)
	}
	if _, err := closed.svc.SendDatingOpeningAnswer(context.Background(), uuid.New(), closed.userB, "x", "m5-nomatch"); !errors.Is(err, ErrDatingConversationNotFound) {
		t.Fatalf("opening answer for an unknown match: err=%v", err)
	}
}

func TestFirstMove_FirstMoversMustBeThePair(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	for _, ok := range [][]uuid.UUID{nil, {a}, {b}, {a, b}} {
		if err := validateFirstMovers(a, b, ok); err != nil {
			t.Fatalf("first movers %v refused: %v", ok, err)
		}
	}
	if err := validateFirstMovers(a, b, []uuid.UUID{a, uuid.New()}); !errors.Is(err, ErrFirstMoverNotInPair) {
		t.Fatalf("a stranger as first mover: err=%v, want ErrFirstMoverNotInPair", err)
	}
}
