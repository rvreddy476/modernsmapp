//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atpost/post-service/database"
	"github.com/atpost/post-service/internal/store/postgres"
)

/*
comment_count against a real Postgres (*_test only): the stored count must
equal the definition after every transition the service performs — create,
reply, delete (once, even when retried), moderation into and out of the
visible set, the auto-review flip — and the recount must repair a corrupted
row and reset a post whose comments are all gone.

Run:
  POSTGRES_DSN=…/post_it_test go test -tags integration ./internal/store/postgres/ -run CommentCount -v
*/
func openCommentCountDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := requireTestDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS users (id UUID PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := postgres.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// A scratch database bootstrapped before dislike_count existed has no
	// migration adding it (setup.sql carries it for fresh installs).
	if _, err := pool.Exec(ctx, `ALTER TABLE comments ADD COLUMN IF NOT EXISTS dislike_count INTEGER NOT NULL DEFAULT 0`); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestCommentCountFollowsEveryTransition(t *testing.T) {
	pool := openCommentCountDB(t)
	store := postgres.New(pool)
	ctx := context.Background()
	author, commenter, moderator := uuid.New(), uuid.New(), uuid.New()
	postID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO posts (id, author_id, text, visibility, content_type, created_at, updated_at) VALUES ($1, $2, 'count me', 'public', 'post', now(), now())`,
		postID, author); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM post_admin_audit WHERE target_id IN (SELECT id FROM comments WHERE post_id = $1)`, postID)
		pool.Exec(ctx, `DELETE FROM comment_idempotency WHERE post_id = $1`, postID)
		pool.Exec(ctx, `DELETE FROM comments WHERE post_id = $1`, postID)
		pool.Exec(ctx, `DELETE FROM post_engagement_counts WHERE post_id = $1`, postID)
		pool.Exec(ctx, `DELETE FROM posts WHERE id = $1`, postID)
	})

	// The service's contract, replayed here step by step: every transition
	// into/out of the visible set is one AdjustCommentCount.
	assertCount := func(step string, want int64) {
		t.Helper()
		stored, err := store.GetCommentCount(ctx, postID)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := store.CountVisibleComments(ctx, postID)
		if err != nil {
			t.Fatal(err)
		}
		if stored != want || actual != want {
			t.Fatalf("%s: stored=%d definition=%d want %d", step, stored, actual, want)
		}
	}

	c1, replayed, err := store.CreateCommentIdempotent(ctx, postID, commenter, "first", "key-1", "fp")
	if err != nil || replayed {
		t.Fatalf("create: %v replayed=%v", err, replayed)
	}
	if err := store.AdjustCommentCount(ctx, postID, 1); err != nil {
		t.Fatal(err)
	}
	assertCount("create", 1)

	// Same key again: replayed, and the service does not adjust on a replay.
	c1again, replayed, err := store.CreateCommentIdempotent(ctx, postID, commenter, "first", "key-1", "fp")
	if err != nil || !replayed || c1again.ID != c1.ID {
		t.Fatalf("replay: %v replayed=%v same=%v", err, replayed, c1again != nil && c1again.ID == c1.ID)
	}
	assertCount("replayed create", 1)

	reply, _, err := store.CreateReply(ctx, c1.ID, author, "reply")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AdjustCommentCount(ctx, postID, 1); err != nil {
		t.Fatal(err)
	}
	assertCount("reply counts", 2)

	// Moderation: hidden leaves the count, visible again returns.
	if _, prev, err := store.SetCommentModerationStatus(ctx, moderator, c1.ID, "hidden"); err != nil || prev != "visible" {
		t.Fatalf("hide: %v prev=%q", err, prev)
	}
	if err := store.AdjustCommentCount(ctx, postID, -1); err != nil {
		t.Fatal(err)
	}
	assertCount("hidden leaves", 1)
	if _, prev, err := store.SetCommentModerationStatus(ctx, moderator, c1.ID, "visible"); err != nil || prev != "hidden" {
		t.Fatalf("unhide: %v prev=%q", err, prev)
	}
	if err := store.AdjustCommentCount(ctx, postID, 1); err != nil {
		t.Fatal(err)
	}
	assertCount("visible again", 2)

	// Auto-review flip: three reports on the reply move it out, once.
	for i := 0; i < 3; i++ {
		pid, flipped, err := store.IncrementCommentFlaggedCount(ctx, reply.ID)
		if err != nil {
			t.Fatal(err)
		}
		if flipped {
			if pid != postID {
				t.Fatalf("flip reported post %s", pid)
			}
			if err := store.AdjustCommentCount(ctx, postID, -1); err != nil {
				t.Fatal(err)
			}
		}
	}
	assertCount("auto-review leaves", 1)
	if _, flipped, _ := store.IncrementCommentFlaggedCount(ctx, reply.ID); flipped {
		t.Fatal("a fourth report flipped again")
	}

	// Delete: a hidden/held comment was not counted, so deleting it must not decrement.
	if _, counted, err := store.SoftDeleteComment(ctx, reply.ID, author); err != nil || counted {
		t.Fatalf("delete held reply: %v counted=%v (must be false)", err, counted)
	}
	assertCount("deleting a held reply changes nothing", 1)

	// Delete the visible comment: counted once; a retry is refused.
	pid, counted, err := store.SoftDeleteComment(ctx, c1.ID, commenter)
	if err != nil || !counted || pid != postID {
		t.Fatalf("delete: %v counted=%v", err, counted)
	}
	if err := store.AdjustCommentCount(ctx, postID, -1); err != nil {
		t.Fatal(err)
	}
	assertCount("delete", 0)
	if _, _, err := store.SoftDeleteComment(ctx, c1.ID, commenter); err == nil {
		t.Fatal("a second delete of the same comment succeeded — the service would decrement twice")
	}
}

