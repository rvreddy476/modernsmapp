package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// DeltaResult holds the feed delta computation result.
type DeltaResult struct {
	NewCount int `json:"new_count"`
	// NewestAnchor is the created_at (RFC3339Nano) of the newest counted
	// item: a valid `anchor` for the next call. It used to be a post id,
	// which the anchor parser rejects.
	NewestAnchor string `json:"newest_anchor,omitempty"`
	HasMore      bool   `json:"has_more"`
}

const deltaMaxCount = 200 // cap displayed count; HasMore=true beyond this

// deltaColdStartLimit is the cold-start read for the pill. post-service's
// /v1/posts/recent clamps anything above 50 down to 20, so asking for the
// pill's 201 used to count against twenty posts.
const deltaColdStartLimit = 50

// ErrInvalidDeltaAnchor is a client error: the anchor is not a timestamp.
var ErrInvalidDeltaAnchor = errors.New("invalid anchor format: expected an RFC3339 timestamp")

// ErrUnsupportedDeltaFeed is a client error: no such feed_type.
var ErrUnsupportedDeltaFeed = errors.New("unsupported feed_type")

// ParseDeltaAnchor reads a delta anchor: RFC3339 (fractional seconds
// allowed), or a bare date.
func ParseDeltaAnchor(anchor string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, anchor); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", anchor); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("%w, got %q", ErrInvalidDeltaAnchor, anchor)
}

// ComputeFeedDelta computes the number of new items in a feed since the
// given anchor.
//
// Only the viewer's own feeds are served: home, following, flicks and
// posttube. Group, group-channel, channel, community and space counts used
// to be answered here too, straight from those services' tables with no
// membership check — so anyone could learn how active a private group was
// and the id of its newest post — and from tables feed-service's own
// database does not hold. No client asks for them; they are refused.
func (s *Service) ComputeFeedDelta(ctx context.Context, userID uuid.UUID, feedType, anchor string) (*DeltaResult, error) {
	anchorTime, err := ParseDeltaAnchor(anchor)
	if err != nil {
		return nil, err
	}

	var fresh []FeedItem
	switch feedType {
	case "home":
		fresh, err = s.deltaHome(ctx, userID, anchorTime, false)
	case "following":
		fresh, err = s.deltaHome(ctx, userID, anchorTime, true)
	case "flicks":
		fresh, err = s.deltaContentType(ctx, userID, []string{"flick", "reel"}, anchorTime)
	case "posttube":
		fresh, err = s.deltaContentType(ctx, userID, []string{"long_video", "video"}, anchorTime)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedDeltaFeed, feedType)
	}
	if err != nil {
		return nil, err
	}

	result := &DeltaResult{NewCount: len(fresh)}
	if len(fresh) > 0 {
		newest := fresh[0].CreatedAt
		for _, f := range fresh[1:] {
			if f.CreatedAt.After(newest) {
				newest = f.CreatedAt
			}
		}
		result.NewestAnchor = newest.UTC().Format(time.RFC3339Nano)
	}
	if result.NewCount > deltaMaxCount {
		result.NewCount = deltaMaxCount
		result.HasMore = true
	}
	return result, nil
}

// deltaHome returns the new items the home feed (followingOnly=false) or
// the Following tab (true) would show since `since`.
//
// The count has to be measured against the SAME feed the reader will get,
// so this runs GetHomeFeed's filters, in GetHomeFeed's order: the block /
// mute / "don't recommend" set (failing closed, like the page), hidden
// authors, the following narrowing, reels, the main-feed exclusion and the
// hidden long-video tier — then drops posts deleted since they were written.
// It used to count the raw timeline: blocked and muted authors' posts made
// the pill say "3 new posts" and the refresh showed none, and Following
// counted exactly what Home counted.
//
// GetHomeFeed backfills recommended public posts when the timeline holds
// nothing from anybody else (coldStartAllowed, countFromOthers), so the
// home count does too; Following is never backfilled.
func (s *Service) deltaHome(ctx context.Context, userID uuid.UUID, since time.Time, followingOnly bool) ([]FeedItem, error) {
	items, err := s.scyllaStore.GetHomeTimeline(ctx, userID, deltaMaxCount+1)
	if err != nil {
		return nil, err
	}
	candidates := make([]FeedItem, 0, len(items))
	for _, item := range items {
		candidates = append(candidates, FeedItem{
			PostID:      item.PostID,
			AuthorID:    item.AuthorID,
			CreatedAt:   item.CreatedAt,
			ContentType: item.ContentType,
		})
	}

	blocked, err := s.resolveBlockedSet(ctx, userID)
	if err != nil {
		return nil, err
	}
	var following []uuid.UUID
	if followingOnly {
		if following, err = s.viewerFollowing(ctx, userID); err != nil {
			return nil, fmt.Errorf("delta following filter: %w", err)
		}
	}

	if !followingOnly && countFromOthers(candidates, userID) == 0 {
		cold, coldErr := s.getRecentPublicPosts(ctx, deltaColdStartLimit)
		if coldErr != nil {
			// Non-fatal: a delta is an invitation to refresh, not the feed.
			slog.WarnContext(ctx, "feed delta: cold-start count unavailable", "user_id", userID, "err", coldErr)
		} else {
			candidates = cold
		}
	}

	fresh := freshForPill(candidates, userID, since, blocked, followingOnly, following)
	if fresh, err = s.applyHiddenAuthorFilter(ctx, fresh); err != nil {
		return nil, err
	}

	// The main feed does not show reels (dropShortForm), so the pill must
	// not count them either — same position relative to
	// filterMainFeedExcluded as in GetHomeFeed.
	fresh = dropShortForm(fresh)
	fresh = s.filterMainFeedExcluded(ctx, fresh)
	if s.GetLongVideoFrequency(ctx, userID) == "hidden" {
		fresh = s.applyLongVideoFrequency(fresh, "hidden", false)
	}
	return s.openableForPill(ctx, userID, s.dropTombstoned(ctx, fresh))
}

