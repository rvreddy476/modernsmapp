package service

import (
	"context"

	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/google/uuid"
)

// ReactToReel adds a reaction to a reel.
func (s *Service) ReactToReel(ctx context.Context, reelID, userID uuid.UUID, reaction string) error {
	return s.scyllaStore.ReactToReel(ctx, reelID, userID, reaction)
}

// UnreactToReel removes a reaction from a reel.
func (s *Service) UnreactToReel(ctx context.Context, reelID, userID uuid.UUID) error {
	return s.scyllaStore.UnreactToReel(ctx, reelID, userID)
}

// GetReelReaction returns the viewer's reaction for a reel.
func (s *Service) GetReelReaction(ctx context.Context, reelID, userID uuid.UUID) (string, error) {
	return s.scyllaStore.GetReelReaction(ctx, reelID, userID)
}

// AddReelComment adds a comment to a reel.
func (s *Service) AddReelComment(ctx context.Context, reelID, userID uuid.UUID, text string) (uuid.UUID, error) {
	return s.scyllaStore.AddReelComment(ctx, reelID, userID, text)
}

// ListReelComments returns comments for a reel.
func (s *Service) ListReelComments(ctx context.Context, reelID uuid.UUID, limit int) ([]scylla.Comment, error) {
	return s.scyllaStore.ListReelComments(ctx, reelID, limit)
}

// ShareReel records a reel share.
func (s *Service) ShareReel(ctx context.Context, reelID, userID uuid.UUID, shareType string) error {
	return s.scyllaStore.ShareReel(ctx, reelID, userID, shareType)
}

// SaveReel saves a reel to bookmarks.
func (s *Service) SaveReel(ctx context.Context, reelID, userID uuid.UUID) error {
	return s.scyllaStore.SaveReel(ctx, reelID, userID)
}

// UnsaveReel removes a reel from bookmarks.
func (s *Service) UnsaveReel(ctx context.Context, reelID, userID uuid.UUID) error {
	return s.scyllaStore.UnsaveReel(ctx, reelID, userID)
}

// IsReelSaved checks if a user saved a reel.
func (s *Service) IsReelSaved(ctx context.Context, reelID, userID uuid.UUID) (bool, error) {
	return s.scyllaStore.IsReelSaved(ctx, reelID, userID)
}

// ListSavedReels returns saved reel IDs.
func (s *Service) ListSavedReels(ctx context.Context, userID uuid.UUID, limit int) ([]string, error) {
	return s.scyllaStore.ListSavedReels(ctx, userID, limit)
}

// RecordReelView records a view and emits event.
func (s *Service) RecordReelView(ctx context.Context, reelID, viewerID uuid.UUID, sessionID string, watchedMs int64, surface string) error {
	if err := s.scyllaStore.RecordReelView(ctx, reelID, viewerID); err != nil {
		return err
	}
	s.EmitReelViewed(ctx, ReelViewedPayload{
		ReelID:    reelID.String(),
		ViewerID:  viewerID.String(),
		SessionID: sessionID,
		WatchedMs: watchedMs,
		Surface:   surface,
	})
	return nil
}

// GetReelCounts returns engagement counts for a reel.
func (s *Service) GetReelCounts(ctx context.Context, reelID uuid.UUID) (*scylla.ReelCounts, error) {
	return s.scyllaStore.GetReelCounts(ctx, reelID)
}

// BatchGetReelCounts returns engagement counts for multiple reels.
func (s *Service) BatchGetReelCounts(ctx context.Context, reelIDs []uuid.UUID) (map[string]*scylla.ReelCounts, error) {
	return s.scyllaStore.BatchGetReelCounts(ctx, reelIDs)
}

// GetUserReelLikes returns reel IDs liked by a user.
func (s *Service) GetUserReelLikes(ctx context.Context, userID uuid.UUID, limit int) ([]string, error) {
	return s.scyllaStore.GetUserReelLikes(ctx, userID, limit)
}
