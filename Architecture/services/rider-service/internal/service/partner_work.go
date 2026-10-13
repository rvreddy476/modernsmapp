package service

import (
	"context"
	"github.com/atpost/shared/partnerwork"
	"github.com/google/uuid"
)

func (s *Service) PartnerWorkState(ctx context.Context, user uuid.UUID) (partnerwork.State, error) {
	return partnerwork.Snapshot(ctx, s.store.DB(), user)
}
