package service

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"
)

// GetLongVideoFilteredPage is /v1/feed/videos under any narrowing the
// plain surface does not do itself: a category (category.go), a chip, a
// sort (tube_chips.go), or any combination. The unfiltered request keeps
// its original path in GetLongVideoFeedPage; this one collects the page
// window by window, the way the category surface always has, because a
// chip drops rows the timeline read cannot see.
//
// Where each narrowing is applied, and why there:
//
//	chip     — on the candidates of every window BEFORE hydration
//	           (filterItemsByChip): a chip is decided from the timeline row
//	           alone, so hydrating a row the chip would drop is wasted
//	           post-service traffic. The discovery fill passes the same
//	           filter, so a fill cannot hand the page a row the chip
//	           excluded.
//	category — AFTER hydration, as before: category lives on the post.
//	sort     — on the assembled page, last: `popular` orders by the
//	           hydrated view count and skips the ranker; `recent` ranks as
//	           the plain surface does. The Subscriptions tab stays
//	           chronological under every sort, because that tab promises
//	           newest first and a sort parameter does not un-promise it.
//
// followingOnly / subscribedOnly keep their meaning from the plain surface
// and suppress the discovery fill the same way.
func (s *Service) GetLongVideoFilteredPage(ctx context.Context, userID uuid.UUID, limit int, before string, filter TubeFilter, followingOnly, subscribedOnly bool) ([]HydratedPost, string, error) {
	scope, err := s.resolveChipScope(ctx, userID, filter.Chip)
	if err != nil {
		return nil, "", err
	}

	var blocked map[uuid.UUID]struct{}
	fetch := func(ctx context.Context, before string, limit int) ([]FeedItem, string, error) {
		items, next, b, err := s.videoTimelineWindow(ctx, userID, limit, before, followingOnly, subscribedOnly)
		blocked = b
		return filterItemsByChip(items, filter.Chip, scope), next, err
	}
	page, err := s.collectHydratedCategoryPage(ctx, userID, limit, before, filter.Category, fetch)
	if err != nil {
		return nil, "", err
	}

	// Discovery fill, as on the unfiltered surface (GetLongVideoFeedPage),
	// with one extra condition: the timeline must be EXHAUSTED, not merely
	// out of window budget. A fill on a page whose cursor still points into
	// the timeline could resurface the same post on a later page.
	if page.Exhausted && discoveryFillAllowed(followingOnly || subscribedOnly, before, len(page.Posts), limit) {
		fill, err := s.longVideoDiscoveryFill(ctx, userID, blocked, filter.Category, limit*2)
		if err != nil {
			log.Printf("long video discovery fill (category %q, chip %q) failed for %s: %v", filter.Category, filter.Chip, userID, err)
		} else {
			fill = filterItemsByChip(fill, filter.Chip, scope)
			kept := make([]FeedItem, 0, len(page.Posts))
			for _, p := range page.Posts {
				kept = append(kept, page.Items[p.ID])
			}
			merged := mergeDiscoveryFill(kept, fill, limit)
			if extra := merged[len(kept):]; len(extra) > 0 {
				hydrated, err := s.HydratePosts(ctx, extra, userID)
				if err != nil {
					return nil, "", fmt.Errorf("hydrate discovery fill: %w", err)
				}
				// post-service already filtered by category; re-check so a
				// stale cached row cannot slip a different category in.
				for _, p := range filterHydratedByCategory(hydrated, filter.Category) {
					if len(page.Posts) >= limit {
						break
					}
					page.Posts = append(page.Posts, p)
					for _, it := range extra {
						if it.PostID == p.ID {
							page.Items[p.ID] = it
							break
						}
					}
				}
			}
		}
	}

	if subscribedOnly {
		return chronologicalHydratedPage(page), page.Next, nil
	}
	if filter.Sort == TubeSortPopular {
		return sortHydratedByViews(chronologicalHydratedPage(page)), page.Next, nil
	}
	return s.rankHydratedPage(ctx, userID, page, limit, "Long video feed (filtered)"), page.Next, nil
}
