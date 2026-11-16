//go:build integration

package store

import (
	"bytes"
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Live-Postgres checks for ListIDPage (store/id_pages.go). Run with
// -tags integration and POSTGRES_DSN set (CI only).

func followAt(t *testing.T, pool *pgxpool.Pool, follower, followee uuid.UUID, at time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO follows (follower_id, followee_id, created_at) VALUES ($1, $2, $3)`, follower, followee, at); err != nil {
		t.Fatalf("follow: %v", err)
	}
}

// walkIDPages reads a list to its end, calling between(page) after each
// page, and fails on a cursor that does not advance.
func walkIDPages(t *testing.T, s *Store, kind string, owner uuid.UUID, limit int, between func(page int)) []uuid.UUID {
	t.Helper()
	var all []uuid.UUID
	var after *ListCursor
	for page := 0; page < 1000; page++ {
		ids, next, err := s.ListIDPage(context.Background(), kind, owner, limit, after)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		all = append(all, ids...)
		if next == "" {
			return all
		}
		if len(ids) != limit {
			t.Fatalf("page %d holds %d ids but names a next page; want a full page of %d", page, len(ids), limit)
		}
		cur, err := ParseListCursor(next)
		if err != nil {
			t.Fatalf("page %d issued a cursor it cannot read: %q", page, next)
		}
		after = &cur
		if between != nil {
			between(page)
		}
	}
	t.Fatal("the walk never ended")
	return nil
}

func TestListIDPageLive_FollowersNewestFirstExactlyOnce(t *testing.T) {
	pool := followAudiencePool(t)
	s := New(pool)
	owner := uuid.New()
	base := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)

	// 25 followers; five of them share one created_at (a bulk import), so
	// the id tie-break is what keeps the walk exact across a page boundary.
	var want []uuid.UUID // newest first
	type row struct {
		id uuid.UUID
		at time.Time
	}
	var rows []row
	for i := 0; i < 25; i++ {
		at := base.Add(time.Duration(i) * time.Second)
		if i >= 8 && i < 13 {
			at = base.Add(8 * time.Second)
		}
		rows = append(rows, row{uuid.New(), at})
	}
	for _, r := range rows {
		followAt(t, pool, r.id, owner, r.at)
	}
	hidden := uuid.New()
	followAt(t, pool, hidden, owner, base.Add(30*time.Second))
	hide(t, pool, hidden)

	// Postgres orders uuid bytewise, as bytes.Compare does.
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].at.Equal(rows[j].at) {
			return rows[i].at.After(rows[j].at)
		}
		return bytes.Compare(rows[i].id[:], rows[j].id[:]) > 0
	})
	for _, r := range rows {
		want = append(want, r.id)
	}

	got := walkIDPages(t, s, ListFollowers, owner, 10, nil)
	if len(got) != len(want) {
		t.Fatalf("walk returned %d ids, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("position %d = %s, want %s (created_at DESC, id DESC)", i, got[i], want[i])
		}
	}
}

func TestListIDPageLive_ChangesMidWalkNeitherRepeatNorSkip(t *testing.T) {
	pool := followAudiencePool(t)
	s := New(pool)
	owner := uuid.New()
	base := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	var original []uuid.UUID
	for i := 0; i < 30; i++ {
		id := uuid.New()
		original = append(original, id)
		followAt(t, pool, id, owner, base.Add(time.Duration(i)*time.Second))
	}
	// original[29] is the newest. After page 0 (the ten newest): a new
	// follower arrives, and one already-returned follower leaves. Under
	// OFFSET the first repeats a row and the second skips one.
	newcomer := uuid.New()
	got := walkIDPages(t, s, ListFollowers, owner, 10, func(page int) {
		if page != 0 {
			return
		}
		followAt(t, pool, newcomer, owner, time.Now().UTC())
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM follows WHERE follower_id = $1 AND followee_id = $2`, original[29], owner); err != nil {
			t.Fatal(err)
		}
	})
	seen := map[uuid.UUID]int{}
	for _, id := range got {
		seen[id]++
	}
	for i, id := range original {
		if seen[id] != 1 {
			t.Fatalf("original follower %d was returned %d time(s), want exactly once", i, seen[id])
		}
	}
	if seen[newcomer] != 0 {
		t.Fatal("a follower who arrived at the head mid-walk appeared in a later page")
	}
}

func TestListIDPageLive_FollowingAndConnections(t *testing.T) {
	pool := followAudiencePool(t)
	s := New(pool)
	owner := uuid.New()
	base := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)

	var followees, conns []uuid.UUID // oldest first
	for i := 0; i < 7; i++ {
		id := uuid.New()
		followees = append(followees, id)
		followAt(t, pool, owner, id, base.Add(time.Duration(i)*time.Second))
	}
	for i := 0; i < 7; i++ {
		other := uuid.New()
		conns = append(conns, other)
		a, b := owner, other
		if b.String() < a.String() { // normalised: user_a < user_b, so the owner is on either side
			a, b = b, a
		}
		if _, err := pool.Exec(context.Background(),
			`INSERT INTO connections (user_a, user_b, created_at) VALUES ($1, $2, $3)`, a, b, base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	for kind, want := range map[string][]uuid.UUID{ListFollowing: followees, ListConnections: conns} {
		got := walkIDPages(t, s, kind, owner, 3, nil)
		if len(got) != len(want) {
			t.Fatalf("%s: walk returned %d ids, want %d", kind, len(got), len(want))
		}
		for i := range got {
			if got[i] != want[len(want)-1-i] {
				t.Fatalf("%s: position %d out of order (want newest first)", kind, i)
			}
		}
	}
	if _, _, err := s.ListIDPage(context.Background(), "strangers", owner, 3, nil); err == nil {
		t.Fatal("an unknown list kind was served")
	}
}
