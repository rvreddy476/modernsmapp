package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) CreateFlickSeries(ctx context.Context, creatorID uuid.UUID, title, description string) (*postgres.FlickSeries, error) {
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}
	return s.pgStore.CreateFlickSeries(ctx, creatorID, title, description)
}

// GetFlickSeries is GET /v1/series/:id. lv-74: behind the creator's account
// gate (canViewAuthor) like every other read of their content; a series the
// viewer may not see is the same "series not found" as a missing one.
func (s *Service) GetFlickSeries(ctx context.Context, seriesID uuid.UUID, viewerID *uuid.UUID) (*postgres.FlickSeries, error) {
	if s.flickSeries == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	fs, err := s.flickSeries.GetFlickSeries(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	if fs == nil || !s.canViewAuthor(ctx, viewerID, fs.CreatorID) {
		return nil, fmt.Errorf("series not found")
	}
	return fs, nil
}

// ListFlickSeriesByCreator is GET /v1/creators/:id/series, behind the same
// account gate (lv-74): a creator the viewer may not see lists nothing.
func (s *Service) ListFlickSeriesByCreator(ctx context.Context, creatorID uuid.UUID, viewerID *uuid.UUID, limit, offset int) ([]postgres.FlickSeries, error) {
	if !s.canViewAuthor(ctx, viewerID, creatorID) {
		return []postgres.FlickSeries{}, nil
	}
	return s.pgStore.ListFlickSeriesByCreator(ctx, creatorID, limit, offset)
}

// flickSeriesWriter is the reels-series episode add's store slice
// (*postgres.Store); flickSeriesWrites replaces it in tests.
type flickSeriesWriter interface {
	GetPostAuthorID(ctx context.Context, postID uuid.UUID) (uuid.UUID, error)
	AddEpisodeToSeries(ctx context.Context, seriesID, postID uuid.UUID, episodeNum int) (*postgres.FlickSeriesItem, error)
}

func (s *Service) flickSeriesWriterStore() flickSeriesWriter {
	if s.flickSeriesWrites != nil {
		return s.flickSeriesWrites
	}
	if s.pgStore != nil {
		return s.pgStore
	}
	return nil
}

// ErrFlickEpisodeNumberTaken: the episode number is held by another post.
var ErrFlickEpisodeNumberTaken = errors.New("episode number already taken in this series")

// AddEpisodeToSeries is POST /v1/series/:id/episodes, the video-series rules
// (lv-74): the series owner only; the episode is the owner's own post (a
// stranger's reel used to be accepted, misattributing it); an occupied
// number is refused, never overwritten.
func (s *Service) AddEpisodeToSeries(ctx context.Context, userID, seriesID, postID uuid.UUID, episodeNum int) (*postgres.FlickSeriesItem, error) {
	writes := s.flickSeriesWriterStore()
	if s.flickSeries == nil || writes == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	fs, err := s.flickSeries.GetFlickSeries(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	if fs == nil {
		return nil, fmt.Errorf("series not found")
	}
	if fs.CreatorID != userID {
		return nil, fmt.Errorf("forbidden: you do not own this series")
	}
	author, err := writes.GetPostAuthorID(ctx, postID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("post not found")
		}
		return nil, fmt.Errorf("lookup episode author: %w", err)
	}
	if author != userID {
		return nil, fmt.Errorf("forbidden: an episode must be your own post")
	}
	item, err := writes.AddEpisodeToSeries(ctx, seriesID, postID, episodeNum)
	if errors.Is(err, postgres.ErrFlickEpisodeNumberTaken) {
		return nil, ErrFlickEpisodeNumberTaken
	}
	return item, err
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
