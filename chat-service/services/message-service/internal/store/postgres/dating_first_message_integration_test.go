//go:build integration

package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/database"
	store "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// openDatingFirstMessageDB refuses any database whose name does not end in
// "_test": this suite drops the chat schema.
func openDatingFirstMessageDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := requireScratchDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS chat CASCADE`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // bootstrap must stay re-runnable
		if err := store.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
			t.Fatalf("bootstrap %d: %v", i, err)
		}
	}
	return pool
}

func requireScratchDSN(t *testing.T) string {
	t.Helper()
	dsn := strings.TrimSpace(getenv("CHAT_POSTGRES_DSN"))
	if dsn == "" {
		t.Fatal("CHAT_POSTGRES_DSN is required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse CHAT_POSTGRES_DSN: %v", err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing database %q: the D4 suite drops the chat schema; use a *_test database", cfg.ConnConfig.Database)
	}
	return dsn
}

func seedDatingConversation(t *testing.T, pool *pgxpool.Pool, creator uuid.UUID) (uuid.UUID, uuid.UUID) {
	t.Helper()
	convID, matchID := uuid.New(), uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO chat.conversations (id, type, created_by, source_app, match_id)
		VALUES ($1, 'direct', $2, 'dating', $3)
	`, convID, creator, matchID); err != nil {
		t.Fatal(err)
	}
	return convID, matchID
}

func TestDatingFirstMessageObligationIsOncePerConversation(t *testing.T) {
	pool := openDatingFirstMessageDB(t)
	ctx := context.Background()
	s := store.New(pool)
	a, b := uuid.New(), uuid.New()
	convID, matchID := seedDatingConversation(t, pool, a)

	created, err := s.EnqueueDatingFirstMessage(ctx, store.DatingFirstMessageNotification{
		ConversationID: convID, MatchID: matchID, ActorID: a, MessageID: uuid.New(),
	})
	if err != nil || !created {
		t.Fatalf("first enqueue: created=%v err=%v", created, err)
	}
	created, err = s.EnqueueDatingFirstMessage(ctx, store.DatingFirstMessageNotification{
		ConversationID: convID, MatchID: matchID, ActorID: b, MessageID: uuid.New(),
	})
	if err != nil || created {
		t.Fatalf("second message must not create a second obligation: created=%v err=%v", created, err)
	}

	due, err := s.ClaimDueDatingFirstMessages(ctx, 10, time.Minute)
	if err != nil || len(due) != 1 {
		t.Fatalf("claim: n=%d err=%v", len(due), err)
	}
	if due[0].ActorID != a || due[0].MatchID != matchID || due[0].AttemptCount != 1 {
		t.Fatalf("claimed row = %+v, want first sender %s, attempt 1", due[0], a)
	}
	if again, _ := s.ClaimDueDatingFirstMessages(ctx, 10, time.Minute); len(again) != 0 {
		t.Fatalf("a leased row must not be claimed twice, got %d", len(again))
	}

	// Transient failure: deferred rows are not due until the backoff passes.
	if err := s.DeferDatingFirstMessage(ctx, convID, time.Hour, 503, "dating 503"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.ClaimDueDatingFirstMessages(ctx, 10, time.Minute); len(n) != 0 {
		t.Fatalf("a deferred row was claimed before its backoff: %d", len(n))
	}
	forceDue(t, pool, convID)
	due, err = s.ClaimDueDatingFirstMessages(ctx, 10, time.Minute)
	if err != nil || len(due) != 1 || due[0].AttemptCount != 2 {
		t.Fatalf("retry claim: n=%d err=%v", len(due), err)
	}

	if err := s.MarkDatingFirstMessageDelivered(ctx, convID, 200); err != nil {
		t.Fatal(err)
	}
	forceDue(t, pool, convID)
	if n, _ := s.ClaimDueDatingFirstMessages(ctx, 10, time.Minute); len(n) != 0 {
		t.Fatalf("a delivered row must never be claimed again, got %d", len(n))
	}
	// A later message after delivery must not re-arm the row.
	created, err = s.EnqueueDatingFirstMessage(ctx, store.DatingFirstMessageNotification{
		ConversationID: convID, MatchID: matchID, ActorID: b, MessageID: uuid.New(),
	})
	if err != nil || created {
		t.Fatalf("post-delivery enqueue: created=%v err=%v", created, err)
	}
	if n, _ := s.ClaimDueDatingFirstMessages(ctx, 10, time.Minute); len(n) != 0 {
		t.Fatalf("a later message re-armed a delivered obligation: %d claimed", len(n))
	}
	var delivered bool
	var actor uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT delivered_at IS NOT NULL, actor_id FROM chat.dating_first_message_notifications WHERE conversation_id = $1`, convID).Scan(&delivered, &actor); err != nil {
		t.Fatal(err)
	}
	if !delivered || actor != a {
		t.Fatalf("delivered=%v actor=%s after a later message", delivered, actor)
	}
}

func TestDatingFirstMessageTerminalRowIsNeverRetriedAndPurgeErasesIt(t *testing.T) {
	pool := openDatingFirstMessageDB(t)
	ctx := context.Background()
	s := store.New(pool)
	a := uuid.New()
	convID, matchID := seedDatingConversation(t, pool, a)

	if _, err := s.EnqueueDatingFirstMessage(ctx, store.DatingFirstMessageNotification{
		ConversationID: convID, MatchID: matchID, ActorID: a, MessageID: uuid.New(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimDueDatingFirstMessages(ctx, 10, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDatingFirstMessageTerminal(ctx, convID, 410, "moved"); err != nil {
		t.Fatal(err)
	}
	forceDue(t, pool, convID)
	if n, _ := s.ClaimDueDatingFirstMessages(ctx, 10, time.Minute); len(n) != 0 {
		t.Fatalf("a terminal row must never be retried, got %d", len(n))
	}
	var status int
	if err := pool.QueryRow(ctx, `SELECT last_status FROM chat.dating_first_message_notifications WHERE conversation_id = $1`, convID).Scan(&status); err != nil || status != 410 {
		t.Fatalf("last_status=%d err=%v", status, err)
	}

	if err := s.PurgeUser(ctx, a); err != nil {
		t.Fatalf("purge: %v", err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM chat.dating_first_message_notifications WHERE actor_id = $1`, a).Scan(&left); err != nil || left != 0 {
		t.Fatalf("purge left %d obligations keyed by the user (err=%v)", left, err)
	}
}

func forceDue(t *testing.T, pool *pgxpool.Pool, convID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE chat.dating_first_message_notifications SET next_attempt_at = now() - interval '1 second' WHERE conversation_id = $1`, convID); err != nil {
		t.Fatal(err)
	}
}
