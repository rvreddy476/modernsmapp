//go:build integration

package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/atpost/post-service/database"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The two owner checks that live in SQL, proven against the real schema
// (lane post-service-2, 2026-11-18): restore (undelete) is the author's only
// (store.RestorePost's author_id predicate), and a reels-series episode
// number is never overwritten (lv-74: the former ON CONFLICT DO UPDATE).
//
// Run against a scratch database only:
//
//	M7_POSTGRES_DSN=…/post_it_test go test -tags integration ./internal/service/ -run OwnerRoutesStranger -v
func ownerRoutesPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("M7_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("M7_POSTGRES_DSN not set")
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("parse M7_POSTGRES_DSN")
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run against database %q: name must end in _test", cfg.ConnConfig.Database)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap real schema: %v", err)
	}
	return pool
}

func TestIntegrationOwnerRoutesStrangerRestoreIsTheAuthors(t *testing.T) {
	pool := ownerRoutesPool(t)
	ctx := context.Background()
	author, stranger, postID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO posts (id,author_id,text,visibility,content_type,review_status,created_at,updated_at,deleted_at)
		VALUES ($1,$2,'restore owner proof','public','post','approved',NOW(),NOW(),NOW())
	`, postID, author); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM post_engagement_counts WHERE post_id = $1`, postID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM posts WHERE id=$1`, postID)
	})
	deleted := func() bool {
		t.Helper()
		var gone bool
		if err := pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM posts WHERE id=$1`, postID).Scan(&gone); err != nil {
			t.Fatal(err)
		}
		return gone
	}
	svc := New(postgres.New(pool), nil, nil)
	if err := svc.RestorePost(ctx, postID, stranger); !errors.Is(err, postgres.ErrPostNotDeleted) {
		t.Fatalf("stranger restore: %v, want ErrPostNotDeleted (the 404 a missing post gets)", err)
	}
	if !deleted() {
		t.Fatal("a stranger restored someone else's deleted post")
	}
	if err := svc.RestorePost(ctx, postID, author); err != nil {
		t.Fatalf("author restore: %v", err)
	}
	if deleted() {
		t.Fatal("the author's restore left the post deleted")
	}
}

func TestIntegrationOwnerRoutesStrangerFlickEpisodeNumberIsNotOverwritten(t *testing.T) {
	pool := ownerRoutesPool(t)
	ctx := context.Background()
	store := postgres.New(pool)
	owner := uuid.New()
	first, second := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{first, second} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO posts (id,author_id,text,visibility,content_type,review_status,created_at,updated_at)
			VALUES ($1,$2,'flick episode proof','public','flick','approved',NOW(),NOW())`, id, owner); err != nil {
			t.Fatal(err)
		}
	}
	fs, err := store.CreateFlickSeries(ctx, owner, "lv-74 proof", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM flick_series_items WHERE series_id=$1`, fs.ID)
		_, _ = pool.Exec(bg, `DELETE FROM flick_series WHERE id=$1`, fs.ID)
		_, _ = pool.Exec(bg, `DELETE FROM post_engagement_counts WHERE post_id = ANY($1)`, []uuid.UUID{first, second})
		_, _ = pool.Exec(bg, `DELETE FROM posts WHERE id = ANY($1)`, []uuid.UUID{first, second})
	})
	if _, err := store.AddEpisodeToSeries(ctx, fs.ID, first, 1); err != nil {
		t.Fatalf("first episode: %v", err)
	}
	// Re-adding the same post at its own number stays a no-op success.
	if _, err := store.AddEpisodeToSeries(ctx, fs.ID, first, 1); err != nil {
		t.Fatalf("idempotent re-add: %v", err)
	}
	if _, err := store.AddEpisodeToSeries(ctx, fs.ID, second, 1); !errors.Is(err, postgres.ErrFlickEpisodeNumberTaken) {
		t.Fatalf("occupied number: %v, want ErrFlickEpisodeNumberTaken", err)
	}
	var held uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT post_id FROM flick_series_items WHERE series_id=$1 AND episode_num=1`, fs.ID).Scan(&held); err != nil {
		t.Fatal(err)
	}
	if held != first {
		t.Fatal("episode 1 was overwritten")
	}
}
