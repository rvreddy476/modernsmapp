package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrPostsByMediaUnavailable means the store is not wired, so the reverse
// lookup cannot be answered (the caller retries; it must not read "none").
var ErrPostsByMediaUnavailable = errors.New("posts by media: store not configured")

// PostIDsByMediaID returns the non-deleted posts that attach mediaID, in no
// particular order, whatever their visibility. Internal callers only
// (GET /v1/internal/posts/by-media/:mediaId): search-service uses it to find
// the post documents a media-level fact — published captions — belongs to.
func (s *Service) PostIDsByMediaID(ctx context.Context, mediaID uuid.UUID) ([]uuid.UUID, error) {
	if s == nil || s.pgStore == nil {
		return nil, ErrPostsByMediaUnavailable
	}
	return s.pgStore.PostIDsByMediaID(ctx, mediaID)
}
