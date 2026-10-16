//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The likes backfill against real SQL: what it copies, what it keeps, and
// that a dry run leaves nothing behind.
func TestBackfillPostReactions(t *testing.T) {
	pool := openSubscriptionDB(t)
	ctx := context.Background()
	store := postgres.New(pool)

	author, viewer, other := uuid.New(), uuid.New(), uuid.New()
	live, gone := uuid.New(), uuid.New() // gone is never inserted
	for _, u := range []uuid.UUID{author, viewer, other} {
		if _, err := pool.Exec(ctx, `INSERT INTO users (id) VALUES ($1) ON CONFLICT DO NOTHING`, u); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO posts (id, author_id, text, visibility, content_type, created_at, updated_at) VALUES ($1, $2, 'backfill', 'public', 'flick', now(), now())`,
		live, author); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM reactions WHERE target_id IN ($1, $2)`, live, gone)
		pool.Exec(ctx, `DELETE FROM posts WHERE id = $1`, live)
		pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1, $2, $3)`, author, viewer, other)
	})

	// other already reacted after the deploy, with 'spark' at a recent time.
	recent := time.Now().UTC().Truncate(time.Millisecond)
	if _, err := pool.Exec(ctx,
		`INSERT INTO reactions (target_type, target_id, user_id, reaction_type, created_at) VALUES ('post', $1, $2, 'spark', $3)`,
		live, other, recent); err != nil {
		t.Fatal(err)
	}

	old := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	rows := []postgres.BackfillReaction{
		{PostID: live, UserID: viewer, Reaction: "like", CreatedAt: old}, // copied
		{PostID: live, UserID: other, Reaction: "like", CreatedAt: old},  // kept as spark
		{PostID: gone, UserID: viewer, Reaction: "like", CreatedAt: old}, // post missing
	}

	count := func() int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM reactions WHERE target_id IN ($1, $2)`, live, gone).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Dry run: same counts, nothing written.
	known, inserted, err := store.BackfillPostReactions(ctx, rows, false)
	if err != nil {
		t.Fatal(err)
	}
	if known != 2 || inserted != 1 {
		t.Fatalf("dry run: known=%d inserted=%d, want 2 and 1", known, inserted)
	}
	if n := count(); n != 1 {
		t.Fatalf("dry run wrote rows: %d reactions, want the 1 pre-existing", n)
	}

	// Applied.
	known, inserted, err = store.BackfillPostReactions(ctx, rows, true)
	if err != nil {
		t.Fatal(err)
	}
	if known != 2 || inserted != 1 {
		t.Fatalf("apply: known=%d inserted=%d, want 2 and 1", known, inserted)
	}
	var typ string
	var at time.Time
	if err := pool.QueryRow(ctx, `SELECT reaction_type, created_at FROM reactions WHERE target_id = $1 AND user_id = $2`, live, viewer).Scan(&typ, &at); err != nil {
		t.Fatal(err)
	}
	if typ != "like" || !at.Equal(old) {
		t.Fatalf("copied row = %s at %v, want like at %v (the Scylla time)", typ, at, old)
	}
	if err := pool.QueryRow(ctx, `SELECT reaction_type, created_at FROM reactions WHERE target_id = $1 AND user_id = $2`, live, other).Scan(&typ, &at); err != nil {
		t.Fatal(err)
	}
	if typ != "spark" || !at.Equal(recent) {
		t.Fatalf("existing row changed to %s at %v; the Postgres row must win", typ, at)
	}
	if n := count(); n != 2 {
		t.Fatalf("%d reactions, want 2 (nothing for the missing post)", n)
	}

	// The copied like is now in the viewer's liked list.
	liked, err := store.ListLikedShortFormPosts(ctx, viewer, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(liked) != 1 || liked[0].PostID != live {
		t.Fatalf("liked list = %+v, want the backfilled post", liked)
	}

	// Running again changes nothing.
	if _, inserted, err = store.BackfillPostReactions(ctx, rows, true); err != nil || inserted != 0 {
		t.Fatalf("second run inserted %d (err %v), want 0", inserted, err)
	}
}
