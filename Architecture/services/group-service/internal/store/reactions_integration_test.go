//go:build integration

package store

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/group-service/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

/*
Reactions against a real Postgres (a *_test database only).

What these prove, in the order the brief asks: a change of reaction replaces
the row and moves no counter; the same reaction again changes nothing;
removal is safe to repeat; concurrent first reactions produce one row and one
count; the legacy heart and the new reaction are the same row; and every
client read surface carries viewer_reaction and reaction_counts.

Run: GROUP_POSTGRES_DSN=…/group_it_test go test -tags integration ./internal/store/ -run Reaction -p 1
*/

type reactionFixture struct {
	pool    *pgxpool.Pool
	store   *Store
	groupID uuid.UUID
	viewerA uuid.UUID
	viewerB uuid.UUID
	postID  uuid.UUID
}

func newReactionFixture(t *testing.T) *reactionFixture {
	t.Helper()
	dsn := os.Getenv("GROUP_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("GROUP_POSTGRES_DSN is required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run against database %q: integration tests only run against a *_test database", cfg.ConnConfig.Database)
	}
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, database.SetupSQL); err != nil {
		t.Fatal(err)
	}
	// Columns the queries reference that an older scratch schema lacks (the
	// full migration chain cannot run here: 002 alters post-service's posts).
	if _, err := pool.Exec(ctx, `
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS privacy_level TEXT NOT NULL DEFAULT 'public';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
		ALTER TABLE group_members ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
		ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS status VARCHAR(20) DEFAULT 'published';
	`); err != nil {
		t.Fatal(err)
	}
	// The real migration, so the test exercises the DDL that dev will boot.
	m016, err := database.Migrations.ReadFile("migrations/016_group_post_reactions.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(m016)); err != nil {
		t.Fatalf("migration 016: %v", err)
	}

	f := &reactionFixture{
		pool:    pool,
		store:   New(pool),
		groupID: uuid.New(),
		viewerA: uuid.New(),
		viewerB: uuid.New(),
		postID:  uuid.New(),
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO groups (id, name, description, creator_id, visibility) VALUES ($1, $2, '', $3, 'public')`,
		f.groupID, "reactions-"+f.groupID.String()[:8], f.viewerA); err != nil {
		t.Fatal(err)
	}
	for _, m := range []uuid.UUID{f.viewerA, f.viewerB} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, 'member')`,
			f.groupID, m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO group_posts (id, group_id, author_id, content_type, title, body, status)
		 VALUES ($1, $2, $3, 'text', 'reaction fixture', 'a post people react to', 'published')`,
		f.postID, f.groupID, f.viewerA.String()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM group_post_sparks WHERE post_id IN (SELECT id FROM group_posts WHERE group_id = $1)`, f.groupID)
		pool.Exec(ctx, `DELETE FROM group_posts WHERE group_id = $1`, f.groupID)
		pool.Exec(ctx, `DELETE FROM group_members WHERE group_id = $1`, f.groupID)
		pool.Exec(ctx, `DELETE FROM groups WHERE id = $1`, f.groupID)
	})
	return f
}

func (f *reactionFixture) sparkCount(t *testing.T) int {
	t.Helper()
	n, err := f.store.GetGroupPostSparkCount(context.Background(), f.postID)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *reactionFixture) counts(t *testing.T) map[string]int {
	t.Helper()
	m, err := f.store.GetGroupPostReactionCounts(context.Background(), []uuid.UUID{f.postID})
	if err != nil {
		t.Fatal(err)
	}
	if m[f.postID] == nil {
		return map[string]int{}
	}
	return m[f.postID]
}

