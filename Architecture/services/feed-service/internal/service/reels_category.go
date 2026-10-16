package service

import (
	"context"
	"log"
	"sort"

	"github.com/google/uuid"
)

// Reels category chips (TikTok parity T-18, 2026-10-14): `category=<id>` on
// /v1/feed/reels and its /v1/feed/flicks alias keeps only short videos whose
// post-level `category` (post-service's closed flick taxonomy) is that id.
//
// The contract is the Tube one (category.go), deliberately:
//
//   - the value goes through NormalizeCategoryFilter: case and surrounding
//     whitespace are forgiven, a malformed value is 400 INVALID_CATEGORY,
//     and a well-formed id no reel carries is an empty page (200, []) —
//     feed-service does not mirror the taxonomy;
//   - the filter runs after hydration (category lives on the post), so the
//     page is collected window by window over the same short-form timeline
//     window the plain surface reads (reelTimelineWindow): flick/reel rows
//     only, block/mute, hidden-author and following_only applied per window
//     before hydration, and every hydration rule (visibility, privacy,
//     processing, keywords, "Not interested" / "Don't recommend") applied by
//     HydratePosts;
//   - the cursor is exact (collectCategoryPage): the token of the last row
//     KEPT when a window is cut, the window's own cursor when it is
//     consumed, so nothing is skipped or repeated between pages;
//   - the first-page discovery fill runs only when the timeline is
//     EXHAUSTED and is itself narrowed to the category at post-service and
//     re-checked after hydration, so it can neither repeat a row a later
//     page would serve nor smuggle in another category.
//
// Ordering matches the plain reels page: pure recency (scoreReels), not the
// main ranker.
//
// The home feed is untouched: reels never enter it, with or without a
// category.

// maxReelCategoryWindowsWhileEmpty extends the window budget while a reels
// category page has matched nothing. A chip is a narrow slice of a short
// timeline, so a run of other-category reels longer than the usual budget
// (maxCategoryWindows) is far likelier here than on Tube; scanning on while
// the page is still empty keeps the client from receiving an empty page
// with a cursor in that case. Once anything has matched, the usual budget
// applies, so a sparse page returns promptly with what it has.
const maxReelCategoryWindowsWhileEmpty = 3 * maxCategoryWindows

// GetReelCategoryPage is /v1/feed/reels (and /v1/feed/flicks) narrowed to
// one category. followingOnly keeps its meaning from GetFlickFeedPage
// (authors the viewer follows, fail closed) and, as there, forbids the
// discovery fill.
func (s *Service) GetReelCategoryPage(ctx context.Context, userID uuid.UUID, limit int, before, category string, followingOnly bool) ([]HydratedPost, string, error) {
	// The block set the timeline windows resolved, reused by the fill. A
	// nil set is a real answer (blocks nobody), so resolution is tracked
	// separately.
	var blocked map[uuid.UUID]struct{}
	blockedResolved := false
	fetch := func(ctx context.Context, before string, limit int) ([]FeedItem, string, error) {
		items, next, b, err := s.reelTimelineWindow(ctx, userID, limit, before, followingOnly)
		if err == nil {
			blocked, blockedResolved = b, true
		}
		return items, next, err
	}
	page, err := s.collectHydratedCategoryPageWithin(ctx, userID, limit, before, category, fetch, maxCategoryWindows, maxReelCategoryWindowsWhileEmpty)
	if err != nil {
		return nil, "", err
	}

	// Discovery fill, as on the plain reels page (GetFlickFeedPage), with
	// the Tube category surface's extra condition: the timeline must be
	// exhausted, not merely out of window budget, or a fill row could
	// resurface on a later page. Fails closed to the timeline page.
	if page.Exhausted && discoveryFillAllowed(followingOnly, before, len(page.Posts), limit) {
		if !blockedResolved {
			// Never fill against an unresolved block set: resolve it here,
			// fail closed (no fill) on error.
			if blocked, err = s.resolveBlockedSet(ctx, userID); err != nil {
				log.Printf("reels discovery fill (category %q) skipped for %s: %v", category, userID, err)
				return recencyOrderedHydratedPage(page), page.Next, nil
			}
		}
		fill, err := s.shortFormDiscoveryFill(ctx, userID, blocked, category, limit*2)
		if err != nil {
			log.Printf("reels discovery fill (category %q) failed for %s: %v", category, userID, err)
		} else if err := s.appendHydratedFill(ctx, userID, &page, fill, limit, category, isShortFormFillType); err != nil {
			return nil, "", err
		}
	}

	return recencyOrderedHydratedPage(page), page.Next, nil
}

// recencyOrderedHydratedPage orders an assembled reels page the way the
// plain reels page is ordered — scoreReels' pure recency score, newest
// first — and carries the score onto each row, as the plain page's
// pre-hydration scoring does. A row with no candidate behind it keeps its
// place at the end rather than vanishing. An empty page is JSON [].
func recencyOrderedHydratedPage(page categoryPage) []HydratedPost {
	if len(page.Posts) == 0 {
		return []HydratedPost{}
	}
	items := make([]FeedItem, 0, len(page.Posts))
	for _, p := range page.Posts {
		if it, ok := page.Items[p.ID]; ok {
			items = append(items, it)
		}
	}
	scored := scoreReels(items)
	score := make(map[uuid.UUID]float64, len(scored))
	rank := make(map[uuid.UUID]int, len(scored))
	for i, it := range scored {
		score[it.PostID] = it.Score
		rank[it.PostID] = i
	}
	out := make([]HydratedPost, len(page.Posts))
	copy(out, page.Posts)
	for i := range out {
		if v, ok := score[out[i].ID]; ok {
			out[i].Score = v
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, iok := rank[out[i].ID]
		rj, jok := rank[out[j].ID]
		switch {
		case iok && jok:
			return ri < rj
		default:
			return iok && !jok
		}
	})
	return out
}
