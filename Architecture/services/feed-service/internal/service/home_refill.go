package service

import (
	"context"

	"github.com/atpost/feed-service/internal/store/scylla"
)

// Home page refill (finding feed-05, 16 Nov 2026).
//
// A home page used to be cut from ONE timeline read of limit+10 rows. The
// home timeline also holds reels and long videos (they share
// home_timeline_by_user) and rows a filter removes (blocked, hidden and
// muted authors, main_feed=false, notes), so a Following page whose newest
// ~30 rows were all reels came back EMPTY with a cursor. The web then showed
// "No posts yet"; Android gave up after five such pages.
//
// collectHomeCandidates reads further windows while the page is short and
// the timeline goes on, the way the category surfaces already do
// (collectCategoryPage, category.go), up to maxHomeWindows reads.
//
// The cursor stays exact. Each window starts strictly after the last RAW
// row of the window before it, so the windows are contiguous and disjoint,
// and the candidates come out in timeline order. homeWindow then cuts the
// page from them exactly as before, given the last window read:
//
//   - the page filled with candidates to spare: the cursor is the last
//     timeline row SERVED, and the spare ones are read again next page;
//   - every candidate fits and the last read came back full: the cursor is
//     the last raw row of the last window, filtered rows included, so they
//     are not read twice;
//   - the last read came back short: the timeline is exhausted, no cursor.
//
// So no post is served twice or skipped, whatever the filters removed.

// maxHomeWindows bounds the timeline reads of one home request. With the
// default page of 20 (windows of 30 rows) that is 150 rows; a run of
// filtered rows longer than that still returns a short or empty page WITH a
// cursor, and the client reads on from there. Each window costs one Scylla
// read plus the per-window lookups (hidden authors, main-feed policy), so
// the bound is also the request's worst case.
const maxHomeWindows = 5

// homeWindowRead reads one raw timeline window. before == "" is the
// request's own first read (its cursor, or the top); otherwise the window
// strictly after the row with that timeline key.
type homeWindowRead func(ctx context.Context, before string) ([]scylla.FeedItem, error)

// homeWindowFilter turns one raw window into the candidates that survive
// every filter, in the same order. first is true for the request's first
// window only.
type homeWindowFilter func(ctx context.Context, raw []scylla.FeedItem, first bool) ([]FeedItem, error)

// collectHomeCandidates reads and filters windows until `want()` candidates
// are in hand, the timeline is exhausted or the window budget is spent. It
// returns the candidates of every window read, in timeline order, and the
// LAST raw window (what homeWindow derives the cursor from). want is a
// function because the first window's filter decides how many slots the
// cold-start fill takes; it is read after each window.
//
// fetchLimit is how many rows each read asks for: a window shorter than
// that means the timeline has no more rows.
func collectHomeCandidates(ctx context.Context, read homeWindowRead, filter homeWindowFilter, fetchLimit int, want func() int) ([]FeedItem, []scylla.FeedItem, error) {
	raw, err := read(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	candidates, err := filter(ctx, raw, true)
	if err != nil {
		return nil, nil, err
	}
	for reads := 1; reads < maxHomeWindows; reads++ {
		if len(candidates) >= want() {
			break // the page is full
		}
		if fetchLimit <= 0 || len(raw) < fetchLimit {
			break // a short read: the timeline is exhausted
		}
		before := raw[len(raw)-1].CursorToken
		if before == "" {
			break // a row without a timeline key cannot be paged past
		}
		next, err := read(ctx, before)
		if err != nil {
			return nil, nil, err
		}
		homeRefillReads.Inc()
		kept, err := filter(ctx, next, false)
		if err != nil {
			return nil, nil, err
		}
		candidates = append(candidates, kept...)
		raw = next
	}
	return candidates, raw, nil
}
