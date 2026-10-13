package service

import (
	"context"
	"github.com/atpost/shared/ownedmedia"
	"github.com/google/uuid"
)

func (s *Service) AdminEvidence(ctx context.Context, id uuid.UUID) (*ownedmedia.Reference, error) {
	st, err := s.proStore(ctx)
	if err != nil {
		return nil, err
	}
	d, err := st.DocumentByID(ctx, id)
	if err != nil {
		return nil, err
	}
	p, err := st.ProfessionalByID(ctx, d.ProID)
	if err != nil {
		return nil, err
	}
	mid, err := uuid.Parse(d.MediaID)
	if err != nil {
		return nil, err
	}
	return &ownedmedia.Reference{MediaID: mid, OwnerID: p.UserID, Kind: d.Kind}, nil
}
