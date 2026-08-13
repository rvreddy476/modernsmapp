//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/atpost/post-service/database"
	store "github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openM7PostDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("M7_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("M7_POSTGRES_DSN is required")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	// post-service intentionally has a cross-service FK to the canonical media
	// table. This fresh proof DB therefore installs the real parent shape before
	// applying post migrations, matching production's shared app database.
	if _, err := pool.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS media_assets (
			id UUID PRIMARY KEY,
			uploader_id UUID NOT NULL,
			file_type TEXT NOT NULL,
			processing_status TEXT NOT NULL,
			moderation_status TEXT NOT NULL DEFAULT 'pending',
			duration_seconds INTEGER,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), database.SetupSQL); err != nil {
		pool.Close()
		t.Fatalf("apply real setup.sql: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS post_outbox_events (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(), event_type TEXT NOT NULL,
			aggregate_type TEXT NOT NULL, aggregate_id UUID NOT NULL, payload JSONB NOT NULL,
			published BOOLEAN NOT NULL DEFAULT FALSE, published_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	migration, err := database.Migrations.ReadFile("migrations/033_post_moderation_authority.sql")
	if err != nil {
		pool.Close()
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), string(migration)); err != nil {
		pool.Close()
		t.Fatalf("apply real migration 033: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedModerationPost(t *testing.T, pool *pgxpool.Pool, status string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	postID, authorID := uuid.New(), uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO posts (id,author_id,text,visibility,content_type,review_status,search_rev,created_at,updated_at)
		VALUES ($1,$2,'launch proof','public','post',$3,1,NOW(),NOW())
	`, postID, authorID, status)
	if err != nil {
		t.Fatal(err)
	}
	return postID, authorID
}

func TestModerationDecisionExactlyOnceAndAtomic(t *testing.T) {
	pool := openM7PostDB(t)
	ctx := context.Background()
	s := store.New(pool)
	postID, _ := seedModerationPost(t, pool, "approved")
	decisionID, actorID := uuid.New(), uuid.New()
	in := store.ModeratePostInput{
		DecisionID: decisionID, PostID: postID, ActorID: actorID,
		Action: "reject", Reason: "confirmed launch policy violation", Source: "admin",
	}

	const workers = 100
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.ModeratePost(ctx, in); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent retry failed: %v", err)
		}
	}

	var status string
	var rev int64
	if err := pool.QueryRow(ctx, `SELECT review_status,search_rev FROM posts WHERE id=$1`, postID).Scan(&status, &rev); err != nil {
		t.Fatal(err)
	}
	if status != "rejected" || rev != 2 {
		t.Fatalf("status/rev=(%s,%d), want rejected/2", status, rev)
	}
	var decisions, outbox int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM post_moderation_decisions WHERE decision_id=$1`, decisionID).Scan(&decisions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM post_outbox_events WHERE aggregate_id=$1 AND event_type='PostSearchEligibilityChanged'`, postID).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if decisions != 1 || outbox != 1 {
		t.Fatalf("decisions/outbox=(%d,%d), want 1/1", decisions, outbox)
	}

	conflict := in
	conflict.Reason = "different immutable claim"
	if _, err := s.ModeratePost(ctx, conflict); !errors.Is(err, store.ErrModerationDecisionConflict) {
		t.Fatalf("same decision ID with changed reason = %v, want conflict", err)
	}
}

func TestModerationOutboxFailureRollsBackEverything(t *testing.T) {
	pool := openM7PostDB(t)
	ctx := context.Background()
	s := store.New(pool)
	postID, _ := seedModerationPost(t, pool, "approved")
	decisionID := uuid.New()

	_, _ = pool.Exec(ctx, `DROP TRIGGER IF EXISTS m7_fail_outbox ON post_outbox_events`)
	_, _ = pool.Exec(ctx, `DROP FUNCTION IF EXISTS m7_fail_outbox()`)
	_, err := pool.Exec(ctx, `CREATE FUNCTION m7_fail_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected outbox failure'; END $$`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `CREATE TRIGGER m7_fail_outbox BEFORE INSERT ON post_outbox_events FOR EACH ROW EXECUTE FUNCTION m7_fail_outbox()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DROP TRIGGER IF EXISTS m7_fail_outbox ON post_outbox_events`)
		_, _ = pool.Exec(ctx, `DROP FUNCTION IF EXISTS m7_fail_outbox()`)
	})

	_, err = s.ModeratePost(ctx, store.ModeratePostInput{DecisionID: decisionID, PostID: postID, ActorID: uuid.New(), Action: "reject", Reason: "rollback proof", Source: "admin"})
	if err == nil {
		t.Fatal("injected outbox failure unexpectedly succeeded")
	}
	var status string
	var rev int64
	var decisions int
	if err := pool.QueryRow(ctx, `SELECT review_status,search_rev FROM posts WHERE id=$1`, postID).Scan(&status, &rev); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM post_moderation_decisions WHERE decision_id=$1`, decisionID).Scan(&decisions); err != nil {
		t.Fatal(err)
	}
	if status != "approved" || rev != 1 || decisions != 0 {
		t.Fatalf("rollback left status/rev/decisions=%s/%d/%d", status, rev, decisions)
	}
}
