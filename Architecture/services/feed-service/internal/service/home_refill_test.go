package service

import (
	"context"
	"errors"
	"math/rand"
	"testing"
	"time"

	"github.com/atpost/feed-service/internal/store/scylla"
	"github.com/google/uuid"
)

// feed-05: a home page is refilled from further timeline windows when its
// filters leave it short, and the keyset cursor stays exact.

// timelineOf builds a home timeline, newest first, from content types.
func timelineOf(contentTypes ...string) []scylla.FeedItem {
	start := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	rows := make([]scylla.FeedItem, 0, len(contentTypes))
	for i, ct := range contentTypes {
		post := uuid.New()
		at := start.Add(-time.Duration(i) * time.Second)
		rows = append(rows, scylla.FeedItem{
			PostID: post, AuthorID: uuid.New(), CreatedAt: at, ContentType: ct,
			CursorToken: scylla.TimelineTS(post, at).String(),
		})
	}
	return rows
}

func repeatType(ct string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = ct
	}
	return out
}

// homeFetchLimit is GetHomeFeed's window size for a page of `limit`.
func homeFetchLimit(limit int) int {
	fetchLimit := limit + limit/2
	if fetchLimit < limit+10 {
		fetchLimit = limit + 10
	}
	if fetchLimit > 200 {
		fetchLimit = 200
	}
	return fetchLimit
}

// homePager serves pages the way GetHomeFeed does: collectHomeCandidates
// over a synthetic timeline with Home's own reel filter, then homeWindow.
type homePager struct {
	rows  []scylla.FeedItem
	drop  func(scylla.FeedItem) bool // extra filter (blocked author, ...)
	reads int
}

func (p *homePager) page(t *testing.T, token string, limit int) ([]FeedItem, string) {
	t.Helper()
	fetchLimit := homeFetchLimit(limit)
	read := func(_ context.Context, before string) ([]scylla.FeedItem, error) {
		p.reads++
		if before == "" {
			before = token
		}
		return readBefore(p.rows, before, fetchLimit), nil
	}
	filter := func(_ context.Context, raw []scylla.FeedItem, _ bool) ([]FeedItem, error) {
		out := make([]FeedItem, 0, len(raw))
		for _, r := range raw {
			if p.drop != nil && p.drop(r) {
				continue
			}
			out = append(out, FeedItem{PostID: r.PostID, AuthorID: r.AuthorID, CreatedAt: r.CreatedAt, ContentType: r.ContentType, CursorToken: r.CursorToken})
		}
		return dropShortForm(out), nil
	}
	candidates, raw, err := collectHomeCandidates(context.Background(), read, filter, fetchLimit, func() int { return limit })
	if err != nil {
		t.Fatal(err)
	}
	return homeWindow(candidates, raw, fetchLimit, limit)
}

// The finding's acceptance test: 40 reel rows, then 5 posts, limit 20. The
// first page used to be empty with a cursor; it must hold the 5 posts.
func TestHomeRefill_FortyReelsThenFivePostsFirstPageHasThePosts(t *testing.T) {
	rows := timelineOf(append(repeatType("flick", 40), repeatType("post", 5)...)...)
	p := &homePager{rows: rows}
	page, next := p.page(t, "", 20)
	if len(page) != 5 {
		t.Fatalf("first page holds %d items, want the 5 posts behind the 40 reels", len(page))
	}
	for i, it := range page {
		if it.PostID != rows[40+i].PostID {
			t.Fatalf("item %d is not post %d, in timeline order", i, i)
		}
	}
	if next != "" {
		t.Fatalf("the timeline is exhausted (45 rows, the last read came back short) but the page carries cursor %q", next)
	}
	if p.reads != 2 {
		t.Fatalf("took %d reads, want 2 (30 reels, then 10 reels and the 5 posts)", p.reads)
	}
}

// A page that fills on its first window reads once: the refill costs
// nothing when it is not needed.
func TestHomeRefill_FullFirstWindowReadsOnce(t *testing.T) {
	p := &homePager{rows: timelineOf(repeatType("post", 100)...)}
	page, next := p.page(t, "", 20)
	if len(page) != 20 || next == "" || p.reads != 1 {
		t.Fatalf("page %d items, next %q, %d read(s); want 20, a cursor, 1", len(page), next, p.reads)
	}
}

// A run of filtered rows longer than the window budget returns short WITH
// a cursor, and the next request carries on from there; nothing is skipped.
func TestHomeRefill_BoundedAndTheCursorCarriesOn(t *testing.T) {
	rows := timelineOf(append(repeatType("flick", 400), repeatType("post", 3)...)...)
	p := &homePager{rows: rows}
	page, next := p.page(t, "", 20)
	if p.reads != maxHomeWindows {
		t.Fatalf("one request made %d reads, want the bound %d", p.reads, maxHomeWindows)
	}
	if len(page) != 0 || next == "" {
		t.Fatalf("page %d items, next %q; want an empty page with a cursor (150 reels read, 250 to go)", len(page), next)
	}
	if want := rows[maxHomeWindows*homeFetchLimit(20)-1].CursorToken; next != want {
		t.Fatalf("cursor is not the last row read: the next page would re-read or skip rows")
	}
	served := 0
	for pages := 0; next != "" && pages < 50; pages++ {
		page, next = p.page(t, next, 20)
		served += len(page)
	}
	if served != 3 || next != "" {
		t.Fatalf("walking on served %d posts (cursor %q), want the 3 behind the reels and an end", served, next)
	}
}

