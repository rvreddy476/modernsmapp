package service

import (
	"context"
	"github.com/atpost/shared/ownedmedia"
	"github.com/google/uuid"
)

func (s *Service) AdminEvidence(ctx context.Context, delivery bool, parent, id uuid.UUID) (*ownedmedia.Reference, error) {
	st, ok := s.store.(interface {
		AdminEvidence(context.Context, bool, uuid.UUID, uuid.UUID) (*ownedmedia.Reference, error)
	})
	if !ok {
		return nil, ownedmedia.ErrUnavailable
	}
	return st.AdminEvidence(ctx, delivery, parent, id)
}

func (s *Service) AdminRestaurantEvidenceList(ctx context.Context, id uuid.UUID) ([]map[string]any, error) {
	st, ok := s.store.(interface {
		AdminRestaurantEvidenceList(context.Context, uuid.UUID) ([]map[string]any, error)
	})
	if !ok {
		return nil, ownedmedia.ErrUnavailable
	}
	return st.AdminRestaurantEvidenceList(ctx, id)
}
