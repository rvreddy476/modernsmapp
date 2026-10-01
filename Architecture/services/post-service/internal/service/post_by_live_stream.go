package service

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/atpost/post-service/internal/store/postgres"
)

// ErrLiveStreamPostUnavailable means the store is not wired, so the lookup
// cannot be answered (the caller retries; it must not read "no post").
var ErrLiveStreamPostUnavailable = errors.New("post by live stream: store not configured")

// LiveStreamPostRef returns the recording post of a live stream, deleted or
// not, or nil when the stream has none (yet). Internal callers only
// (GET /v1/internal/posts/by-live-stream/:streamId): live-service-v2 stores
// the id as the stream's recording_post_id.
func (s *Service) LiveStreamPostRef(ctx context.Context, streamID uuid.UUID) (*postgres.LiveStreamPostRef, error) {
	if s == nil || s.pgStore == nil {
		return nil, ErrLiveStreamPostUnavailable
	}
	return s.pgStore.GetLiveStreamPostRef(ctx, streamID)
}
