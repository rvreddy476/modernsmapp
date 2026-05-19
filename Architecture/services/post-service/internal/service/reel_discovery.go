package service

import (
	"context"
	"log"

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

// RecordVideoModeration writes an automatic moderation review for a
// freshly-created video post, giving every reel / video an auditable
// verdict row. It is called synchronously from CreatePost.
//
// v1 decisioning is heuristic: it mirrors the text-spam verdict that
// CreatePost already computed (passed in as reviewStatus). The marked
// HOOK below is the single integration point for a visual content-safety
// classifier (CSAM / nudity / graphic-violence detection run against the
// transcoded video). Until that provider is integrated, video frames are
// not scanned — a known, deliberate gap, not a silent omission.
func (s *Service) RecordVideoModeration(ctx context.Context, postID uuid.UUID, reviewStatus string, spamScore float64) {
	decision := "approved"
	switch reviewStatus {
	case "flagged":
		decision = "flagged"
	case "rejected":
		decision = "rejected"
	}

	// HOOK: integrate a visual content-safety classifier here. Run it
	// against the transcoded video and override `decision` (e.g. to
	// "rejected" on a CSAM hit), updating the post's review_status to
	// match so the GetPost / GetPostsByIDs gate hides it.

	confidence := 1.0
	if spamScore > 0 {
		confidence = spamScore
	}
	review := &postgres.ModerationReview{
		ReelID:       postID,
		ReviewerType: "auto",
		Decision:     decision,
		Confidence:   &confidence,
	}
	if err := s.pgStore.InsertModerationReview(ctx, review); err != nil {
		log.Printf("Warning: failed to record moderation review for post %s: %v", postID, err)
	}
}
