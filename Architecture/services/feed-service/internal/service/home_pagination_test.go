package service

import (
	"math/rand"
	"testing"
	"time"

	"github.com/atpost/feed-service/internal/store/scylla"
	"github.com/google/uuid"
)

// Home-feed pagination: the page is the first `limit` filtered candidates in
// timeline order, ranking only reorders it, and the cursor is the oldest
// timeline row the page reached. Walking every page must serve each
// eligible post exactly once, whatever the ranker does to each page.

// syntheticTimeline is a home timeline, newest first, keyed like the real
// one (scylla.TimelineTS), with a third of the rows by blocked authors.
func syntheticTimeline(n int) ([]scylla.FeedItem, map[uuid.UUID]struct{}) {
	blocked := map[uuid.UUID]struct{}{}
	blockedAuthor := uuid.New()
	blocked[blockedAuthor] = struct{}{}
	start := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	rows := make([]scylla.FeedItem, 0, n)
	for i := 0; i < n; i++ {
		author := uuid.New()
		if i%3 == 0 {
			author = blockedAuthor
		}
		post := uuid.New()
		// Several posts share each millisecond, the case a timestamp
		// cursor could not page through without repeating or skipping.
		at := start.Add(-time.Duration(i/4) * time.Millisecond)
		rows = append(rows, scylla.FeedItem{
			PostID: post, AuthorID: author, CreatedAt: at, ContentType: "post",
			CursorToken: scylla.TimelineTS(post, at).String(),
		})
	}
	// Newest first by key, exactly as Scylla returns them.
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && keyAfter(rows[j], rows[j-1]); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
	return rows, blocked
}

func keyAfter(a, b scylla.FeedItem) bool {
	ka, kb := uuid.MustParse(a.CursorToken), uuid.MustParse(b.CursorToken)
	ta, tb := ka.Time(), kb.Time()
	if ta != tb {
		return ta > tb
	}
	return a.CursorToken > b.CursorToken
}

// readBefore mimics GetHomeTimeline / GetHomeTimelineBeforeToken: up to
// limit rows strictly after `token` in timeline order ("" = from the top).
func readBefore(rows []scylla.FeedItem, token string, limit int) []scylla.FeedItem {
	start := 0
	if token != "" {
		start = -1
		for i, r := range rows {
			if r.CursorToken == token {
				start = i + 1
				break
			}
		}
		if start < 0 {
			panic("cursor names no row")
		}
	}
	end := start + limit
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end]
}

func TestHomeWindow_WalkServesEveryEligiblePostExactlyOnce(t *testing.T) {
	for _, limit := range []int{1, 7, 20} {
		rows, blocked := syntheticTimeline(300)
		fetchLimit := limit + limit/2
		if fetchLimit < limit+10 {
			fetchLimit = limit + 10
		}
		rng := rand.New(rand.NewSource(int64(limit)))

		served := map[uuid.UUID]int{}
		token := ""
		for page := 0; page < 1000; page++ {
			raw := readBefore(rows, token, fetchLimit)
			candidates := make([]FeedItem, 0, len(raw))
			for _, r := range raw {
				if _, ok := blocked[r.AuthorID]; ok {
					continue
				}
				candidates = append(candidates, FeedItem{
					PostID: r.PostID, AuthorID: r.AuthorID, CreatedAt: r.CreatedAt,
					ContentType: r.ContentType, CursorToken: r.CursorToken,
				})
			}
			window, next := homeWindow(candidates, raw, fetchLimit, limit)
			if len(window) > limit {
				t.Fatalf("limit %d: page of %d", limit, len(window))
			}
			// Any ranking at all: shuffle the page.
			rng.Shuffle(len(window), func(i, j int) { window[i], window[j] = window[j], window[i] })
			for _, w := range window {
				served[w.PostID]++
			}
			if next == "" {
				break
			}
			token = next
		}

		want := 0
		for _, r := range rows {
			if _, ok := blocked[r.AuthorID]; ok {
				if served[r.PostID] != 0 {
					t.Fatalf("limit %d: served a blocked author's post", limit)
				}
				continue
			}
			want++
			if got := served[r.PostID]; got != 1 {
				t.Fatalf("limit %d: post served %d times, want exactly 1", limit, got)
			}
		}
		if len(served) != want {
			t.Fatalf("limit %d: served %d distinct posts, want %d", limit, len(served), want)
		}
	}
}

func TestHomeWindow_ColdStartItemsNeverBecomeTheCursor(t *testing.T) {
	// Two timeline rows (the viewer's own) and a cold-start fill that came
	// from post-service, not the timeline: no CursorToken.
	rows, _ := syntheticTimeline(2)
	candidates := []FeedItem{
		{PostID: rows[0].PostID, CursorToken: rows[0].CursorToken},
		{PostID: rows[1].PostID, CursorToken: rows[1].CursorToken},
	}
	for i := 0; i < 30; i++ {
		candidates = append(candidates, FeedItem{PostID: uuid.New(), Source: sourceColdStart})
	}
	window, next := homeWindow(candidates, rows, 30, 20)
	if len(window) != 20 {
		t.Fatalf("page of %d, want 20", len(window))
	}
	if next != "" {
		t.Fatalf("the timeline is exhausted (short read) but the page carries cursor %q", next)
	}
}

func TestHomeWindow_FilteredOutPageStillAdvances(t *testing.T) {
	rows, _ := syntheticTimeline(40)
	// Every row was filtered: an empty page, but the read came back full,
	// so the next page must start below it rather than end the feed.
	window, next := homeWindow(nil, rows[:30], 30, 20)
	if len(window) != 0 {
		t.Fatalf("page of %d, want 0", len(window))
	}
	if next != rows[29].CursorToken {
		t.Fatalf("cursor %q, want the oldest row read (%q)", next, rows[29].CursorToken)
	}
}

func TestWholeWindow_KeepsItemsTheRankerDropped(t *testing.T) {
	a, b, c := FeedItem{PostID: uuid.New()}, FeedItem{PostID: uuid.New()}, FeedItem{PostID: uuid.New()}
	got := wholeWindow([]FeedItem{a, b, c}, []FeedItem{c, a})
	if len(got) != 3 || got[0].PostID != c.PostID || got[1].PostID != a.PostID || got[2].PostID != b.PostID {
		t.Fatalf("wholeWindow = %v, want ranked order then the dropped item", got)
	}
}
