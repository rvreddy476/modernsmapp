//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	store "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Dating backend audit H2: dating-service relays closures through an
// at-least-once outbox, so chat can apply the same dating.match.closed or
// dating.user.blocked twice. A redelivery keeps the first closed_at and
// touches no other conversation; the send gate keeps reading it as closed.

func closedAt(t *testing.T, pool *pgxpool.Pool, convID uuid.UUID) *time.Time {
	t.Helper()
	var at *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT closed_at FROM chat.conversations WHERE id = $1`, convID).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

func addMembers(t *testing.T, pool *pgxpool.Pool, convID uuid.UUID, users ...uuid.UUID) {
	t.Helper()
	for _, u := range users {
		if _, err := pool.Exec(context.Background(), `
			INSERT INTO chat.conversation_members (conversation_id, user_id, role) VALUES ($1, $2, 'member')
		`, convID, u); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDatingClosureRedeliveryIsIdempotent(t *testing.T) {
	pool := openDatingFirstMessageDB(t)
	ctx := context.Background()
	s := store.New(pool)
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	convID, matchID := seedDatingConversation(t, pool, a)
	addMembers(t, pool, convID, a, b)
	otherConv, _ := seedDatingConversation(t, pool, a)
	addMembers(t, pool, otherConv, a, c)

	if err := s.MarkConversationClosedByMatch(ctx, matchID); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	first := closedAt(t, pool, convID)
	if first == nil {
		t.Fatal("match.closed did not close the conversation")
	}
	time.Sleep(10 * time.Millisecond)
	if err := s.MarkConversationClosedByMatch(ctx, matchID); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if again := closedAt(t, pool, convID); again == nil || !again.Equal(*first) {
		t.Fatalf("redelivery moved closed_at %v -> %v", first, again)
	}
	// The pair closure (user.blocked) redelivered on top: still the first
	// closed_at, and the other match's conversation stays open.
	for i := 0; i < 2; i++ {
		if err := s.MarkConversationsClosedByPair(ctx, b, a); err != nil {
			t.Fatalf("user.blocked delivery %d: %v", i, err)
		}
	}
	if again := closedAt(t, pool, convID); again == nil || !again.Equal(*first) {
		t.Fatalf("pair closure moved closed_at %v -> %v", first, again)
	}
	if other := closedAt(t, pool, otherConv); other != nil {
		t.Fatal("closing one pair closed another match's conversation")
	}
	meta, err := s.GetConversationMeta(ctx, convID)
	if err != nil || meta == nil || meta.ClosedAt == nil || meta.MatchID == nil || *meta.MatchID != matchID {
		t.Fatalf("send-gate meta after redelivery = %+v, %v; want closed with the match id", meta, err)
	}
}
