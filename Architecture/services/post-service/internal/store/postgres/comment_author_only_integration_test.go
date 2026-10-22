//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/atpost/post-service/internal/store/postgres"
)

// Security sweep #28 (21 Oct 2026): comment delete and edit are author-only
// (comments.go SoftDeleteComment / EditComment compare the row's author to
// the caller), and no test pinned it. Neither a stranger nor the POST's
// author may delete or edit someone else's comment; nothing changes when
// they try; the comment's author may do both.
//
// Run:
//
//	POSTGRES_DSN=…/post_it_test go test -tags integration ./internal/store/postgres/ -run CommentDeleteAndEditAreAuthorOnly -v
func TestIntegrationCommentDeleteAndEditAreAuthorOnly(t *testing.T) {
	pool := openCommentCountDB(t)
	store := postgres.New(pool)
	ctx := context.Background()
	postAuthor, commenter, stranger := uuid.New(), uuid.New(), uuid.New()
	postID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO posts (id, author_id, text, visibility, content_type, created_at, updated_at) VALUES ($1, $2, 'author-only proof', 'public', 'post', now(), now())`,
		postID, postAuthor); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM comment_idempotency WHERE post_id = $1`, postID)
		pool.Exec(ctx, `DELETE FROM comments WHERE post_id = $1`, postID)
		pool.Exec(ctx, `DELETE FROM post_engagement_counts WHERE post_id = $1`, postID)
		pool.Exec(ctx, `DELETE FROM posts WHERE id = $1`, postID)
	})

	c, _, err := store.CreateCommentIdempotent(ctx, postID, commenter, "original body", "author-only-key", "fp")
	if err != nil {
		t.Fatal(err)
	}
	state := func() (body string, deleted bool) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT body, is_deleted FROM comments WHERE id = $1`, c.ID).Scan(&body, &deleted); err != nil {
			t.Fatal(err)
		}
		return
	}

	for name, who := range map[string]uuid.UUID{"stranger": stranger, "post author": postAuthor} {
		if _, err := store.EditComment(ctx, c.ID, who, "rewritten by "+name); err == nil || err.Error() != "NOT_COMMENT_AUTHOR" {
			t.Fatalf("%s edit: %v, want NOT_COMMENT_AUTHOR", name, err)
		}
		if _, _, err := store.SoftDeleteComment(ctx, c.ID, who); err == nil || err.Error() != "NOT_COMMENT_AUTHOR" {
			t.Fatalf("%s delete: %v, want NOT_COMMENT_AUTHOR", name, err)
		}
		if body, deleted := state(); body != "original body" || deleted {
			t.Fatalf("after the %s's refused edit/delete: body=%q deleted=%v", name, body, deleted)
		}
	}

	if _, err := store.EditComment(ctx, c.ID, commenter, "edited by its author"); err != nil {
		t.Fatalf("author edit: %v", err)
	}
	if body, _ := state(); body != "edited by its author" {
		t.Fatalf("author edit did not land: body=%q", body)
	}
	if _, _, err := store.SoftDeleteComment(ctx, c.ID, commenter); err != nil {
		t.Fatalf("author delete: %v", err)
	}
	if _, deleted := state(); !deleted {
		t.Fatal("author delete did not soft-delete the comment")
	}
}
