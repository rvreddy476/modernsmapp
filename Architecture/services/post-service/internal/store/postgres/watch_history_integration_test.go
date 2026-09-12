//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Watch history and the bookmarks type filter against a real Postgres
// (2026-09-12).
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/store/postgres/ -run 'WatchHistory|Bookmarks' -v
//
// Same rig as channel_subscriptions_integration_test.go (requireTestDSN,
// openSubscriptionDB, newUser): the real schema path, a database whose name
// ends in _test, every fixture cleaned up. These run against real SQL
// because the keyset predicate and the ANY() filter ARE the change.

// insertWatchPost seeds a minimal visible post for one author.
func insertWatchPost(t *testing.T, pool *pgxpool.Pool, author uuid.UUID, contentType string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO posts (id, author_id, text, visibility, content_type, created_at, updated_at)
		VALUES ($1, $2, 'history proof', 'public', $3, NOW(), NOW())`, id, author, contentType)
	if err != nil {
		t.Fatalf("insert post: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM posts WHERE id = $1`, id) })
	return id
}

// insertProgress writes a watch_progress row at an explicit last_watched_at
// so the order under test is the one we chose, not insertion order.
func insertProgress(t *testing.T, pool *pgxpool.Pool, user, post uuid.UUID, at time.Time, completed bool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO watch_progress (user_id, post_id, position_ms, duration_ms, percent_watched, completed, last_watched_at)
		VALUES ($1, $2, 1000, 10000, 10, $3, $4)`, user, post, completed, at)
	if err != nil {
		t.Fatalf("insert watch_progress: %v", err)
	}
}

type historyRig struct {
	pool   *pgxpool.Pool
	store  *postgres.Store
	viewer uuid.UUID
	author uuid.UUID
}

func newHistoryRig(t *testing.T) *historyRig {
	t.Helper()
	pool := openSubscriptionDB(t)
	// The media_assets stub that rig creates carries only the columns the
	// subscription tests touch; a post read attaches media through a LEFT
	// JOIN that also names media-service's alt columns. Idempotent, and a
	// stub either way: the real table belongs to media-service.
	if _, err := pool.Exec(context.Background(), `ALTER TABLE media_assets
		ADD COLUMN IF NOT EXISTS alt_text TEXT,
		ADD COLUMN IF NOT EXISTS alt_decorative BOOLEAN`); err != nil {
		t.Fatalf("media_assets alt columns: %v", err)
	}
	viewer, author := uuid.New(), uuid.New()
	newUser(t, pool, viewer)
	newUser(t, pool, author)
	// watch_progress cascades from users and posts.
	return &historyRig{pool: pool, store: postgres.New(pool), viewer: viewer, author: author}
}

func TestWatchHistoryIncludesCompletedRowsMostRecentFirst(t *testing.T) {
	r := newHistoryRig(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)

	oldest := insertWatchPost(t, r.pool, r.author, "long_video")
	finished := insertWatchPost(t, r.pool, r.author, "long_video")
	newest := insertWatchPost(t, r.pool, r.author, "long_video")
	insertProgress(t, r.pool, r.viewer, oldest, base, false)
	insertProgress(t, r.pool, r.viewer, finished, base.Add(time.Minute), true)
	insertProgress(t, r.pool, r.viewer, newest, base.Add(2*time.Minute), false)

	rows, next, err := r.store.GetWatchHistory(ctx, r.viewer, 20, "")
	if err != nil {
		t.Fatal(err)
	}
	if next != "" {
		t.Fatalf("three rows under a limit of 20 must be the last page; got next_cursor %q", next)
	}
	got := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		got = append(got, row.PostID)
	}
	want := []uuid.UUID{newest, finished, oldest}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("history order: got %v want %v (completed row kept, most recent first)", got, want)
	}
	if !rows[1].Completed {
		t.Fatal("the finished row lost its completed flag")
	}

	// continue-watching is unchanged: still only the unfinished rows.
	cw, err := r.store.GetContinueWatching(ctx, r.viewer, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(cw) != 2 {
		t.Fatalf("continue watching returned %d rows, want 2 (completed excluded)", len(cw))
	}
}

func TestWatchHistoryCursorPagesWithoutOverlapOrGaps(t *testing.T) {
	r := newHistoryRig(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)

	// Six rows; two of them share a last_watched_at so the post_id
	// tie-break is exercised, not just the timestamp.
	posts := make([]uuid.UUID, 6)
	for i := range posts {
		posts[i] = insertWatchPost(t, r.pool, r.author, "long_video")
		at := base.Add(time.Duration(i) * time.Minute)
		if i == 3 {
			at = base.Add(2 * time.Minute)
		}
		insertProgress(t, r.pool, r.viewer, posts[i], at, i%2 == 0)
	}

	seen := map[uuid.UUID]int{}
	var pages [][]uuid.UUID
	cursor := ""
	for page := 0; page < 3; page++ {
		rows, next, err := r.store.GetWatchHistory(ctx, r.viewer, 2, cursor)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if len(rows) != 2 {
			t.Fatalf("page %d: %d rows, want 2", page, len(rows))
		}
		ids := []uuid.UUID{rows[0].PostID, rows[1].PostID}
		for _, id := range ids {
			seen[id]++
		}
		pages = append(pages, ids)
		if page < 2 && next == "" {
			t.Fatalf("page %d: no next_cursor with rows still to come", page)
		}
		if page == 2 && next != "" {
			t.Fatalf("last page still carries a next_cursor %q", next)
		}
		cursor = next
	}
	if len(seen) != 6 {
		t.Fatalf("three pages of two covered %d distinct rows, want 6: %v", len(seen), pages)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("row %s appeared %d times across pages: %v", id, n, pages)
		}
	}

	// A cursor we did not issue is refused, not treated as page one.
	if _, _, err := r.store.GetWatchHistory(ctx, r.viewer, 2, "definitely-not-a-cursor"); err == nil {
		t.Fatal("malformed cursor accepted")
	}
}

func TestClearWatchHistoryRemovesOnlyTheViewersRows(t *testing.T) {
	r := newHistoryRig(t)
	ctx := context.Background()
	other := uuid.New()
	newUser(t, r.pool, other)
	now := time.Now().UTC()

	mine := []uuid.UUID{
		insertWatchPost(t, r.pool, r.author, "long_video"),
		insertWatchPost(t, r.pool, r.author, "long_video"),
	}
	shared := insertWatchPost(t, r.pool, r.author, "long_video")
	insertProgress(t, r.pool, r.viewer, mine[0], now, true)
	insertProgress(t, r.pool, r.viewer, mine[1], now.Add(time.Second), false)
	insertProgress(t, r.pool, r.viewer, shared, now.Add(2*time.Second), false)
	insertProgress(t, r.pool, other, shared, now, false)

	cleared, err := r.store.DeleteAllWatchProgress(ctx, r.viewer)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleared) != 3 {
		t.Fatalf("clear reported %d post ids, want 3 (the Redis mirror needs every key)", len(cleared))
	}

	var n int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM watch_progress WHERE user_id = $1`, r.viewer).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d of the viewer's rows survived the clear", n)
	}
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM watch_progress WHERE user_id = $1`, other).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("another viewer's row was touched: %d rows left, want 1", n)
	}
}

func TestBookmarksTypeFilterAndLimitCap(t *testing.T) {
	r := newHistoryRig(t)
	ctx := context.Background()
	st := r.store

	// 101 long videos and 2 flicks, all bookmarked by the viewer. saved_at
	// is set explicitly, one second apart: the bookmarks cursor is the
	// timestamp alone, so two rows saved in the same microsecond would be
	// a flake of the fixture, not a finding about the filter.
	base := time.Date(2026, 9, 12, 7, 0, 0, 0, time.UTC)
	bookmark := func(id uuid.UUID, i int) {
		t.Helper()
		_, err := r.pool.Exec(ctx, `
			INSERT INTO saved_items (id, user_id, target_type, target_id, collection_name, created_at)
			VALUES ($1, $2, 'post', $3, 'All Saved', $4)`, uuid.New(), r.viewer, id, base.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatalf("bookmark: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(), `DELETE FROM saved_items WHERE user_id = $1`, r.viewer)
	})
	longVideos := map[uuid.UUID]bool{}
	for i := 0; i < 101; i++ {
		id := insertWatchPost(t, r.pool, r.author, "long_video")
		longVideos[id] = true
		bookmark(id, i)
	}
	for i := 0; i < 2; i++ {
		bookmark(insertWatchPost(t, r.pool, r.author, "flick"), 101+i)
	}

	// type=long_video: only long videos, and limit 500 is clamped to 100
	// (so the 101st long video lands on page two, with a cursor to reach it).
	posts, next, err := st.GetBookmarks(ctx, r.viewer, []string{"long_video"}, 500, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 100 {
		t.Fatalf("limit 500 returned %d posts, want 100 (clamped)", len(posts))
	}
	if next == "" {
		t.Fatal("101 long videos under a cap of 100: expected a next_cursor")
	}
	for _, p := range posts {
		if p.ContentType != "long_video" || !longVideos[p.ID] {
			t.Fatalf("type=long_video returned %s (%s)", p.ID, p.ContentType)
		}
	}
	rest, next2, err := st.GetBookmarks(ctx, r.viewer, []string{"long_video"}, 500, next)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 1 || next2 != "" {
		t.Fatalf("page two: %d posts, next=%q; want the 1 remaining long video and no cursor", len(rest), next2)
	}

	// type=flick: the two flicks only.
	flicks, _, err := st.GetBookmarks(ctx, r.viewer, []string{"flick"}, 20, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(flicks) != 2 {
		t.Fatalf("type=flick returned %d posts, want 2", len(flicks))
	}

	// No filter: everything, still capped.
	all, _, err := st.GetBookmarks(ctx, r.viewer, nil, 500, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 100 {
		t.Fatalf("unfiltered limit 500 returned %d posts, want 100", len(all))
	}
}
