//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/atpost/post-service/internal/store/postgres"
)

/*
PUT /v1/posts/:postId/pin counts the author's pinned posts LEAVING OUT the
post being pinned, so re-pinning one of three is not a fourth (*_test only).

Run:
  POSTGRES_DSN=…/post_it_test go test -tags integration ./internal/store/postgres/ -run PinnedCount -v
*/

func TestPinnedCountLeavesOutThePostBeingPinned(t *testing.T) {
	pool := openCommentCountDB(t)
	store := postgres.New(pool)
	ctx := context.Background()
	author := uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM post_engagement_counts WHERE post_id IN (SELECT id FROM posts WHERE author_id = $1)`, author)
		_, _ = pool.Exec(context.Background(), `DELETE FROM posts WHERE author_id = $1`, author)
	})

	pinned := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for _, id := range pinned {
		if _, err := pool.Exec(ctx, `INSERT INTO posts (id, author_id, text, visibility, content_type, is_pinned, created_at, updated_at)
			VALUES ($1, $2, 'pinned', 'public', 'post', true, now(), now())`, id, author); err != nil {
			t.Fatal(err)
		}
	}
	// A deleted pinned post never counts.
	if _, err := pool.Exec(ctx, `INSERT INTO posts (id, author_id, text, visibility, content_type, is_pinned, created_at, updated_at, deleted_at)
		VALUES ($1, $2, 'gone', 'public', 'post', true, now(), now(), now())`, uuid.New(), author); err != nil {
		t.Fatal(err)
	}

	if n, err := store.CountPinnedByAuthor(ctx, author, uuid.Nil); err != nil || n != 3 {
		t.Fatalf("count = %d (err %v), want 3", n, err)
	}
	if n, err := store.CountPinnedByAuthor(ctx, author, uuid.New()); err != nil || n != 3 {
		t.Fatalf("count for a new post = %d (err %v), want 3", n, err)
	}
	if n, err := store.CountPinnedByAuthor(ctx, author, pinned[1]); err != nil || n != 2 {
		t.Fatalf("count re-pinning one of the three = %d (err %v), want 2", n, err)
	}
}
