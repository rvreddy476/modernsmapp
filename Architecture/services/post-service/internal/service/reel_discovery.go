package service

import (
	"context"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// GetTrendingHashtags returns trending hashtags from recent reels.
func (s *Service) GetTrendingHashtags(ctx context.Context, limit, sinceDays int) ([]postgres.TrendingHashtag, error) {
	return s.pgStore.GetTrendingHashtags(ctx, limit, sinceDays)
}

// LookupSlugRedirect finds the current reel_id for an old slug.
func (s *Service) LookupSlugRedirect(ctx context.Context, oldSlug string) (*uuid.UUID, *string, error) {
	return s.pgStore.LookupSlugRedirect(ctx, oldSlug)
}

// GetFlaggedReels returns reels pending moderation review.
func (s *Service) GetFlaggedReels(ctx context.Context, limit, offset int) ([]postgres.ModerationReview, error) {
	return s.pgStore.GetFlaggedReels(ctx, limit, offset)
}

// GetReelModerationReviews returns all moderation reviews for a reel.
func (s *Service) GetReelModerationReviews(ctx context.Context, reelID uuid.UUID) ([]postgres.ModerationReview, error) {
	return s.pgStore.GetModerationReviewsByReel(ctx, reelID)
}

// AutoModeratePendingReel creates an automatic moderation review for a newly published reel.
// This is a placeholder that marks reels as "approved" by default. In production,
// this would integrate with an AI content safety API.
func (s *Service) AutoModeratePendingReel(ctx context.Context, reelID uuid.UUID) error {
	confidence := 1.0
	reviewerType := "auto"
	decision := "approved"
	review := &postgres.ModerationReview{
		ReelID:       reelID,
		ReviewerType: reviewerType,
		Decision:     decision,
		Confidence:   &confidence,
	}
	return s.pgStore.InsertModerationReview(ctx, review)
}
