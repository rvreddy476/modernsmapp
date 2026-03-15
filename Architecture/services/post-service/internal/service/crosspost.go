package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/atpost/post-service/internal/engagement"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// CreateCrosspost creates a cross-post link from a source post to a target module (e.g., postbook).
// It creates an embed post in the target module and links the two via crosspost_links.
// Rate limited to 5/hour per user.
func (s *Service) CreateCrosspost(ctx context.Context, sourcePostID, userID uuid.UUID, targetModule string) (*postgres.CrosspostLink, error) {
	// Validate target module
	if targetModule != "postbook" {
		return nil, fmt.Errorf("invalid target module: only 'postbook' is supported")
	}

	// Rate limit: 5 crossposts per hour per user
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:crosspost:%s", userID), engagement.CrosspostLimitPerHour, time.Hour) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}

	// Fetch source post and verify ownership
	source, err := s.pgStore.GetPost(ctx, sourcePostID)
	if err != nil {
		return nil, fmt.Errorf("failed to get source post: %w", err)
	}
	if source == nil {
		return nil, fmt.Errorf("source post not found")
	}
	if source.AuthorID != userID {
		return nil, fmt.Errorf("FORBIDDEN")
	}

	// Verify source is a video/flick (not already an embed or text post)
	switch source.ContentType {
	case "video", "long_video", "flick", "reel":
		// valid source types
	default:
		return nil, fmt.Errorf("cannot crosspost content type '%s'", source.ContentType)
	}

	// Check for existing active crosspost
	existing, err := s.pgStore.GetCrosspostLink(ctx, sourcePostID, targetModule)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing crosspost: %w", err)
	}
	if existing != nil {
		return nil, fmt.Errorf("crosspost already exists for this post and target module")
	}

	// Create crosspost link + embed post (transactional)
	link, err := s.pgStore.CreateCrosspostLink(ctx, source, targetModule)
	if err != nil {
		return nil, fmt.Errorf("failed to create crosspost link: %w", err)
	}

	// Publish PostCreated event for the embed post (so feed-service can pick it up)
	if s.producer != nil {
		embedContentType := "video_embed"
		if source.ContentType == "flick" || source.ContentType == "reel" {
			embedContentType = "flick_embed"
		}
		go func() {
			if err := s.producer.PublishPostCreated(context.Background(), link.TargetPostID, userID, source.Text, "public", embedContentType, 0); err != nil {
				log.Printf("Warning: failed to publish embed post created event: %v", err)
			}
		}()
	}

	return link, nil
}

// RemoveCrosspost soft-deletes a crosspost link and its target embed post.
func (s *Service) RemoveCrosspost(ctx context.Context, crosspostID, userID uuid.UUID) error {
	// Fetch the crosspost link to verify ownership
	link, err := s.pgStore.GetCrosspostLinkByID(ctx, crosspostID)
	if err != nil {
		return fmt.Errorf("failed to get crosspost link: %w", err)
	}
	if link == nil {
		return fmt.Errorf("crosspost not found")
	}

	// Verify ownership via source post
	source, err := s.pgStore.GetPost(ctx, link.SourcePostID)
	if err != nil {
		return fmt.Errorf("failed to get source post: %w", err)
	}
	if source == nil || source.AuthorID != userID {
		return fmt.Errorf("FORBIDDEN")
	}

	// Soft-delete link + embed post
	if err := s.pgStore.SoftDeleteCrosspostLink(ctx, crosspostID); err != nil {
		return fmt.Errorf("failed to remove crosspost: %w", err)
	}

	// Publish crosspost.removed event
	if s.producer != nil {
		go func() {
			if err := s.producer.PublishCrosspostRemoved(context.Background(), crosspostID, link.SourcePostID, link.SourceModule, link.TargetPostID); err != nil {
				log.Printf("Warning: failed to publish crosspost removed event: %v", err)
			}
		}()
	}

	return nil
}

// ListCrossposts returns all active crosspost links for a source post.
func (s *Service) ListCrossposts(ctx context.Context, sourcePostID uuid.UUID) ([]postgres.CrosspostLink, error) {
	return s.pgStore.ListCrosspostLinks(ctx, sourcePostID)
}

// ListCrosspostsByUser returns all active crosspost links for a user's posts (paginated).
func (s *Service) ListCrosspostsByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]postgres.CrosspostLink, int64, error) {
	return s.pgStore.ListCrosspostsByUser(ctx, userID, limit, offset)
}
