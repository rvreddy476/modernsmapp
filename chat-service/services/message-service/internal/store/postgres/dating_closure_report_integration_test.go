//go:build integration

package postgres_test

import (
	"context"
	"testing"

	store "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// 10 Nov 2026: a dating closure names the conversations it applied to, so
// the consumer can send the match_closed frame to an open thread.

func TestDatingClosureNamesTheClosedConversations(t *testing.T) {
	pool := openDatingFirstMessageDB(t)
	ctx := context.Background()
	s := store.New(pool)
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	convID, matchID := seedDatingConversation(t, pool, a)
	addMembers(t, pool, convID, a, b)
	otherConv, _ := seedDatingConversation(t, pool, a)
	addMembers(t, pool, otherConv, a, c)

	closed, err := s.CloseDatingConversationsByMatch(ctx, matchID)
	if err != nil {
		t.Fatalf("close by match: %v", err)
	}
	if len(closed) != 1 || closed[0].ConversationID != convID || closed[0].MatchID != matchID || closed[0].ClosedAt.IsZero() {
		t.Fatalf("closed = %+v, want only %v with its match and closed_at", closed, convID)
	}
	first := closed[0].ClosedAt
	// A redelivery names it again, with the first closed_at.
	again, err := s.CloseDatingConversationsByMatch(ctx, matchID)
	if err != nil || len(again) != 1 || !again[0].ClosedAt.Equal(first) {
		t.Fatalf("redelivery = %+v, %v; want the same conversation and closed_at", again, err)
	}
	if none, err := s.CloseDatingConversationsByMatch(ctx, uuid.New()); err != nil || len(none) != 0 {
		t.Fatalf("unknown match = %+v, %v; want nothing", none, err)
	}

	// The pair closure names only what it closed now: the a-c conversation
	// is untouched by an a-b block, and an already-closed one is not named.
	pair, err := s.CloseDatingConversationsByPair(ctx, b, a)
	if err != nil || len(pair) != 0 {
		t.Fatalf("pair on an already-closed conversation = %+v, %v; want nothing", pair, err)
	}
	pair, err = s.CloseDatingConversationsByPair(ctx, c, a)
	if err != nil || len(pair) != 1 || pair[0].ConversationID != otherConv {
		t.Fatalf("pair closure = %+v, %v; want %v", pair, err, otherConv)
	}
}
