package service

import (
	"context"
	"fmt"
	"github.com/atpost/shared/ownedmedia"
	"github.com/google/uuid"
)

func (s *Service) AdminEvidence(ctx context.Context, id uuid.UUID) (*ownedmedia.Reference, error) {
	d, err := s.store.GetPartnerDocument(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.MediaID == nil {
		return nil, fmt.Errorf("no media")
	}
	p, err := s.store.GetPartner(ctx, d.PartnerID)
	if err != nil {
		return nil, err
	}
	return &ownedmedia.Reference{MediaID: *d.MediaID, OwnerID: p.UserID, Kind: d.DocumentType}, nil
}
