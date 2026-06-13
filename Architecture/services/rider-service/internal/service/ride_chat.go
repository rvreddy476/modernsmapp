package service

import (
	"context"
	"fmt"

	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

func (s *Service) resolveRideRole(ctx context.Context, rideID, userID uuid.UUID) (string, error) {
	m, err := s.store.RidePartyMembership(ctx, rideID, userID)
	if err != nil {
		return "", err
	}
	switch {
	case m.IsCustomer:
		return "customer", nil
	case m.IsPartner:
		return "partner", nil
	}
	return "", fmt.Errorf("forbidden: not a party to this ride")
}

// AppendRideMessage resolves the author's role + appends the message,
// then publishes a realtime frame so the other party's open SSE
// updates without polling.
func (s *Service) AppendRideMessage(ctx context.Context, rideID, authorID uuid.UUID, body string, isAdmin bool) (*store.RideMessage, error) {
	role := "admin"
	if !isAdmin {
		r, err := s.resolveRideRole(ctx, rideID, authorID)
		if err != nil {
			return nil, err
		}
		role = r
	}
	m, err := s.store.AppendRideMessage(ctx, rideID, authorID, role, body)
	if err != nil {
		return nil, err
	}
	s.publishRealtime(ctx, "rider.ride."+rideID.String(), "rider.ride.message", m)
	return m, nil
}

// ListRideMessages returns the thread. Party-only or admin override.
func (s *Service) ListRideMessages(ctx context.Context, rideID, viewerID uuid.UUID, isAdmin bool) ([]store.RideMessage, error) {
	if !isAdmin {
		if _, err := s.resolveRideRole(ctx, rideID, viewerID); err != nil {
			return nil, err
		}
	}
	return s.store.ListRideMessages(ctx, rideID)
}

// MarkRideMessageRead is idempotent at the store layer.
func (s *Service) MarkRideMessageRead(ctx context.Context, messageID, viewerID uuid.UUID, role string) error {
	return s.store.MarkRideMessageRead(ctx, messageID, viewerID, role)
}
