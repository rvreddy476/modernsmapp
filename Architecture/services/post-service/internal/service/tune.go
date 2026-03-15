package service

import (
	"context"

	"github.com/google/uuid"
)

func (s *Service) CreateTune(ctx context.Context, userID, postID uuid.UUID) error {
	return s.pgStore.CreateTune(ctx, userID, postID)
}

func (s *Service) DeleteTune(ctx context.Context, userID, postID uuid.UUID) error {
	return s.pgStore.DeleteTune(ctx, userID, postID)
}
