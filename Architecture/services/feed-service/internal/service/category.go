package service

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// Tube category filter (2026-09-05): `category=<id>` on /v1/feed/videos and
// /v1/feed/watch keeps only long videos whose post-level `category` (the
// post-service taxonomy, GET /v1/posts/categories) is that id.
//
// Validation is syntactic, deliberately. post-service owns the taxonomy and
// the client fetches the list from it, so feed-service accepts any
// well-formed slug and lets an id outside the taxonomy fall through to an
// empty page — the same answer a real but unused category gets. Mirroring
// the list here (fetch-and-cache) would buy a 400 for a case a real client
// never produces, at the cost of a cross-service dependency at request time
// and a second copy of the list to keep in step.
//
// The filter runs AFTER hydration: `category` lives on the post, not on
// the Scylla timeline row, so it is only known once post-service has
// answered. A page is therefore collected window by window — the same
// keyset window the unfiltered surface reads — until `limit` rows match or
// the timeline is exhausted, bounded so a category the viewer's follows
// never post cannot walk the whole timeline on one request.

// categorySlugRe is the shape of a taxonomy id: lowercase, digits, `_`/`-`,
// 1–32 runes, starting with a letter or digit.
var categorySlugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// NormalizeCategoryFilter returns the canonical form of a `category` query
// value. Empty means "no filter" and is fine. Case and surrounding
// whitespace carry no intent and are forgiven; anything else outside the
// slug shape is refused (false) so a malformed value is a 400, not a
// silently unfiltered page.
func NormalizeCategoryFilter(raw string) (string, bool) {
	id := strings.ToLower(strings.TrimSpace(raw))
	if id == "" {
		return "", true
	}
	return id, categorySlugRe.MatchString(id)
}

// maxCategoryWindows bounds how many timeline windows one category page
// may consume. Five windows of `limit` rows each is plenty for a category
// with any presence in the viewer's follows; beyond that the page returns
// short with a cursor, and the client asks again.
const maxCategoryWindows = 5

// windowFetcher reads one chronological keyset window of the surface.
type windowFetcher func(ctx context.Context, before string, limit int) ([]FeedItem, string, error)

// hydrator turns candidates into viewer-shaped posts (HydratePosts).
type hydrator func(ctx context.Context, items []FeedItem) ([]HydratedPost, error)

// categoryPage is what collectCategoryPage assembled.
type categoryPage struct {
	Posts []HydratedPost
	// Items maps every post on the page back to the candidate that
	// produced it (cursor token, source) for ranking and the fill merge.
	Items map[uuid.UUID]FeedItem
	// Next is the cursor to continue from: the row after the last one this
	// page CONSUMED — kept or rejected — never merely the last one kept.
	Next string
	// Exhausted is true when the timeline ran out inside this request, so
	// the page is short for want of rows rather than for want of windows.
	Exhausted bool
}

// collectCategoryPage pulls windows through hydration and keeps the rows
// in `category` until the page is full, the timeline is exhausted, or the
// window budget is spent. Pure over its two callbacks, for the unit test.
//
// Paging is exact: when a window yields more matches than the page still
// needs, the page is cut and Next is the cursor token of the last row
// KEPT, so the next request resumes on the row right after it. When a
// whole window is consumed, Next is that window's own cursor. Either way
// nothing is skipped and nothing repeats.
func collectCategoryPage(ctx context.Context, limit int, before, category string, fetch windowFetcher, hydrate hydrator) (categoryPage, error) {
	page := categoryPage{Items: make(map[uuid.UUID]FeedItem)}
	if limit <= 0 {
		return page, nil
	}
	cursor := before
	for w := 0; w < maxCategoryWindows; w++ {
		items, next, err := fetch(ctx, cursor, limit)
		if err != nil {
			return categoryPage{}, err
		}
		byID := make(map[uuid.UUID]FeedItem, len(items))
		for _, it := range items {
			byID[it.PostID] = it
		}
		hydrated, err := hydrate(ctx, items)
		if err != nil {
			return categoryPage{}, err
		}
		matched := filterHydratedByCategory(hydrated, category)

		need := limit - len(page.Posts)
		if len(matched) > need {
			matched = matched[:need]
			for _, p := range matched {
				page.Items[p.ID] = byID[p.ID]
			}
			page.Posts = append(page.Posts, matched...)
			page.Next = byID[matched[len(matched)-1].ID].CursorToken
			if page.Next == "" {
				page.Next = next // defensive: a timeline row always carries a token
			}
			return page, nil
		}
		for _, p := range matched {
			page.Items[p.ID] = byID[p.ID]
		}
		page.Posts = append(page.Posts, matched...)
		page.Next = next
		if next == "" {
			page.Exhausted = true
			return page, nil
		}
		if len(page.Posts) >= limit {
			return page, nil
		}
		cursor = next
	}
	return page, nil
}

