package service

import (
	"context"
	"fmt"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

func (s *Service) CreateFlickSeries(ctx context.Context, creatorID uuid.UUID, title, description string) (*postgres.FlickSeries, error) {
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}
	return s.pgStore.CreateFlickSeries(ctx, creatorID, title, description)
}

func (s *Service) GetFlickSeries(ctx context.Context, seriesID uuid.UUID) (*postgres.FlickSeries, error) {
	fs, err := s.pgStore.GetFlickSeries(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	if fs == nil {
		return nil, fmt.Errorf("series not found")
	}
	return fs, nil
}

func (s *Service) ListFlickSeriesByCreator(ctx context.Context, creatorID uuid.UUID, limit, offset int) ([]postgres.FlickSeries, error) {
	return s.pgStore.ListFlickSeriesByCreator(ctx, creatorID, limit, offset)
}

func (s *Service) AddEpisodeToSeries(ctx context.Context, userID, seriesID, postID uuid.UUID, episodeNum int) (*postgres.FlickSeriesItem, error) {
	fs, err := s.pgStore.GetFlickSeries(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	if fs == nil {
		return nil, fmt.Errorf("series not found")
	}
	if fs.CreatorID != userID {
		return nil, fmt.Errorf("forbidden: you do not own this series")
	}
	return s.pgStore.AddEpisodeToSeries(ctx, seriesID, postID, episodeNum)
}

func (s *Service) GetSeriesEpisodes(ctx context.Context, seriesID uuid.UUID) ([]postgres.FlickSeriesItem, error) {
	return s.pgStore.GetSeriesEpisodes(ctx, seriesID)
}

func (s *Service) FollowSeries(ctx context.Context, userID, seriesID uuid.UUID) error {
	return s.pgStore.FollowSeries(ctx, seriesID, userID)
}

func (s *Service) UnfollowSeries(ctx context.Context, userID, seriesID uuid.UUID) error {
	return s.pgStore.UnfollowSeries(ctx, seriesID, userID)
}

// GetRemixToken returns metadata about a source post that allows remix creation.
// It checks the post's remix_setting: only posts with 'allow' or 'allow_audio_only' may be remixed.
func (s *Service) GetRemixToken(ctx context.Context, postID uuid.UUID) (map[string]interface{}, error) {
	post, err := s.pgStore.GetPost(ctx, postID)
	if err != nil || post == nil {
		return nil, fmt.Errorf("post not found")
	}
	if post.RemixSetting == "disallow" {
		return nil, fmt.Errorf("this post does not allow remixes")
	}
	return map[string]interface{}{
		"source_post_id": postID,
		"allow_remix":    true,
		"remix_setting":  post.RemixSetting,
		"post_type":      post.ContentType,
	}, nil
}
