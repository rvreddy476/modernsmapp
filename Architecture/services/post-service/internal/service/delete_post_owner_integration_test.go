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

// Security sweep #28 (21 Oct 2026): DeletePost is author-only (the service
// checks the row's author, the store's UPDATE re-checks author_id), and no
// test pinned it. A stranger — and a moderator-looking caller is just a
// stranger here — gets ErrPostForbidden and the post stays live; the author's
// delete soft-deletes it.
//
// Run against a scratch database only:
//
//	M7_POSTGRES_DSN=…/post_it_test go test -tags integration ./internal/service/ -run DeletePostIsAuthorOnly -v
func TestIntegrationDeletePostIsAuthorOnly(t *testing.T) {
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

	author, stranger := uuid.New(), uuid.New()
	postID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO posts (id,author_id,text,visibility,content_type,review_status,created_at,updated_at)
		VALUES ($1,$2,'delete owner proof','public','post','approved',NOW(),NOW())
	`, postID, author); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM posts WHERE id=$1`, postID) })

	deleted := func() bool {
		t.Helper()
		var gone bool
		if err := pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM posts WHERE id=$1`, postID).Scan(&gone); err != nil {
			t.Fatal(err)
		}
		return gone
	}

	svc := New(postgres.New(pool), nil, nil)
	if err := svc.DeletePost(ctx, postID, stranger); !errors.Is(err, ErrPostForbidden) {
		t.Fatalf("stranger delete: %v, want ErrPostForbidden", err)
	}
	if deleted() {
		t.Fatal("a stranger soft-deleted someone else's post")
	}
	// The store's own predicate holds even if the service check were skipped.
	if n, err := postgres.New(pool).DeleteUploadCascade(ctx, postID, stranger, svc.PurgeAfter()); err == nil {
		t.Fatalf("store cascade accepted a non-author (%d row(s))", n)
	}
	if deleted() {
		t.Fatal("the store deleted a post for a non-author")
	}

	if err := svc.DeletePost(ctx, postID, author); err != nil {
		t.Fatalf("author delete: %v", err)
	}
	if !deleted() {
		t.Fatal("the author's delete did not soft-delete the post")
	}
}
