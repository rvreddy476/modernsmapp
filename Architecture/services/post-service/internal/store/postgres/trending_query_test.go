package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// /v1/posts/trending (2026-11-07): candidates are the week before a fixed
// as-of time, and every page of one scroll is scored against that time.

func TestTrendingQuery_FirstPageIsWindowedAndScoredAgainstAFixedTime(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	q, args, asOf := trendingQuery([]string{"flick"}, 20, "", now)
	if !asOf.Equal(now) {
		t.Fatalf("first page as-of = %v, want now %v", asOf, now)
	}
	if strings.Contains(q, "NOW()") {
		t.Fatal("trending SQL still reads NOW(): the score moves between pages")
	}
	for _, want := range []string{"p.created_at > $3", "p.created_at <= $2", ViewerEligibleSQL("p")} {
		if !strings.Contains(q, want) {
			t.Errorf("trending SQL lacks %q", want)
		}
	}
	if got := args[1].(time.Time); !got.Equal(now) {
		t.Errorf("$2 = %v, want the as-of time %v", got, now)
	}
	if got := args[2].(time.Time); !got.Equal(now.Add(-TrendingWindow)) {
		t.Errorf("$3 = %v, want as-of minus the window %v", got, now.Add(-TrendingWindow))
	}
	if TrendingWindow != 7*24*time.Hour {
		t.Errorf("TrendingWindow = %v, want 7 days", TrendingWindow)
	}
}

func TestTrendingQuery_LaterPagesKeepTheCursorsAsOfTime(t *testing.T) {
	first := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	cursor := encodeTrendingCursor(trendingCursor{Score: 42, PostID: uuid.NewString(), AsOf: first})
	// An hour later the freshness bonus of a two-hour-old post has stepped
	// down; scoring page 2 against now would move rows across the boundary.
	q, args, asOf := trendingQuery(nil, 20, cursor, first.Add(time.Hour))
	if !asOf.Equal(first) {
		t.Fatalf("page 2 as-of = %v, want the cursor's %v", asOf, first)
	}
	if got := args[1].(time.Time); !got.Equal(first) {
		t.Errorf("$2 = %v, want the cursor's as-of %v", got, first)
	}
	if got := args[2].(time.Time); !got.Equal(first.Add(-TrendingWindow)) {
		t.Errorf("$3 = %v, want the cursor's window start", got)
	}
	if !strings.Contains(q, "p.id) < ($4, $5)") || args[3].(float64) != 42 {
		t.Errorf("page 2 does not continue after the cursor: %v", args)
	}
}

func TestTrendingQuery_OldAndBrokenCursors(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	// A cursor from before as_of existed still continues, scored against now.
	legacy := encodeHashtagTopCursor(7, uuid.NewString())
	q, _, asOf := trendingQuery(nil, 20, legacy, now)
	if !asOf.Equal(now) || !strings.Contains(q, "p.id) < (") {
		t.Errorf("legacy cursor: as-of %v, continues=%v", asOf, strings.Contains(q, "p.id) < ("))
	}
	// Garbage is a first page.
	q, args, _ := trendingQuery(nil, 20, "not-a-cursor", now)
	if strings.Contains(q, "p.id) < (") || len(args) != 3 {
		t.Errorf("garbage cursor was honoured: %v", args)
	}
}
