package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// DeltaResult holds the feed delta computation result.
type DeltaResult struct {
	NewCount     int    `json:"new_count"`
	NewestAnchor string `json:"newest_anchor,omitempty"`
	HasMore      bool   `json:"has_more"`
}

const deltaMaxCount = 200 // cap displayed count; HasMore=true beyond this

// ComputeFeedDelta computes the number of new items in a feed since the given anchor.
func (s *Service) ComputeFeedDelta(ctx context.Context, userID uuid.UUID, feedType, anchor, groupID, channelID, communityID, spaceID string) (*DeltaResult, error) {
	// Parse anchor as a timestamp (ISO 8601 / RFC3339)
	anchorTime, err := time.Parse(time.RFC3339, anchor)
	if err != nil {
		// Try parsing as just a date
		anchorTime, err = time.Parse("2006-01-02", anchor)
		if err != nil {
			return nil, fmt.Errorf("invalid anchor format: expected RFC3339 timestamp, got %q", anchor)
		}
	}

	var count int
	var newestAnchor string

	switch feedType {
	case "home":
		count, newestAnchor, err = s.deltaHome(ctx, userID, anchorTime)
	case "following":
		count, newestAnchor, err = s.deltaFollowing(ctx, userID, anchorTime)
	case "group":
		count, newestAnchor, err = s.deltaGroup(ctx, groupID, anchorTime)
	case "group_channel":
		count, newestAnchor, err = s.deltaGroupChannel(ctx, groupID, channelID, anchorTime)
	case "channel":
		count, newestAnchor, err = s.deltaChannel(ctx, channelID, anchorTime)
	case "community":
		count, newestAnchor, err = s.deltaCommunity(ctx, communityID, anchorTime)
	case "community_space":
		count, newestAnchor, err = s.deltaCommunitySpace(ctx, spaceID, anchorTime)
	case "flicks":
		count, newestAnchor, err = s.deltaContentType(ctx, userID, "flick", anchorTime)
	case "posttube":
		count, newestAnchor, err = s.deltaContentType(ctx, userID, "long_video", anchorTime)
	default:
		return nil, fmt.Errorf("unsupported feed_type: %s", feedType)
	}

	if err != nil {
		return nil, err
	}

	hasMore := count > deltaMaxCount
	if hasMore {
		count = deltaMaxCount
	}

	return &DeltaResult{
		NewCount:     count,
		NewestAnchor: newestAnchor,
		HasMore:      hasMore,
	}, nil
}

// deltaHome counts new items in the user's home timeline since anchor.
// Applies the social-home distribution exclusion (P0-1) and the viewer's
// hidden long-video tier (P0-4) so the "N new posts" pill never counts
// posts the page itself would not show.
func (s *Service) deltaHome(ctx context.Context, userID uuid.UUID, since time.Time) (int, string, error) {
	items, err := s.scyllaStore.GetHomeTimeline(ctx, userID, deltaMaxCount+1)
	if err != nil {
		return 0, "", err
	}

	/*
		The count has to be measured against the SAME feed the reader will get.

		GetHomeFeed backfills recommended public posts when the timeline holds
		nothing from anybody else (see coldStartAllowed and countFromOthers).
		This counted the raw timeline only, so for exactly those readers — the
		ones whose feed is entirely backfilled — the answer was always zero and
		the "N new posts" banner could never appear, however much was published.
		Two surfaces disagreeing about what a feed contains is worse than either
		of them being wrong on its own.
	*/
	// The timeline is []scylla.FeedItem and the cold-start source is
	// []FeedItem; normalise to the service type before either is counted.
	candidates := make([]FeedItem, 0, len(items))
	for _, item := range items {
		candidates = append(candidates, FeedItem{
			PostID:      item.PostID,
			AuthorID:    item.AuthorID,
			CreatedAt:   item.CreatedAt,
			ContentType: item.ContentType,
		})
	}

	if countFromOthers(candidates, userID) == 0 {
		cold, coldErr := s.getRecentPublicPosts(ctx, deltaMaxCount+1)
		if coldErr != nil {
			// Non-fatal: a delta is an invitation to refresh, not the feed.
			slog.WarnContext(ctx, "feed delta: cold-start count unavailable", "user_id", userID, "err", coldErr)
		} else {
			candidates = cold
		}
	}

	fresh := make([]FeedItem, 0, len(candidates))
	for _, item := range candidates {
		// Your own post is never news to you: you just wrote it, and the
		// composer has already put it at the top of the feed.
		if item.AuthorID == userID {
			continue
		}
		if item.CreatedAt.After(since) {
			fresh = append(fresh, item)
		}
	}
	// The main feed does not show reels (dropShortForm), so the pill must
	// not count them either — same position relative to
	// filterMainFeedExcluded as in GetHomeFeed.
	fresh = dropShortForm(fresh)
	fresh = s.filterMainFeedExcluded(ctx, fresh)
	if s.GetLongVideoFrequency(ctx, userID) == "hidden" {
		fresh = s.applyLongVideoFrequency(fresh, "hidden", false)
	}

	count := len(fresh)
	newest := ""
	if count > 0 {
		newest = fresh[0].PostID.String()
	}
	return count, newest, nil
}

