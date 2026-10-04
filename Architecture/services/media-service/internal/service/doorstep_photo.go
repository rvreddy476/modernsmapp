package service

import (
	"context"
	"github.com/google/uuid"
)

func (s *Service) PrepareDoorstepPhoto(ctx context.Context, id, owner uuid.UUID) (bool, error) {
	return s.pgStore.PrepareDoorstepPhoto(ctx, id, owner)
}
