package service

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ReelFeedItem represents a reel in the feed response.
type ReelFeedItem struct {
	*postgres.Post
	ViewerReaction string `json:"viewer_reaction,omitempty"`
	IsSaved        bool   `json:"is_saved"`
}

// GetReelFeed returns personalized reel candidates for a user.
// Uses a simple chronological feed with seen suppression via Redis set.
func (s *Service) GetReelFeed(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]ReelFeedItem, string, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	// Get candidate reels (public reels, ordered by recency).
	// Over-fetch by 10 to compensate for seen-suppression filtering.
	reels, nextCursor, err := s.pgStore.GetReelCandidates(ctx, limit+10, cursor)
	if err != nil {
		return nil, "", err
	}

	// Filter out seen reels
	seenKey := fmt.Sprintf("reel:seen:%s", userID.String())
	var filtered []*postgres.Post
	for _, reel := range reels {
		isSeen, _ := s.rdb.SIsMember(ctx, seenKey, reel.ID.String()).Result()
		if !isSeen {
			filtered = append(filtered, reel)
		}
		if len(filtered) >= limit {
			break
		}
	}

	// Mark filtered reels as seen with a 24-hour TTL
	if len(filtered) > 0 {
		members := make([]interface{}, len(filtered))
		for i, r := range filtered {
			members[i] = r.ID.String()
		}
		s.rdb.SAdd(ctx, seenKey, members...)
		s.rdb.Expire(ctx, seenKey, 24*time.Hour)
	}

	// Hydrate with viewer state (reaction + saved status)
	items := make([]ReelFeedItem, 0, len(filtered))
	for _, reel := range filtered {
		item := ReelFeedItem{Post: reel}
		if reaction, err := s.scyllaStore.GetReelReaction(ctx, reel.ID, userID); err == nil {
			item.ViewerReaction = reaction
		}
		if saved, err := s.scyllaStore.IsReelSaved(ctx, reel.ID, userID); err == nil {
			item.IsSaved = saved
		}
		items = append(items, item)
	}

	// Compute next cursor from last item
	outCursor := ""
	if len(filtered) >= limit && nextCursor != "" {
		outCursor = nextCursor
	}

	return items, outCursor, nil
}

// ResetReelSeenState clears a user's seen reel set (for "refresh feed").
func (s *Service) ResetReelSeenState(ctx context.Context, userID uuid.UUID) error {
	seenKey := fmt.Sprintf("reel:seen:%s", userID.String())
	return s.rdb.Del(ctx, seenKey).Err()
}