func (f *reactionFixture) rows(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM group_post_sparks WHERE post_id = $1`, f.postID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestReactionReplaceIdempotentRemove(t *testing.T) {
	f := newReactionFixture(t)
	ctx := context.Background()
	a := f.viewerA.String()

	ch, err := f.store.SetGroupPostReaction(ctx, f.postID, a, "like")
	if err != nil || !ch.Inserted || ch.Current != "like" {
		t.Fatalf("first like: %+v %v", ch, err)
	}
	if f.sparkCount(t) != 1 {
		t.Fatalf("spark_count after first like = %d, want 1", f.sparkCount(t))
	}

	// Replace: love in, like out, counter untouched.
	ch, err = f.store.SetGroupPostReaction(ctx, f.postID, a, "love")
	if err != nil || ch.Inserted || !ch.Replaced || ch.Previous != "like" || ch.Current != "love" {
		t.Fatalf("replace: %+v %v", ch, err)
	}
	if n := f.sparkCount(t); n != 1 {
		t.Fatalf("spark_count after replace = %d, want 1 (a replace must not double-count)", n)
	}
	if c := f.counts(t); c["love"] != 1 || c["like"] != 0 || len(c) != 1 {
		t.Fatalf("counts after replace = %v, want {love:1}", c)
	}

	// Same reaction again: no-op.
	ch, err = f.store.SetGroupPostReaction(ctx, f.postID, a, "love")
	if err != nil || ch.Inserted || ch.Replaced || ch.Previous != "love" {
		t.Fatalf("idempotent retry: %+v %v", ch, err)
	}
	if n := f.sparkCount(t); n != 1 {
		t.Fatalf("spark_count after retry = %d, want 1", n)
	}

	// Remove, then remove again.
	removed, prev, err := f.store.RemoveGroupPostReaction(ctx, f.postID, a)
	if err != nil || !removed || prev != "love" {
		t.Fatalf("remove: removed=%v prev=%q err=%v", removed, prev, err)
	}
	if n := f.sparkCount(t); n != 0 {
		t.Fatalf("spark_count after remove = %d, want 0", n)
	}
	removed, _, err = f.store.RemoveGroupPostReaction(ctx, f.postID, a)
	if err != nil || removed {
		t.Fatalf("second remove: removed=%v err=%v — a retry must be a safe no-op", removed, err)
	}
	if n := f.sparkCount(t); n != 0 {
		t.Fatalf("spark_count after second remove = %d, want 0 (never below zero, never moved twice)", n)
	}
	if len(f.counts(t)) != 0 {
		t.Fatalf("counts after remove = %v, want {}", f.counts(t))
	}
}

// Many concurrent first reactions, then many concurrent replacements, from
// the same viewer: one row, one count, every call succeeds.
func TestReactionConcurrentRetriesCountOnce(t *testing.T) {
	f := newReactionFixture(t)
	ctx := context.Background()
	a := f.viewerA.String()

	run := func(reactions []string) (inserted int) {
		var mu sync.Mutex
		var wg sync.WaitGroup
		errs := make(chan error, len(reactions))
		for _, r := range reactions {
			wg.Add(1)
			go func(r string) {
				defer wg.Done()
				ch, err := f.store.SetGroupPostReaction(ctx, f.postID, a, r)
				if err != nil {
					errs <- err
					return
				}
				if ch.Inserted {
					mu.Lock()
					inserted++
					mu.Unlock()
				}
			}(r)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("concurrent set: %v", err)
		}
		return inserted
	}

	same := make([]string, 8)
	for i := range same {
		same[i] = "like"
	}
	if got := run(same); got != 1 {
		t.Fatalf("%d calls reported Inserted for 8 concurrent first likes, want exactly 1", got)
	}
	if n, r := f.sparkCount(t), f.rows(t); n != 1 || r != 1 {
		t.Fatalf("after concurrent first likes: spark_count=%d rows=%d, want 1/1", n, r)
	}

	mixed := []string{"love", "smile", "like", "wow", "love", "sad", "angry", "smile"}
	if got := run(mixed); got != 0 {
		t.Fatalf("%d calls reported Inserted for concurrent replacements, want 0", got)
	}
	if n, r := f.sparkCount(t), f.rows(t); n != 1 || r != 1 {
		t.Fatalf("after concurrent replacements: spark_count=%d rows=%d, want 1/1", n, r)
	}
	c := f.counts(t)
	total := 0
	for _, v := range c {
		total += v
	}
	if total != 1 {
		t.Fatalf("counts after concurrent replacements sum to %d (%v), want 1", total, c)
	}
}

// Legacy heart ⇄ reaction: the same row, the same count.
func TestLegacySparkAndReactionShareOneRow(t *testing.T) {
	f := newReactionFixture(t)
	ctx := context.Background()
	a := f.viewerA.String()

	if err := f.store.SparkGroupPost(ctx, f.postID, a, false); err != nil {
		t.Fatal(err)
	}
	if c := f.counts(t); c["like"] != 1 {
		t.Fatalf("a legacy heart must read as like; counts=%v", c)
	}
	if mine, _ := f.store.GetViewerReaction(ctx, f.postID, a); mine != "like" {
		t.Fatalf("viewer reaction after legacy spark = %q, want like", mine)
	}
	// Old client hearts twice: still 409, still one row.
	if err := f.store.SparkGroupPost(ctx, f.postID, a, false); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("second legacy spark: err=%v, want \"already sparked\"", err)
	}
	// New client changes it to love: count unchanged.
	if _, err := f.store.SetGroupPostReaction(ctx, f.postID, a, "love"); err != nil {
		t.Fatal(err)
	}
	if n := f.sparkCount(t); n != 1 {
		t.Fatalf("spark_count after heart→love = %d, want 1", n)
	}
	// Old client still cannot heart on top of a love.
	if err := f.store.SparkGroupPost(ctx, f.postID, a, false); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("legacy spark over love: err=%v, want \"already sparked\"", err)
	}
	// Old client unsparks: the love goes, the count goes to 0.
	if err := f.store.UnsparkGroupPost(ctx, f.postID, a); err != nil {
		t.Fatal(err)
	}
	if n, r := f.sparkCount(t), f.rows(t); n != 0 || r != 0 {
		t.Fatalf("after legacy unspark: spark_count=%d rows=%d, want 0/0", n, r)
	}

	// A supernova weighs 5 in the legacy counter and 1 person in the counts;
	// removing it through the reaction route takes its weight back.
	if err := f.store.SparkGroupPost(ctx, f.postID, a, true); err != nil {
		t.Fatal(err)
	}
	if n, c := f.sparkCount(t), f.counts(t); n != 5 || c["like"] != 1 {
		t.Fatalf("supernova: spark_count=%d counts=%v, want 5 / {like:1}", n, c)
	}
	if _, _, err := f.store.RemoveGroupPostReaction(ctx, f.postID, a); err != nil {
		t.Fatal(err)
	}
	if n := f.sparkCount(t); n != 0 {
		t.Fatalf("spark_count after removing a supernova = %d, want 0", n)
	}
}

// Every client read surface carries the viewer's reaction and the counts.
func TestReactionsOnEveryReadSurface(t *testing.T) {
	f := newReactionFixture(t)
	ctx := context.Background()
	a, b := f.viewerA.String(), f.viewerB.String()
	if _, err := f.store.SetGroupPostReaction(ctx, f.postID, a, "love"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SetGroupPostReaction(ctx, f.postID, b, "like"); err != nil {
		t.Fatal(err)
	}
	wantCounts := map[string]int{"love": 1, "like": 1}

	check := func(surface string, p *GroupPostV2, wantMine string) {
		t.Helper()
		if p == nil {
			t.Fatalf("%s: post missing", surface)
		}
		if wantMine == "" {
			if p.ViewerReaction != nil {
				t.Errorf("%s: viewer_reaction = %q, want null", surface, *p.ViewerReaction)
			}
		} else if p.ViewerReaction == nil || *p.ViewerReaction != wantMine {
			t.Errorf("%s: viewer_reaction = %v, want %q", surface, p.ViewerReaction, wantMine)
		}
		if p.ReactionCounts == nil || p.ReactionCounts["love"] != wantCounts["love"] || p.ReactionCounts["like"] != wantCounts["like"] {
			t.Errorf("%s: reaction_counts = %v, want %v", surface, p.ReactionCounts, wantCounts)
		}
		if !p.ViewerSparked && wantMine != "" {
			t.Errorf("%s: viewer_sparked false although the viewer reacted — legacy clients read this flag", surface)
		}
	}

	p, err := f.store.GetGroupPostV2ForViewer(ctx, f.postID, a)
	if err != nil {
		t.Fatal(err)
	}
	check("GetGroupPostV2ForViewer(A)", p, "love")

	posts, err := f.store.ListGroupPostsV2(ctx, f.groupID, nil, b, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	check("ListGroupPostsV2(B)", findPost(posts, f.postID), "like")

	posts, err = f.store.SearchGroupPostsV2(ctx, f.groupID, "react", a, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	check("SearchGroupPostsV2(A)", findPost(posts, f.postID), "love")

	posts, err = f.store.ListMyGroupsFeed(ctx, f.viewerB, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	check("ListMyGroupsFeed(B)", findPost(posts, f.postID), "like")

	// Anonymous viewer: counts yes, viewer_reaction null.
	p, err = f.store.GetGroupPostV2ForViewer(ctx, f.postID, "")
	if err != nil {
		t.Fatal(err)
	}
	check("GetGroupPostV2ForViewer(anon)", p, "")

	// Pending queue: a pending post carries {} and the moderator sees counts
	// through the same field.
	pending := uuid.New()
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO group_posts (id, group_id, author_id, content_type, body, status)
		 VALUES ($1, $2, $3, 'text', 'awaiting approval', 'pending_approval')`,
		pending, f.groupID, b); err != nil {
		t.Fatal(err)
	}
	posts, err = f.store.ListPendingGroupPostsV2(ctx, f.groupID, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	pp := findPost(posts, pending)
	if pp == nil || pp.ReactionCounts == nil || len(pp.ReactionCounts) != 0 {
		t.Fatalf("ListPendingGroupPostsV2: reaction_counts = %v, want an empty non-nil map", pp)
	}
}

// The CHECK constraint is the last line of defence when Go is bypassed.
func TestReactionCheckConstraintRefusesUnknownEmoji(t *testing.T) {
	f := newReactionFixture(t)
	_, err := f.pool.Exec(context.Background(),
		`INSERT INTO group_post_sparks (post_id, user_id, reaction) VALUES ($1, $2, 'heart')`,
		f.postID, f.viewerA.String())
	if err == nil || !strings.Contains(err.Error(), "group_post_sparks_reaction_check") {
		t.Fatalf("raw insert of 'heart': err=%v, want the CHECK constraint to refuse it", err)
	}
	if f.rows(t) != 0 {
		t.Fatal("a refused insert left a row")
	}
}