func TestRecountRepairsDriftAndZeroes(t *testing.T) {
	pool := openCommentCountDB(t)
	store := postgres.New(pool)
	ctx := context.Background()
	author := uuid.New()
	withComments, emptied := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{withComments, emptied} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO posts (id, author_id, text, visibility, content_type, created_at, updated_at) VALUES ($1, $2, 'recount', 'public', 'post', now(), now())`, id, author); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM comments WHERE post_id IN ($1, $2)`, withComments, emptied)
		pool.Exec(ctx, `DELETE FROM post_engagement_counts WHERE post_id IN ($1, $2)`, withComments, emptied)
		pool.Exec(ctx, `DELETE FROM posts WHERE id IN ($1, $2)`, withComments, emptied)
	})
	// Two visible, one hidden, one deleted → definition says 2.
	for i, spec := range []struct{ status string; deleted bool }{{"visible", false}, {"visible", false}, {"hidden", false}, {"visible", true}} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO comments (id, post_id, author_id, body, is_reply, moderation_status, is_deleted, created_at, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, now(), now())`,
			uuid.New(), withComments, author, "c", i == 1, spec.status, spec.deleted); err != nil {
			t.Fatal(err)
		}
	}
	// The doubled figure the old writers left behind, and a stale non-zero
	// on a post whose comments are gone.
	if err := store.SetEngagementCount(ctx, withComments, "comment_count", 4); err != nil {
		t.Fatal(err)
	}
	if err := store.SetEngagementCount(ctx, emptied, "comment_count", 3); err != nil {
		t.Fatal(err)
	}
	corrected, err := store.RecountCommentCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if corrected < 2 {
		t.Fatalf("corrected %d rows, want at least the two seeded", corrected)
	}
	counts, err := store.GetCommentCounts(ctx, []uuid.UUID{withComments, emptied})
	if err != nil {
		t.Fatal(err)
	}
	if counts[withComments] != 2 || counts[emptied] != 0 {
		t.Fatalf("after recount: %v, want {withComments:2, emptied:0}", counts)
	}
	// A second recount changes nothing.
	if again, _ := store.RecountCommentCounts(ctx); again != 0 {
		t.Fatalf("a recount of a correct table corrected %d rows", again)
	}
}