// deltaFollowing counts new items from followed users since anchor.
// Uses the same home timeline but scoped to followed sources.
func (s *Service) deltaFollowing(ctx context.Context, userID uuid.UUID, since time.Time) (int, string, error) {
	// For following feed, we use the home timeline (which is populated by fanout from followed users)
	return s.deltaHome(ctx, userID, since)
}

// deltaGroup counts new posts in a group since anchor (Postgres query).
func (s *Service) deltaGroup(ctx context.Context, groupID string, since time.Time) (int, string, error) {
	var count int
	var newestID *string
	err := s.pgStore.DB().QueryRow(ctx, `
		SELECT COUNT(*), (SELECT id::text FROM group_posts WHERE group_id = $1 AND status = 'published' AND created_at > $2 ORDER BY created_at DESC LIMIT 1)
		FROM group_posts
		WHERE group_id = $1 AND status = 'published' AND created_at > $2
	`, groupID, since).Scan(&count, &newestID)
	if err != nil {
		slog.Warn("delta group query failed", "group_id", groupID, "error", err)
		return 0, "", err
	}
	newest := ""
	if newestID != nil {
		newest = *newestID
	}
	return count, newest, nil
}

// deltaGroupChannel counts new posts in a specific group channel since anchor.
func (s *Service) deltaGroupChannel(ctx context.Context, groupID, channelID string, since time.Time) (int, string, error) {
	var count int
	var newestID *string
	err := s.pgStore.DB().QueryRow(ctx, `
		SELECT COUNT(*), (SELECT id::text FROM group_posts WHERE group_id = $1 AND channel_id = $2 AND status = 'published' AND created_at > $3 ORDER BY created_at DESC LIMIT 1)
		FROM group_posts
		WHERE group_id = $1 AND channel_id = $2 AND status = 'published' AND created_at > $3
	`, groupID, channelID, since).Scan(&count, &newestID)
	if err != nil {
		slog.Warn("delta group_channel query failed", "group_id", groupID, "channel_id", channelID, "error", err)
		return 0, "", err
	}
	newest := ""
	if newestID != nil {
		newest = *newestID
	}
	return count, newest, nil
}

// deltaChannel counts new updates in a channel since anchor.
func (s *Service) deltaChannel(ctx context.Context, channelID string, since time.Time) (int, string, error) {
	var count int
	var newestID *string
	err := s.pgStore.DB().QueryRow(ctx, `
		SELECT COUNT(*), (SELECT id::text FROM channel_updates WHERE channel_id = $1 AND status = 'published' AND published_at > $2 ORDER BY published_at DESC LIMIT 1)
		FROM channel_updates
		WHERE channel_id = $1 AND status = 'published' AND published_at > $2
	`, channelID, since).Scan(&count, &newestID)
	if err != nil {
		slog.Warn("delta channel query failed", "channel_id", channelID, "error", err)
		return 0, "", err
	}
	newest := ""
	if newestID != nil {
		newest = *newestID
	}
	return count, newest, nil
}

// deltaCommunity counts new posts in a community since anchor.
func (s *Service) deltaCommunity(ctx context.Context, communityID string, since time.Time) (int, string, error) {
	var count int
	var newestID *string
	err := s.pgStore.DB().QueryRow(ctx, `
		SELECT COUNT(*), (SELECT id::text FROM community_posts WHERE community_id = $1 AND status = 'published' AND created_at > $2 ORDER BY created_at DESC LIMIT 1)
		FROM community_posts
		WHERE community_id = $1 AND status = 'published' AND created_at > $2
	`, communityID, since).Scan(&count, &newestID)
	if err != nil {
		slog.Warn("delta community query failed", "community_id", communityID, "error", err)
		return 0, "", err
	}
	newest := ""
	if newestID != nil {
		newest = *newestID
	}
	return count, newest, nil
}

// deltaCommunitySpace counts new posts in a community space since anchor.
func (s *Service) deltaCommunitySpace(ctx context.Context, spaceID string, since time.Time) (int, string, error) {
	var count int
	var newestID *string
	err := s.pgStore.DB().QueryRow(ctx, `
		SELECT COUNT(*), (SELECT id::text FROM community_posts WHERE space_id = $1 AND status = 'published' AND created_at > $2 ORDER BY created_at DESC LIMIT 1)
		FROM community_posts
		WHERE space_id = $1 AND status = 'published' AND created_at > $2
	`, spaceID, since).Scan(&count, &newestID)
	if err != nil {
		slog.Warn("delta community_space query failed", "space_id", spaceID, "error", err)
		return 0, "", err
	}
	newest := ""
	if newestID != nil {
		newest = *newestID
	}
	return count, newest, nil
}

// deltaContentType counts new items of a specific content type in the user's timeline.
func (s *Service) deltaContentType(ctx context.Context, userID uuid.UUID, contentType string, since time.Time) (int, string, error) {
	items, err := s.scyllaStore.GetHomeTimelineByContentType(ctx, userID, contentType, deltaMaxCount+1)
	if err != nil {
		return 0, "", err
	}

	count := 0
	var newest string
	for _, item := range items {
		if item.CreatedAt.After(since) {
			count++
			if newest == "" {
				newest = item.PostID.String()
			}
		}
	}
	return count, newest, nil
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