type visibilityOnlyKey struct{}

// visibilityOnly reports whether HydratePosts should stop after its
// filters: the caller wants which posts the viewer may open, not the
// profiles, media URLs, view counts and reasons a rendered page needs.
func visibilityOnly(ctx context.Context) bool {
	v, _ := ctx.Value(visibilityOnlyKey{}).(bool)
	return v
}

// openableForPill keeps only the items the viewer could actually open
// (audit L6). The pill used to count raw timeline rows: a reel still
// processing, a post a moderator took down, one written before its author
// went private, a members-only or now-unlisted post — each made the pill
// say "1 new" and newest_anchor point at a post the refresh never showed.
// The items now go through HydratePosts' whole verdict (post-service's
// batch, processing, listing visibility, keywords, privacy, "Not
// interested"), without the render enrichment. Fails like the page does:
// an error is an error, never an unfiltered count.
func (s *Service) openableForPill(ctx context.Context, viewerID uuid.UUID, items []FeedItem) ([]FeedItem, error) {
	if len(items) == 0 {
		return items, nil
	}
	visible, err := s.HydratePosts(context.WithValue(ctx, visibilityOnlyKey{}, true), items, viewerID)
	if err != nil {
		return nil, fmt.Errorf("feed delta: visibility check: %w", err)
	}
	return keepHydrated(items, visible), nil
}

// keepHydrated returns the items whose post survived hydration.
func keepHydrated(items []FeedItem, visible []HydratedPost) []FeedItem {
	ok := make(map[uuid.UUID]struct{}, len(visible))
	for _, p := range visible {
		ok[p.ID] = struct{}{}
	}
	out := make([]FeedItem, 0, len(items))
	for _, it := range items {
		if _, keep := ok[it.PostID]; keep {
			out = append(out, it)
		}
	}
	return out
}

// freshForPill is deltaHome's pure step: items newer than `since`, not the
// viewer's own (the composer already put those at the top), not by anyone
// in the blocked set, and — for the Following tab — only by authors the
// viewer follows.
func freshForPill(candidates []FeedItem, viewer uuid.UUID, since time.Time, blocked map[uuid.UUID]struct{}, followingOnly bool, following []uuid.UUID) []FeedItem {
	candidates = applyBlockFilter(candidates, blocked)
	if followingOnly {
		candidates = filterByAuthorSet(candidates, following)
	}
	out := make([]FeedItem, 0, len(candidates))
	for _, item := range candidates {
		if item.AuthorID == viewer || !item.CreatedAt.After(since) {
			continue
		}
		out = append(out, item)
	}
	return out
}

// deltaContentType returns the new items of the given content types in the
// viewer's timeline (the reels and Tube pills), minus blocked and hidden
// authors, the viewer's own posts and deleted posts.
func (s *Service) deltaContentType(ctx context.Context, userID uuid.UUID, contentTypes []string, since time.Time) ([]FeedItem, error) {
	items, err := s.scyllaStore.GetHomeTimelineByContentTypes(ctx, userID, contentTypes, deltaMaxCount+1)
	if err != nil {
		return nil, err
	}
	blocked, err := s.resolveBlockedSet(ctx, userID)
	if err != nil {
		return nil, err
	}
	candidates := make([]FeedItem, 0, len(items))
	for _, item := range items {
		candidates = append(candidates, FeedItem{
			PostID:      item.PostID,
			AuthorID:    item.AuthorID,
			CreatedAt:   item.CreatedAt,
			ContentType: item.ContentType,
		})
	}
	fresh := freshForPill(candidates, userID, since, blocked, false, nil)
	if fresh, err = s.applyHiddenAuthorFilter(ctx, fresh); err != nil {
		return nil, err
	}
	return s.openableForPill(ctx, userID, s.dropTombstoned(ctx, fresh))
}

// dropTombstoned removes posts soft-deleted since their timeline rows were
// written (post_lifecycle.go writes the tombstone). Best effort: on a Redis
// error the items are returned as they are — an over-count by a deleted
// post is the pill's worst case, and the page itself is gated by
// post-service's batch endpoint.
func (s *Service) dropTombstoned(ctx context.Context, items []FeedItem) []FeedItem {
	if len(items) == 0 || s.rdb == nil {
		return items
	}
	keys := make([]string, len(items))
	for i, it := range items {
		keys[i] = DeletedTombstoneKey(it.PostID)
	}
	vals, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		slog.WarnContext(ctx, "feed delta: tombstone check unavailable", "err", err)
		return items
	}
	out := items[:0]
	for i, it := range items {
		if vals[i] != nil {
			continue
		}
		out = append(out, it)
	}
	return out
}

// GetCachedDelta retrieves a cached delta result from Redis.
func (s *Service) GetCachedDelta(ctx context.Context, cacheKey string) (*DeltaResult, error) {
	data, err := s.rdb.Get(ctx, cacheKey).Bytes()
	if err != nil {
		return nil, err
	}
	var result DeltaResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CacheDelta stores a delta result in Redis with the given TTL.
func (s *Service) CacheDelta(ctx context.Context, cacheKey string, result *DeltaResult, ttl time.Duration) {
	data, err := json.Marshal(result)
	if err != nil {
		slog.Warn("failed to marshal delta for caching", "error", err)
		return
	}
	if err := s.rdb.Set(ctx, cacheKey, data, ttl).Err(); err != nil {
		slog.Warn("failed to cache delta", "key", cacheKey, "error", err)
	}
}
