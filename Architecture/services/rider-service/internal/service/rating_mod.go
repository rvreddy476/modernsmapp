package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

// AdminHideRideRating soft-hides a 1-star rating that violates ToS.
// `visibility` is 'hidden' | 'flagged' | 'public' (last one reverts).
func (s *Service) AdminHideRideRating(ctx context.Context, adminID, rideID uuid.UUID, visibility string) error {
	return s.store.HideRideRating(ctx, adminID, rideID, visibility)
}

// PartnerRespondToRating lets a partner reply to their rating. Length-
// capped at 1000 chars to stay readable and prevent abuse.
func (s *Service) PartnerRespondToRating(ctx context.Context, partnerUserID, rideID uuid.UUID, response string) error {
	if len(response) > 1000 {
		return fmt.Errorf("invalid: response longer than 1000 chars")
	}
	if response == "" {
		return fmt.Errorf("invalid: response required")
	}
	partner, err := s.store.GetPartnerByUserID(ctx, partnerUserID)
	if err != nil {
		if errors.Is(err, store.ErrPartnerNotFound) {
			return fmt.Errorf("not_found: partner")
		}
		return err
	}
	return s.store.AddPartnerResponse(ctx, partner.ID, rideID, response)
}
