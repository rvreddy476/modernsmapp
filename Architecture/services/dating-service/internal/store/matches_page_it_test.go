// Match list paging against Postgres (CI only, on a *_test database):
// every row once across pages, ties on the sort time broken by id, the
// bucket filters still applied, and the unpaged call unchanged.
package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMatchesPageIT_KeysetWalksEveryRowOnceInOrder(t *testing.T) {
	s, cleanup := statusITStore(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	viewer := uuid.New()
	ensureProfileForTest(t, s, viewer)
	now := time.Now().UTC().Truncate(time.Microsecond)
	tie := now.Add(-time.Hour)
	older := now.Add(-3 * time.Hour)

	seed := func(status string, matchedAt time.Time, last *time.Time) uuid.UUID {
		other := uuid.New()
		ensureProfileForTest(t, s, other)
		return seedMatchRow(t, s, viewer, other, status, true, matchedAt, last)
	}
	// Five active matches: two share one sort time exactly (the id breaks
	// the tie), one sorts by its last message.
	m1 := seed("conversing", now.Add(-10*time.Hour), &now)
	m2 := seed("matched", tie, nil)
	m3 := seed("matched", tie, nil)
	m4 := seed("conversing", now.Add(-2*time.Hour), nil)
	m5 := seed("matched", older, nil)
	// Not in the active bucket.
	q1 := seed("quiet", now.Add(-30*time.Minute), nil)
	seed("closed", now, nil)

	hi, lo := m2, m3
	if hi.String() < lo.String() {
		hi, lo = lo, hi
	}
	want := []uuid.UUID{m1, hi, lo, m4, m5}

	var got []uuid.UUID
	var after *MatchCursor
	pages := 0
	for {
		page, next, err := s.ListMatchesPageForUser(ctx, viewer, "active", 2, after)
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		pages++
		if len(page) > 2 {
			t.Fatalf("page %d has %d rows, limit 2", pages, len(page))
		}
		for _, m := range page {
			got = append(got, m.ID)
		}
		if next == nil {
			break
		}
		// The cursor is the wire string, as a client would send it back.
		decoded, err := DecodeMatchCursor(next.Encode())
		if err != nil {
			t.Fatalf("cursor: %v", err)
		}
		after = decoded
		if pages > 5 {
			t.Fatalf("paging did not end")
		}
	}
	if pages != 3 {
		t.Fatalf("pages = %d, want 3", pages)
	}
	if len(got) != len(want) {
		t.Fatalf("walked %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %v, want %v (walk %v)", i, got[i], want[i], want)
		}
	}

	quiet, next, err := s.ListMatchesPageForUser(ctx, viewer, "quiet", 2, nil)
	if err != nil || next != nil || len(quiet) != 1 || quiet[0].ID != q1 {
		t.Fatalf("quiet = %v / %+v / %v, want only %v and no cursor", quiet, next, err, q1)
	}

	// The unpaged call is the same order, all at once.
	all, err := s.ListMatchesForUser(ctx, viewer, "active")
	if err != nil {
		t.Fatalf("unpaged: %v", err)
	}
	if len(all) != len(want) {
		t.Fatalf("unpaged = %d rows, want %d", len(all), len(want))
	}
	for i := range want {
		if all[i].ID != want[i] {
			t.Fatalf("unpaged row %d = %v, want %v", i, all[i].ID, want[i])
		}
	}
}
