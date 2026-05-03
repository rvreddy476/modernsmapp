package service

import (
	"context"
	"fmt"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// validVisibility gates the spec §10 enum.
func validVisibility(v string) bool {
	switch v {
	case "public", "match_only", "sparked_only":
		return true
	}
	return false
}

// ListPhotos returns the user's photos.
func (s *Service) ListPhotos(ctx context.Context, userID uuid.UUID) ([]store.Photo, error) {
	return s.store.ListPhotos(ctx, userID)
}

// CreatePhoto validates input and inserts the photo.
func (s *Service) CreatePhoto(ctx context.Context, userID uuid.UUID, p store.CreatePhotoParams) (*store.Photo, error) {
	if p.MediaID == uuid.Nil {
		return nil, fmt.Errorf("invalid: media_id is required")
	}
	if p.Visibility != "" && !validVisibility(p.Visibility) {
		return nil, fmt.Errorf("invalid: visibility must be one of public|match_only|sparked_only")
	}
	return s.store.CreatePhoto(ctx, userID, p)
}

// UpdatePhoto applies the partial update.
func (s *Service) UpdatePhoto(ctx context.Context, userID, photoID uuid.UUID, p store.UpdatePhotoParams) (*store.Photo, error) {
	if p.Visibility != nil && !validVisibility(*p.Visibility) {
		return nil, fmt.Errorf("invalid: visibility must be one of public|match_only|sparked_only")
	}
	return s.store.UpdatePhoto(ctx, userID, photoID, p)
}

// DeletePhoto removes the photo.
func (s *Service) DeletePhoto(ctx context.Context, userID, photoID uuid.UUID) error {
	return s.store.DeletePhoto(ctx, userID, photoID)
}