// Every eligible post is served exactly once across pages, with reels and
// blocked authors interleaved in runs of every length, whatever ranking
// does to each page.
func TestHomeRefill_WalkServesEveryPostExactlyOnce(t *testing.T) {
	for _, limit := range []int{1, 7, 20, 50} {
		rng := rand.New(rand.NewSource(int64(limit)))
		var types []string
		for len(types) < 900 {
			// Runs of reels from 0 to 120 long (longer than a window, and
			// some longer than a whole request's budget for small limits),
			// then a few posts.
			types = append(types, repeatType("flick", rng.Intn(121))...)
			types = append(types, repeatType("post", 1+rng.Intn(6))...)
		}
		rows := timelineOf(types...)
		blockedAuthor := uuid.New()
		for i := range rows {
			if rng.Intn(5) == 0 {
				rows[i].AuthorID = blockedAuthor
			}
		}
		p := &homePager{rows: rows, drop: func(r scylla.FeedItem) bool { return r.AuthorID == blockedAuthor }}

		want := map[uuid.UUID]bool{}
		for _, r := range rows {
			if r.ContentType == "post" && r.AuthorID != blockedAuthor {
				want[r.PostID] = true
			}
		}
		served := map[uuid.UUID]int{}
		var order []uuid.UUID
		token, emptyPages := "", 0
		for pages := 0; ; pages++ {
			if pages > 5000 {
				t.Fatalf("limit %d: the walk never ended", limit)
			}
			page, next := p.page(t, token, limit)
			if len(page) > limit {
				t.Fatalf("limit %d: a page of %d", limit, len(page))
			}
			if len(page) == 0 {
				emptyPages++
			}
			order = append(order, func() []uuid.UUID {
				ids := make([]uuid.UUID, len(page))
				for i, it := range page {
					ids[i] = it.PostID
				}
				return ids
			}()...)
			rng.Shuffle(len(page), func(i, j int) { page[i], page[j] = page[j], page[i] }) // any ranking at all
			for _, it := range page {
				served[it.PostID]++
			}
			if next == "" {
				break
			}
			if next == token {
				t.Fatalf("limit %d: the cursor did not advance", limit)
			}
			token = next
		}
		for id := range want {
			if served[id] != 1 {
				t.Fatalf("limit %d: an eligible post was served %d times, want exactly once", limit, served[id])
			}
		}
		if len(served) != len(want) {
			t.Fatalf("limit %d: served %d distinct posts, want %d (a filtered row got through)", limit, len(served), len(want))
		}
		// Pages are cut in timeline order: the walk is the eligible rows in
		// order, with nothing out of place across a page boundary.
		i := 0
		for _, r := range rows {
			if want[r.PostID] {
				if order[i] != r.PostID {
					t.Fatalf("limit %d: position %d of the walk is out of timeline order", limit, i)
				}
				i++
			}
		}
		// No run here is longer than 150 rows for limit 20 and up, so no
		// page of those walks may come back empty before the end.
		if limit >= 20 && emptyPages > 1 {
			t.Fatalf("limit %d: %d empty pages; runs within the window budget must be refilled", limit, emptyPages)
		}
	}
}

// The cold-start fill takes page slots, so `want` shrinks and the refill
// stops early; and it stops at once when the first window fills the page.
func TestCollectHomeCandidates_WantIsReadAfterTheFirstWindow(t *testing.T) {
	rows := timelineOf(repeatType("flick", 200)...)
	cold := 0
	reads := 0
	read := func(_ context.Context, before string) ([]scylla.FeedItem, error) {
		reads++
		return readBefore(rows, before, 30), nil
	}
	filter := func(_ context.Context, raw []scylla.FeedItem, first bool) ([]FeedItem, error) {
		if first {
			cold = 20 // the cold-start fill found a page's worth
		}
		return nil, nil
	}
	if _, _, err := collectHomeCandidates(context.Background(), read, filter, 30, func() int { return 20 - cold }); err != nil {
		t.Fatal(err)
	}
	if reads != 1 {
		t.Fatalf("read %d windows although the cold-start fill already filled the page", reads)
	}
}

// A failed refill read or filter fails the request: a page silently cut at
// the failure would carry a cursor past rows that were never filtered.
func TestCollectHomeCandidates_ErrorsAreNotSwallowed(t *testing.T) {
	rows := timelineOf(repeatType("flick", 200)...)
	boom := errors.New("scylla timeout")
	keepNone := func(context.Context, []scylla.FeedItem, bool) ([]FeedItem, error) { return nil, nil }

	reads := 0
	failSecondRead := func(_ context.Context, before string) ([]scylla.FeedItem, error) {
		if reads++; reads == 2 {
			return nil, boom
		}
		return readBefore(rows, before, 30), nil
	}
	if _, _, err := collectHomeCandidates(context.Background(), failSecondRead, keepNone, 30, func() int { return 20 }); !errors.Is(err, boom) {
		t.Fatalf("refill read error = %v, want it returned", err)
	}

	okRead := func(_ context.Context, before string) ([]scylla.FeedItem, error) {
		return readBefore(rows, before, 30), nil
	}
	failSecondFilter := func(_ context.Context, _ []scylla.FeedItem, first bool) ([]FeedItem, error) {
		if !first {
			return nil, boom // the hidden-author lookup failed: fail closed
		}
		return nil, nil
	}
	if _, _, err := collectHomeCandidates(context.Background(), okRead, failSecondFilter, 30, func() int { return 20 }); !errors.Is(err, boom) {
		t.Fatalf("refill filter error = %v, want it returned", err)
	}
}
