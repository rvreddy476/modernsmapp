package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Listed pages that are filtered after the read (2026-11-07).
//
// The trending, saved and hashtag lists read a page from Postgres and then
// drop the rows this viewer may not see (postsVisibleToViewer,
// hashtagPostsVisibleTo). A page whose every row was dropped came back empty
// WITH a next cursor, and Android stops paging on an empty page, so every
// visible row after it was unreachable. visiblePage reads on until the page
// has a row or the source is exhausted.

// listRefillMaxRounds bounds the store reads behind one listed page. A page
// still empty after this many is returned empty with its cursor: the
// source is not exhausted, and the client may ask again.
const listRefillMaxRounds = 5

// visiblePage reads store pages from cursor through fetch and turns each into
// the rows the viewer is shown through keep (every gate that drops rows
// belongs in keep), until a page has at least one row or the source is
// exhausted (no next cursor, or one that does not advance), at most
// listRefillMaxRounds reads. It returns the kept rows of the last page read
// and that page's next cursor, so an exhausted source returns no cursor.
func visiblePage[T any](
	cursor string,
	fetch func(cursor string) ([]postgres.Post, string, error),
	keep func([]postgres.Post) ([]T, error),
) ([]T, string, error) {
	for round := 1; ; round++ {
		posts, next, err := fetch(cursor)
		if err != nil {
			return nil, "", err
		}
		kept, err := keep(posts)
		if err != nil {
			return nil, "", err
		}
		if len(kept) > 0 || next == "" || next == cursor || round >= listRefillMaxRounds {
			return kept, next, nil
		}
		cursor = next
	}
}

// The first trending page is the same for everyone who asks within a minute
// (the score is engagement plus a freshness step, not per viewer), and it is
// the page nearly every request is for. It is cached as the ranked ids and
// the next cursor only: on a hit the rows are re-read by id in one primary
// key lookup, so a post deleted, moderated, held or narrowed since the
// ranking is dropped at once rather than served for the cache's lifetime,
// and the viewer gate (postsVisibleToViewer) runs after it as on a miss.
const (
	trendingCacheKeyPrefix = "post:trending:first:"
	trendingFirstPageTTL   = 60 * time.Second
)

// trendingFirstPage is the cached ranking of a first trending page.
type trendingFirstPage struct {
	IDs  []uuid.UUID `json:"ids"`
	Next string      `json:"next"`
}

// trendingCacheKey names the first page for a content-type set and page
// size. The size is clamped the way the store clamps it, so out-of-range
// sizes share one entry.
func trendingCacheKey(contentTypes []string, limit int) string {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	types := append([]string(nil), contentTypes...)
	sort.Strings(types)
	return fmt.Sprintf("%s%d:%s", trendingCacheKeyPrefix, limit, strings.Join(types, ","))
}

// trendingPage is one store page of trending: the first page through the
// cache, every later page straight from the store (its cursor pins the
// ranking time, postgres.trendingCursor).
func (s *Service) trendingPage(ctx context.Context, store listReadStore, contentTypes []string, limit int, cursor string) ([]postgres.Post, string, error) {
	if cursor != "" {
		return store.GetTrendingPosts(ctx, contentTypes, limit, cursor)
	}
	rdb := s.postCacheRedis()
	key := trendingCacheKey(contentTypes, limit)
	if rdb != nil {
		rctx, cancel := postCacheCtx(ctx)
		raw, err := rdb.Get(rctx, key).Bytes()
		cancel()
		var cached trendingFirstPage
		switch {
		case err == nil && json.Unmarshal(raw, &cached) == nil:
			posts, err := store.GetPostsByIDs(ctx, cached.IDs)
			if err == nil {
				return stillTrendingInOrder(posts, cached.IDs), cached.Next, nil
			}
			slog.WarnContext(ctx, "trending cache hit not re-read; ranking again", "err", err)
		case err != nil && !errors.Is(err, redis.Nil):
			slog.DebugContext(ctx, "trending cache read failed; ranking in Postgres", "err", err)
		}
	}

	posts, next, err := store.GetTrendingPosts(ctx, contentTypes, limit, "")
	if err != nil {
		return nil, "", err
	}
	if rdb != nil {
		page := trendingFirstPage{IDs: make([]uuid.UUID, len(posts)), Next: next}
		for i := range posts {
			page.IDs[i] = posts[i].ID
		}
		if data, err := json.Marshal(page); err == nil {
			wctx, cancel := postCacheCtx(ctx)
			_ = rdb.Set(wctx, key, data, trendingFirstPageTTL).Err()
			cancel()
		}
	}
	return posts, next, nil
}

// stillTrendingInOrder puts re-read rows back in the cached ranking's order
// and keeps those the ranking query would still admit — the Go side of its
// predicate: public, not scheduled, effectively approved (deleted rows are
// not re-read at all).
func stillTrendingInOrder(posts []postgres.Post, order []uuid.UUID) []postgres.Post {
	byID := make(map[uuid.UUID]postgres.Post, len(posts))
	for _, p := range posts {
		byID[p.ID] = p
	}
	out := make([]postgres.Post, 0, len(order))
	for _, id := range order {
		p, ok := byID[id]
		if !ok || p.Visibility != "public" || p.PublishAt != nil || p.EffectiveReviewStatus() != "approved" {
			continue
		}
		out = append(out, p)
	}
	return out
}
