package service

import (
	"context"
	"fmt"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// CreateVideoSeries creates a new video series owned by creatorID.
func (s *Service) CreateVideoSeries(ctx context.Context, vs *postgres.VideoSeries) error {
	if vs.Title == "" {
		return fmt.Errorf("title is required")
	}
	return s.pgStore.CreateVideoSeries(ctx, vs)
}

// GetVideoSeries retrieves a video series by ID, returning an error if not found.
func (s *Service) GetVideoSeries(ctx context.Context, id uuid.UUID) (*postgres.VideoSeries, error) {
	vs, err := s.pgStore.GetVideoSeries(ctx, id)
	if err != nil {
		return nil, err
	}
	if vs == nil {
		return nil, fmt.Errorf("video series not found")
	}
	return vs, nil
}

// ListVideoSeriesByCreator returns paginated video series for the given creator.
func (s *Service) ListVideoSeriesByCreator(ctx context.Context, creatorID uuid.UUID, limit, offset int) ([]postgres.VideoSeries, error) {
	return s.pgStore.ListVideoSeriesByCreator(ctx, creatorID, limit, offset)
}

// AddEpisodeToVideoSeries adds an episode after verifying the caller owns the series.
func (s *Service) AddEpisodeToVideoSeries(ctx context.Context, callerID, seriesID, postID uuid.UUID, episodeNum int, title *string) (*postgres.VideoSeriesEpisode, error) {
	vs, err := s.pgStore.GetVideoSeries(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	if vs == nil {
		return nil, fmt.Errorf("video series not found")
	}
	if vs.CreatorID != callerID {
		return nil, fmt.Errorf("forbidden: you do not own this video series")
	}
	return s.pgStore.AddEpisodeToVideoSeries(ctx, seriesID, postID, episodeNum, title)
}

// GetVideoSeriesEpisodes returns all episodes for a video series.
func (s *Service) GetVideoSeriesEpisodes(ctx context.Context, seriesID uuid.UUID) ([]postgres.VideoSeriesEpisode, error) {
	return s.pgStore.GetVideoSeriesEpisodes(ctx, seriesID)
}
