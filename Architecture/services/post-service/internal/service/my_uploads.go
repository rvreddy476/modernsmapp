package service

import (
	"context"
	"fmt"
	"log"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// UploadDetail extends PostDetail with optional video metadata.
type UploadDetail struct {
	PostDetail
	VideoMetadata *postgres.VideoMetadata `json:"video_metadata,omitempty"`
}

// GetMyVideos returns the user's video and long_video uploads with video metadata.
func (s *Service) GetMyVideos(ctx context.Context, authorID uuid.UUID, limit int, cursor string) ([]UploadDetail, string, error) {
	posts, nextCursor, err := s.pgStore.GetUploadsByContentTypes(ctx, authorID, []string{"video", "long_video"}, limit, cursor)
	if err != nil {
		return nil, "", err
	}
	return s.enrichUploads(ctx, posts), nextCursor, nil
}

// GetMyFlicks returns the user's flick and reel uploads with video metadata.
func (s *Service) GetMyFlicks(ctx context.Context, authorID uuid.UUID, limit int, cursor string) ([]UploadDetail, string, error) {
	posts, nextCursor, err := s.pgStore.GetUploadsByContentTypes(ctx, authorID, []string{"flick", "reel"}, limit, cursor)
	if err != nil {
		return nil, "", err
	}
	return s.enrichUploads(ctx, posts), nextCursor, nil
}

// GetMyPosts returns the user's text/image posts.
func (s *Service) GetMyPosts(ctx context.Context, authorID uuid.UUID, limit int, cursor string) ([]PostDetail, string, error) {
	posts, nextCursor, err := s.pgStore.GetUploadsByContentTypes(ctx, authorID, []string{"post", "image"}, limit, cursor)
	if err != nil {
		return nil, "", err
	}

	details := make([]PostDetail, len(posts))
	for i, p := range posts {
		post := p
		counts, _ := s.scyllaStore.GetCounts(ctx, p.ID)
		details[i] = PostDetail{Post: &post, Counts: counts}
	}
	return details, nextCursor, nil
}

// GetUploadCounts returns counts of videos, flicks, and posts for a user.
func (s *Service) GetUploadCounts(ctx context.Context, authorID uuid.UUID) (videos, flicks, posts int64, err error) {
	return s.pgStore.CountUploadsByContentTypes(ctx, authorID)
}

// DeleteUploadCascade deletes a post and all its crosspost links + embed posts.
func (s *Service) DeleteUploadCascade(ctx context.Context, postID, authorID uuid.UUID) error {
	// Fetch the post first for event data
	source, err := s.pgStore.GetPost(ctx, postID)
	if err != nil {
		return fmt.Errorf("failed to get post: %w", err)
	}
	if source == nil {
		return fmt.Errorf("post not found")
	}
	if source.AuthorID != authorID {
		return fmt.Errorf("FORBIDDEN")
	}

	cascadeCount, err := s.pgStore.DeleteUploadCascade(ctx, postID, authorID)
	if err != nil {
		return fmt.Errorf("failed to delete upload: %w", err)
	}

	// Publish upload.deleted event
	if s.producer != nil && cascadeCount >= 0 {
		go func() {
			if err := s.producer.PublishUploadDeleted(ctx, postID, authorID, source.ContentType); err != nil {
				log.Printf("Warning: failed to publish upload deleted event: %v", err)
			}
		}()
	}

	return nil
}

// enrichUploads adds engagement counts and video metadata to posts.
func (s *Service) enrichUploads(ctx context.Context, posts []postgres.Post) []UploadDetail {
	if len(posts) == 0 {
		return nil
	}

	// Collect post IDs for batch lookups
	postIDs := make([]uuid.UUID, len(posts))
	for i, p := range posts {
		postIDs[i] = p.ID
	}

	// Batch-fetch video metadata
	videoMeta, _ := s.pgStore.BatchGetVideoMetadata(ctx, postIDs)

	details := make([]UploadDetail, len(posts))
	for i, p := range posts {
		post := p
		counts, _ := s.scyllaStore.GetCounts(ctx, p.ID)
		details[i] = UploadDetail{
			PostDetail:    PostDetail{Post: &post, Counts: counts},
			VideoMetadata: videoMeta[p.ID],
		}
	}
	return details
}
