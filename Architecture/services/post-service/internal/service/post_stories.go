// post_stories.go holds the thin story calls that pass through to the
// store (get, feeds, by author, delete, view, cleanup) and the
// CreateStoryInput shape. Moved out of post.go unchanged.

package service

import (
	"context"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ============================================================
// Stories
// ============================================================

// CreateStoryInput holds fields for creating a story.
type CreateStoryInput struct {
	AuthorID uuid.UUID
	// MediaID is the canonical asset. M4-P0-4 removed the MediaURL field
	// entirely rather than deprecating it: a field that still exists is a
	// field a caller can still populate.
	MediaID        uuid.UUID
	MediaType      string
	Caption        string
	Visibility     string
	IsHighlight    bool
	HighlightGroup *string
	IdempotencyKey string
}

// CreateStory creates a new ephemeral story with 24h expiry.

// GetStory returns a single story by ID.
func (s *Service) GetStory(ctx context.Context, storyID uuid.UUID) (*postgres.Story, error) {
	return s.pgStore.GetStory(ctx, storyID)
}

// GetStoriesFeed returns stories from followed users. Caller provides followed user IDs.
func (s *Service) GetStoriesFeed(ctx context.Context, followedUserIDs []uuid.UUID) ([]postgres.Story, error) {
	return s.pgStore.GetStoriesFeed(ctx, followedUserIDs)
}

// GetStoriesFeedForUser resolves the user's following graph and returns active stories.
func (s *Service) GetStoriesFeedForUser(ctx context.Context, userID uuid.UUID) ([]postgres.Story, error) {
	following, err := s.fetchFollowing(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.pgStore.GetStoriesFeed(ctx, following)
}

// GetStoriesByAuthor returns active stories for a specific author.
func (s *Service) GetStoriesByAuthor(ctx context.Context, authorID uuid.UUID) ([]postgres.Story, error) {
	return s.pgStore.GetStoriesByAuthor(ctx, authorID)
}

// DeleteStory removes a story.
func (s *Service) DeleteStory(ctx context.Context, storyID, authorID uuid.UUID) error {
	return s.pgStore.DeleteStory(ctx, storyID, authorID)
}

// ViewStory increments the view count of a story via the sharded
// Redis counter; PG snapshot is materialised every ~10s by the flush
// worker in cmd/server/main.go. Hot at viral scale (1M+ views/24h on
// one row) — without sharding every viewer write contended on the
// same UPDATE.
func (s *Service) ViewStory(ctx context.Context, storyID uuid.UUID) error {
	return s.adjustStoryViewCount(ctx, storyID)
}

// CleanupExpiredStories removes stories past their expiry. Called by cron.
func (s *Service) CleanupExpiredStories(ctx context.Context) (int64, error) {
	return s.pgStore.CleanupExpiredStories(ctx)
}