// filterHydratedByCategory keeps the posts whose `category` is exactly
// `category`. Empty category keeps everything.
func filterHydratedByCategory(posts []HydratedPost, category string) []HydratedPost {
	if category == "" {
		return posts
	}
	out := make([]HydratedPost, 0, len(posts))
	for _, p := range posts {
		if p.Category == category {
			out = append(out, p)
		}
	}
	return out
}

// GetLongVideoCategoryPage is /v1/feed/videos narrowed to one category:
// hydrated, filtered, first-page discovery fill (itself category-narrowed
// at post-service), then ranked as one window.
func (s *Service) GetLongVideoCategoryPage(ctx context.Context, userID uuid.UUID, limit int, before, category string) ([]HydratedPost, string, error) {
	var blocked map[uuid.UUID]struct{}
	fetch := func(ctx context.Context, before string, limit int) ([]FeedItem, string, error) {
		items, next, b, err := s.videoTimelineWindow(ctx, userID, limit, before, false)
		blocked = b
		return items, next, err
	}
	page, err := s.collectHydratedCategoryPage(ctx, userID, limit, before, category, fetch)
	if err != nil {
		return nil, "", err
	}

	// Discovery fill, as on the unfiltered surface (GetLongVideoFeedPage),
	// with one extra condition: the timeline must be EXHAUSTED, not merely
	// out of window budget. A fill on a page whose cursor still points into
	// the timeline could resurface the same post on a later page.
	if before == "" && page.Exhausted && len(page.Posts) < limit {
		fill, err := s.longVideoDiscoveryFill(ctx, userID, blocked, category, limit*2)
		if err != nil {
			log.Printf("long video discovery fill (category %q) failed for %s: %v", category, userID, err)
		} else {
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
				for _, p := range filterHydratedByCategory(hydrated, category) {
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

	return s.rankHydratedPage(ctx, userID, page, limit, "Long video feed (category)"), page.Next, nil
}

// GetVideoFeedCategoryPage is /v1/feed/watch narrowed to one category.
// followingOnly keeps its meaning from GetVideoFeedPage (authors the viewer
// follows, per graph-service, fail closed — every window the category scan
// pulls is narrowed the same way). No discovery fill: the watch surface
// never had one.
func (s *Service) GetVideoFeedCategoryPage(ctx context.Context, userID uuid.UUID, limit int, before string, followingOnly bool, category string) ([]HydratedPost, string, error) {
	fetch := func(ctx context.Context, before string, limit int) ([]FeedItem, string, error) {
		items, next, _, err := s.videoTimelineWindow(ctx, userID, limit, before, followingOnly)
		return items, next, err
	}
	page, err := s.collectHydratedCategoryPage(ctx, userID, limit, before, category, fetch)
	if err != nil {
		return nil, "", err
	}
	return s.rankHydratedPage(ctx, userID, page, limit, "Video feed (category)"), page.Next, nil
}

func (s *Service) collectHydratedCategoryPage(ctx context.Context, userID uuid.UUID, limit int, before, category string, fetch windowFetcher) (categoryPage, error) {
	hydrate := func(ctx context.Context, items []FeedItem) ([]HydratedPost, error) {
		return s.HydratePosts(ctx, items, userID)
	}
	return collectCategoryPage(ctx, limit, before, category, fetch, hydrate)
}

// rankHydratedPage runs the main ranker over the assembled page — one
// fixed window, exactly as the unfiltered surfaces rank theirs — and
// reorders the hydrated rows to match, carrying the score onto each. On
// any ranker error the page keeps its chronological order. A row the
// ranker did not return keeps its place at the end rather than vanishing.
func (s *Service) rankHydratedPage(ctx context.Context, userID uuid.UUID, page categoryPage, limit int, surface string) []HydratedPost {
	if len(page.Posts) == 0 {
		return []HydratedPost{} // JSON [] like every other surface, never null
	}
	if s.ranker == nil {
		return page.Posts
	}
	items := make([]FeedItem, 0, len(page.Posts))
	for _, p := range page.Posts {
		if it, ok := page.Items[p.ID]; ok {
			items = append(items, it)
		}
	}
	ranked, err := s.ranker.Rank(ctx, userID, feedItemsToCandidates(items), limit)
	if err != nil {
		log.Printf("%s ranking failed, fallback to chronological: %v", surface, err)
		return page.Posts
	}
	pos := make(map[uuid.UUID]int, len(page.Posts))
	for i, p := range page.Posts {
		pos[p.ID] = i
	}
	out := make([]HydratedPost, 0, len(page.Posts))
	seen := make(map[uuid.UUID]struct{}, len(page.Posts))
	for _, c := range ranked {
		i, ok := pos[c.PostID]
		if !ok {
			continue
		}
		if _, dup := seen[c.PostID]; dup {
			continue
		}
		seen[c.PostID] = struct{}{}
		p := page.Posts[i]
		p.Score = c.Score
		out = append(out, p)
	}
	for _, p := range page.Posts {
		if _, ok := seen[p.ID]; !ok {
			out = append(out, p)
		}
	}
	return out
}
