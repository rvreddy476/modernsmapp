//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/post-service/internal/store/postgres"
)

/*
Profile sort (T-21) and hashtag total (T-34) against a real Postgres
(*_test only). Every sort is paged to the end at several page sizes and must
return each row exactly once, in the documented order — including rows that
share a created_at and rows that share a like count, and across a pinned
row ending a page (which the old created_at-only cursor got wrong).

Run:
  POSTGRES_DSN=…/post_it_test go test -tags integration ./internal/store/postgres/ -run 'AuthorSort|HashtagTotal' -v
*/

type sortRow struct {
	id      uuid.UUID
	created time.Time
	likes   int
	pinned  bool
}

func uuidLess(a, b uuid.UUID) bool { return bytes.Compare(a[:], b[:]) < 0 }

func TestAuthorSortPagesEveryRowOnceInOrder(t *testing.T) {
	pool := openCommentCountDB(t)
	store := postgres.New(pool)
	ctx := context.Background()
	author := uuid.New()
	t0 := time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC)
	rows := []sortRow{
		{uuid.New(), t0, 5, false},
		{uuid.New(), t0.Add(time.Second), 9, true}, // pinned, shares a timestamp
		{uuid.New(), t0.Add(time.Second), 5, false},
		{uuid.New(), t0.Add(2 * time.Second), 0, false},
		{uuid.New(), t0.Add(3 * time.Second), 9, false},
		{uuid.New(), t0.Add(3 * time.Second), 9, false},
	}
	t.Cleanup(func() {
		bg := context.Background()
		pool.Exec(bg, `DELETE FROM post_engagement_counts WHERE post_id IN (SELECT id FROM posts WHERE author_id = $1)`, author)
		pool.Exec(bg, `DELETE FROM posts WHERE author_id = $1`, author)
	})
	for _, r := range rows {
		if _, err := pool.Exec(ctx, `INSERT INTO posts (id, author_id, text, visibility, content_type, is_pinned, created_at, updated_at)
			VALUES ($1, $2, 'sort me', 'public', 'post', $3, $4, $4)`, r.id, author, r.pinned, r.created); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO post_engagement_counts (post_id, like_count) VALUES ($1, $2)
			ON CONFLICT (post_id) DO UPDATE SET like_count = EXCLUDED.like_count`, r.id, r.likes); err != nil {
			t.Fatal(err)
		}
	}
	// Excluded from every sort: deleted and scheduled.
	gone, scheduled := uuid.New(), uuid.New()
	for _, q := range []struct {
		id  uuid.UUID
		sql string
	}{
		{gone, `INSERT INTO posts (id, author_id, text, visibility, content_type, created_at, updated_at, deleted_at) VALUES ($1, $2, 'x', 'public', 'post', now(), now(), now())`},
		{scheduled, `INSERT INTO posts (id, author_id, text, visibility, content_type, created_at, updated_at, publish_at) VALUES ($1, $2, 'x', 'public', 'post', now(), now(), now() + interval '1 day')`},
	} {
		if _, err := pool.Exec(ctx, q.sql, q.id, author); err != nil {
			t.Fatal(err)
		}
	}

	want := map[postgres.AuthorPostSort][]uuid.UUID{}
	ordered := func(less func(a, b sortRow) bool) []uuid.UUID {
		cp := append([]sortRow(nil), rows...)
		sort.Slice(cp, func(i, j int) bool { return less(cp[i], cp[j]) })
		out := make([]uuid.UUID, len(cp))
		for i, r := range cp {
			out[i] = r.id
		}
		return out
	}
	want[postgres.AuthorSortLatest] = ordered(func(a, b sortRow) bool {
		if a.pinned != b.pinned {
			return a.pinned
		}
		if !a.created.Equal(b.created) {
			return a.created.After(b.created)
		}
		return uuidLess(b.id, a.id)
	})
	want[postgres.AuthorSortOldest] = ordered(func(a, b sortRow) bool {
		if !a.created.Equal(b.created) {
			return a.created.Before(b.created)
		}
		return uuidLess(a.id, b.id)
	})
	want[postgres.AuthorSortPopular] = ordered(func(a, b sortRow) bool {
		if a.likes != b.likes {
			return a.likes > b.likes
		}
		return uuidLess(b.id, a.id)
	})
	if want[postgres.AuthorSortLatest][0] != rows[1].id {
		t.Fatalf("fixture: the pinned row must lead latest")
	}

	for _, s := range postgres.AuthorPostSorts {
		for _, limit := range []int{1, 2, 4, 50} {
			var got []uuid.UUID
			cursor := ""
			for pages := 0; ; pages++ {
				if pages > len(rows)+1 {
					t.Fatalf("%s limit %d: cursor never ran out", s, limit)
				}
				page, next, err := store.GetPostsByAuthorSorted(ctx, author, "", s, limit, cursor, true)
				if err != nil {
					t.Fatalf("%s limit %d: %v", s, limit, err)
				}
				for _, p := range page {
					got = append(got, p.ID)
				}
				if next == "" {
					break
				}
				// Re-reading the same page with the same cursor is the same page.
				again, nextAgain, err := store.GetPostsByAuthorSorted(ctx, author, "", s, limit, cursor, true)
				if err != nil || nextAgain != next || len(again) != len(page) || again[0].ID != page[0].ID {
					t.Fatalf("%s limit %d: page not stable on re-read (%v)", s, limit, err)
				}
				cursor = next
			}
			if len(got) != len(want[s]) {
				t.Fatalf("%s limit %d: got %d rows %v, want %v", s, limit, len(got), got, want[s])
			}
			for i := range got {
				if got[i] != want[s][i] {
					t.Fatalf("%s limit %d: order\n got %v\nwant %v", s, limit, got, want[s])
				}
			}
		}
	}

	// The default wrapper is latest.
	def, _, err := store.GetPostsByAuthor(ctx, author, "", 50, "", true)
	if err != nil || len(def) != len(rows) || def[0].ID != rows[1].id {
		t.Fatalf("GetPostsByAuthor default = %d rows (%v), want latest with the pinned row first", len(def), err)
	}

	// A pre-T-21 cursor (bare created_at) still pages latest.
	legacy, _, err := store.GetPostsByAuthorSorted(ctx, author, "", postgres.AuthorSortLatest, 50, t0.Add(time.Second).Format(time.RFC3339Nano), true)
	if err != nil || len(legacy) != 1 || legacy[0].ID != rows[0].id {
		t.Fatalf("legacy cursor: %d rows (%v), want only the row older than it", len(legacy), err)
	}

	// A cursor from one sort is refused by another.
	_, popNext, err := store.GetPostsByAuthorSorted(ctx, author, "", postgres.AuthorSortPopular, 1, "", true)
	if err != nil || popNext == "" {
		t.Fatalf("popular page 1: %v", err)
	}
	if _, _, err := store.GetPostsByAuthorSorted(ctx, author, "", postgres.AuthorSortOldest, 1, popNext, true); !errors.Is(err, postgres.ErrInvalidAuthorCursor) {
		t.Fatalf("popular cursor on oldest: %v, want ErrInvalidAuthorCursor", err)
	}
}

func TestHashtagTotalMatchesTheListedPosts(t *testing.T) {
	pool := openCommentCountDB(t)
	store := postgres.New(pool)
	ctx := context.Background()
	author, other := uuid.New(), uuid.New()
	tag := "t34" + uuid.NewString()[:8]
	t.Cleanup(func() {
		bg := context.Background()
		for _, a := range []uuid.UUID{author, other} {
			pool.Exec(bg, `DELETE FROM post_engagement_counts WHERE post_id IN (SELECT id FROM posts WHERE author_id = $1)`, a)
			pool.Exec(bg, `DELETE FROM posts WHERE author_id = $1`, a)
		}
	})
	insert := func(by uuid.UUID, contentType, visibility, extra string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO posts (id, author_id, text, visibility, content_type, hashtags, created_at, updated_at`+extraCols(extra)+`)
			VALUES ($1, $2, 'tagged', $3, $4, ARRAY[$5, 'other']::text[], now(), now()`+extraVals(extra)+`)`,
			uuid.New(), by, visibility, contentType, tag); err != nil {
			t.Fatal(err)
		}
	}
	insert(author, "flick", "public", "")
	insert(author, "flick", "public", "")
	insert(author, "post", "public", "")
	insert(author, "post", "followers", "")     // not on the public tag page
	insert(author, "post", "public", "deleted") // gone
	insert(author, "post", "public", "scheduled")
	insert(author, "post", "public", "flagged") // not approved: neither listed nor counted
	insert(author, "post", "public", "pending")
	insert(other, "post", "public", "")

	// The top sort reads two counters cmd/server adds at boot (ensureSchema).
	for _, ddl := range []string{
		`ALTER TABLE post_engagement_counts ADD COLUMN IF NOT EXISTS views_count BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE post_engagement_counts ADD COLUMN IF NOT EXISTS reports_count INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	sum := func(byAuthor map[uuid.UUID]int) int {
		n := 0
		for _, c := range byAuthor {
			n += c
		}
		return n
	}
	byAuthor, truncated, err := store.CountPostsByHashtag(ctx, tag, nil, nil)
	if err != nil || sum(byAuthor) != 4 || truncated || byAuthor[author] != 3 || byAuthor[other] != 1 {
		t.Fatalf("counts = %v truncated=%v (%v), want author 3 + other 1", byAuthor, truncated, err)
	}
	for _, sort := range []postgres.HashtagSortMode{postgres.HashtagSortRecent, postgres.HashtagSortTop} {
		listed, _, err := store.GetPostsByHashtag(ctx, tag, 50, "", sort, nil)
		if err != nil || len(listed) != sum(byAuthor) {
			t.Fatalf("%s: the list has %d posts (%v); the total must match it (%d)", sort, len(listed), err, sum(byAuthor))
		}
		for _, p := range listed {
			if p.ReviewStatus != "approved" || p.Visibility != "public" {
				t.Fatalf("%s listed a %s/%s post", sort, p.ReviewStatus, p.Visibility)
			}
		}
	}
	flicks, _, err := store.CountPostsByHashtag(ctx, tag, []string{"flick"}, nil)
	if err != nil || sum(flicks) != 2 {
		t.Fatalf("flick total = %v (%v), want 2", flicks, err)
	}
	// Excluding a denied author is how the service narrows a full scan.
	rest, _, err := store.CountPostsByHashtag(ctx, tag, nil, []uuid.UUID{author})
	if err != nil || sum(rest) != 1 || rest[other] != 1 {
		t.Fatalf("excluding the author = %v (%v), want other's 1", rest, err)
	}
	if none, _, err := store.CountPostsByHashtag(ctx, tag+"x", nil, nil); err != nil || len(none) != 0 {
		t.Fatalf("unknown tag counts = %v (%v), want none", none, err)
	}
}

func extraCols(kind string) string {
	switch kind {
	case "deleted":
		return ", deleted_at"
	case "scheduled":
		return ", publish_at"
	case "flagged", "pending":
		return ", review_status"
	}
	return ""
}

func extraVals(kind string) string {
	switch kind {
	case "deleted":
		return ", now()"
	case "scheduled":
		return ", now() + interval '1 day'"
	case "flagged":
		return ", 'flagged'"
	case "pending":
		return ", 'pending'"
	}
	return ""
}
