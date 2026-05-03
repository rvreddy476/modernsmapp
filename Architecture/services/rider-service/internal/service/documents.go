package service

import (
	"context"

	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

// ListPartnerDocuments returns the partner's KYC documents.
func (s *Service) ListPartnerDocuments(ctx context.Context, userID uuid.UUID) ([]store.PartnerDocument, error) {
	p, err := s.GetMyPartner(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.store.ListPartnerDocuments(ctx, p.ID)
}

// ListVehicleDocuments returns docs for a specific vehicle.
func (s *Service) ListVehicleDocuments(ctx context.Context, userID, vehicleID uuid.UUID) ([]store.VehicleDocument, error) {
	v, err := s.store.GetVehicle(ctx, vehicleID)
	if err != nil {
		return nil, err
	}
	p, err := s.store.GetPartner(ctx, v.PartnerID)
	if err != nil {
		return nil, err
	}
	if p.UserID != userID {
		return nil, errForbidden("vehicle does not belong to user")
	}
	return s.store.ListVehicleDocuments(ctx, vehicleID)
}

// errForbidden is a tiny helper so the message gets the "forbidden:" prefix
// the http layer uses for 403 mapping.
func errForbidden(msg string) error {
	return &serviceErr{kind: "forbidden", msg: msg}
}

type serviceErr struct {
	kind string
	msg  string
}

func (e *serviceErr) Error() string { return e.kind + ": " + e.msg }
