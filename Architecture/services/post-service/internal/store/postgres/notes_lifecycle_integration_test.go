//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Echo delete / restore against a real Postgres (2026-11-13).
//
//	POSTGRES_DSN=…/post_it_test go test -tags integration ./internal/store/postgres/ -run NoteLifecycle -v
//
// A reply's parent reply_count goes down once on delete and up once on
// restore, whichever route deleted it, and a repeated delete or restore
// moves nothing. PostDeleted names the reply's or quote's target author.

func seedNote(t *testing.T, pool *pgxpool.Pool, author uuid.UUID, replyTo, quoteOf *uuid.UUID, replyCount int) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	conv := id
	if replyTo != nil {
		conv = *replyTo
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO posts (id, author_id, text, visibility, content_type, review_status, created_at, updated_at,
			reply_to_id, conversation_id, quote_of_id, reply_policy, reply_count)
		VALUES ($1, $2, 'a note', 'public', 'note', 'approved', now(), now(), $3, $4, $5, 'everyone', $6)`,
		id, author, replyTo, conv, quoteOf, replyCount); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM post_outbox_events WHERE aggregate_id = $1`, id)
		_, _ = pool.Exec(context.Background(), `DELETE FROM posts WHERE id = $1`, id)
	})
	return id
}

func replyCount(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT reply_count FROM posts WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func lastPostDeleted(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) events.PostDeletedPayload {
	t.Helper()
	var raw []byte
	if err := pool.QueryRow(context.Background(), `
		SELECT payload FROM post_outbox_events WHERE aggregate_id = $1 AND event_type = $2
		ORDER BY created_at DESC LIMIT 1`, id, events.PostDeleted).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var p events.PostDeletedPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNoteLifecycleReplyCountMovesOncePerTransition(t *testing.T) {
	pool := openSubscriptionDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	alice, bob := uuid.New(), uuid.New()
	newUser(t, pool, alice)
	newUser(t, pool, bob)
	parent := seedNote(t, pool, alice, nil, nil, 1)
	reply := seedNote(t, pool, bob, &parent, nil, 0)

	if _, err := st.DeleteUploadCascade(ctx, reply, bob, time.Hour); err != nil {
		t.Fatal(err)
	}
	if got := replyCount(t, pool, parent); got != 0 {
		t.Fatalf("after delete: reply_count %d, want 0", got)
	}
	ev := lastPostDeleted(t, pool, reply)
	if ev.ReplyToID != parent.String() || ev.TargetAuthorID != alice.String() || ev.QuoteOfID != "" {
		t.Fatalf("PostDeleted of a reply: %+v", ev)
	}
	// A second delete finds no live row and moves nothing.
	if _, err := st.DeleteUploadCascade(ctx, reply, bob, time.Hour); err == nil {
		t.Fatal("second delete succeeded")
	}
	if got := replyCount(t, pool, parent); got != 0 {
		t.Fatalf("after a repeated delete: reply_count %d, want 0", got)
	}

	if _, err := st.RestorePost(ctx, reply, bob, time.Hour); err != nil {
		t.Fatal(err)
	}
	if got := replyCount(t, pool, parent); got != 1 {
		t.Fatalf("after restore: reply_count %d, want 1", got)
	}
	if _, err := st.RestorePost(ctx, reply, bob, time.Hour); !errors.Is(err, postgres.ErrPostNotDeleted) {
		t.Fatalf("second restore: %v, want ErrPostNotDeleted", err)
	}
	if got := replyCount(t, pool, parent); got != 1 {
		t.Fatalf("after a repeated restore: reply_count %d, want 1", got)
	}
}

func TestNoteLifecycleQuoteAndTopLevelLeaveCountsAlone(t *testing.T) {
	pool := openSubscriptionDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	alice, bob := uuid.New(), uuid.New()
	newUser(t, pool, alice)
	newUser(t, pool, bob)
	target := seedNote(t, pool, alice, nil, nil, 3)
	quote := seedNote(t, pool, bob, nil, &target, 0)

	if _, err := st.DeleteUploadCascade(ctx, quote, bob, time.Hour); err != nil {
		t.Fatal(err)
	}
	ev := lastPostDeleted(t, pool, quote)
	if ev.QuoteOfID != target.String() || ev.TargetAuthorID != alice.String() || ev.ReplyToID != "" {
		t.Fatalf("PostDeleted of a quote: %+v", ev)
	}
	if _, err := st.RestorePost(ctx, quote, bob, time.Hour); err != nil {
		t.Fatal(err)
	}
	if got := replyCount(t, pool, target); got != 3 {
		t.Fatalf("a quote moved the target's reply_count to %d", got)
	}

	if _, err := st.DeleteUploadCascade(ctx, target, alice, time.Hour); err != nil {
		t.Fatal(err)
	}
	if ev := lastPostDeleted(t, pool, target); ev.TargetAuthorID != "" || ev.ReplyToID != "" || ev.QuoteOfID != "" {
		t.Fatalf("PostDeleted of a top-level note names a target: %+v", ev)
	}
}
