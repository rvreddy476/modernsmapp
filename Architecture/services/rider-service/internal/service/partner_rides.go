package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

func (s *Service) ListPartnerRides(ctx context.Context, user uuid.UUID, limit int) ([]store.Ride, error) {
	return s.store.ListPartnerRides(ctx, user, limit)
}
func (s *Service) CreatePartnerRideSupport(ctx context.Context, user, ride uuid.UUID, category, description string) (*store.Complaint, error) {
	category = strings.TrimSpace(category)
	description = strings.TrimSpace(description)
	if !store.IsValidComplaintCategory(category) || description == "" || len(description) > 4000 {
		return nil, fmt.Errorf("invalid: choose a support category and enter a description up to 4000 characters")
	}
	c, err := s.store.CreatePartnerSupport(ctx, user, ride, category, description)
	if errors.Is(err, store.ErrRideNotFound) {
		return nil, fmt.Errorf("not_found: ride")
	}
	return c, err
}

// Partner readback never opens the customer's sealed start code. Approval and
// subscription changes must not prevent a partner from recovering accepted work.
func (s *Service) GetPartnerActiveRide(ctx context.Context, userID uuid.UUID) (*store.Ride, error) {
	p, err := s.store.GetPartnerByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("partner lookup: %w", err)
	}
	r, err := s.store.GetActiveRideForPartner(ctx, p.ID)
	if errors.Is(err, store.ErrRideNotFound) {
		return nil, nil
	}
	return r, err
}

func (s *Service) GetPartnerRide(ctx context.Context, userID, rideID uuid.UUID) (*store.Ride, error) {
	p, err := s.store.GetPartnerByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("partner lookup: %w", err)
	}
	r, err := s.store.GetRide(ctx, rideID)
	if errors.Is(err, store.ErrRideNotFound) {
		return nil, fmt.Errorf("not_found: ride")
	}
	if err != nil {
		return nil, err
	}
	if r.PartnerID == nil || *r.PartnerID != p.ID {
		return nil, fmt.Errorf("not_found: ride")
	}
	return r, nil
}
