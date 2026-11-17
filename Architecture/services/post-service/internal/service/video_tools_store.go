package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// videoToolStore is the storage slice behind the creator tools in
// post_video_tools.go (trim, category override, cover frame, publish,
// distribution): an interface so each owner check and the cover-frame
// thumbnail rule are testable without a database (lane post-service-2,
// 2026-11-18). *postgres.Store implements it; videoToolsSeam replaces it in
// tests.
type videoToolStore interface {
	GetPostAuthorID(ctx context.Context, postID uuid.UUID) (uuid.UUID, error)
	GetPost(ctx context.Context, postID uuid.UUID) (*postgres.Post, error)
	GetVideoMetadata(ctx context.Context, postID uuid.UUID) (*postgres.VideoMetadata, error)
	UpdateTrim(ctx context.Context, postID uuid.UUID, startMs int, endMs *int) error
	UpdateFinalCategory(ctx context.Context, postID uuid.UUID, category string) error
	UpdateVideoMetadata(ctx context.Context, vm *postgres.VideoMetadata) error
	UpdatePostCoverMedia(ctx context.Context, postID uuid.UUID, coverMediaID *uuid.UUID) error
	UpdateDistribution(ctx context.Context, postID, authorID uuid.UUID, policy json.RawMessage,
		buildEvent func(rev int64) (eventType string, payload interface{})) (int64, error)
}

// videoToolSeams are the test stand-ins for the creator tools: the store
// slice, and PublishScheduled behind PublishVideo (nil = the real flip).
type videoToolSeams struct {
	videoToolsSeam       videoToolStore
	publishScheduledSeam func(ctx context.Context, postID uuid.UUID, authorID *uuid.UUID, dueOnly bool) (bool, error)
	// flickSeriesWrites stands in for the reels-series episode add's store
	// (series.go, lv-74).
	flickSeriesWrites flickSeriesWriter
}

// videoTools is the seam, else the Postgres store; nil when neither is wired
// (every tool then fails closed with ErrAuthoringStoreUnavailable).
func (s *Service) videoTools() videoToolStore {
	if s.videoToolsSeam != nil {
		return s.videoToolsSeam
	}
	if s.pgStore != nil {
		return s.pgStore
	}
	return nil
}

// ErrInvalidCoverThumbnail: cover-frame's thumbnail_url is not a reference to
// platform media (a bare media id or /v1/media/<id>/serve). 400.
var ErrInvalidCoverThumbnail = errors.New("thumbnail_url must be a media id of your own image")

// normalizeCoverThumbnail is lv-28: cover-frame's thumbnail_url is drawn on
// the watch page and on other creators' related and end-screen cards, so it
// takes the chapter-thumbnail and playlist-cover rule (tube_audit.go): the
// caller's own finished, moderated image by media id, stored as its serve
// path. It used to store any string, so an off-platform image skipped
// moderation and worked as a tracking pixel on every viewer. An empty string
// clears it (nil stored).
func (s *Service) normalizeCoverThumbnail(ctx context.Context, ownerID uuid.UUID, raw string) (*string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	id, ok := parsePlatformMediaRef(raw)
	if !ok {
		return nil, ErrInvalidCoverThumbnail
	}
	if err := s.verifyOwnImage(ctx, ownerID, id); err != nil {
		return nil, err
	}
	out := mediaServeURL(id)
	return &out, nil
}

// errVideoToolsUnavailable wraps a missing store as the tools' fail-closed
// answer.
func errVideoToolsUnavailable() error {
	return fmt.Errorf("video tools: %w", ErrAuthoringStoreUnavailable)
}
