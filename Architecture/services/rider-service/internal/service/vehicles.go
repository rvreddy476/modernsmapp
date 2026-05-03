package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

// allowedVehicleTypes covers the rider_vehicle_type enum values.
var allowedVehicleTypes = map[string]bool{
	"bike": true, "auto": true, "mini_cab": true,
	"sedan": true, "suv": true, "premium": true,
	"ev_bike": true, "ev_car": true,
}

// AddVehicleRequest is the input for AddVehicle.
type AddVehicleRequest struct {
	VehicleType        string
	RegistrationNumber string
	Brand              *string
	Model              *string
	Color              *string
	ManufactureYear    *int
	SeatCount          *int
	FuelType           *string
	IsEV               bool
}

// AddVehicle creates a vehicle row in `pending` status. Anti-fraud: the
// registration number is unique per active vehicle (mopedu spec §13).
func (s *Service) AddVehicle(ctx context.Context, userID, partnerID uuid.UUID, req AddVehicleRequest) (*store.Vehicle, error) {
	if !allowedVehicleTypes[req.VehicleType] {
		return nil, fmt.Errorf("invalid: vehicle_type must be one of bike, auto, mini_cab, sedan, suv, premium, ev_bike, ev_car")
	}
	reg := strings.ToUpper(strings.TrimSpace(req.RegistrationNumber))
	if reg == "" {
		return nil, fmt.Errorf("invalid: registration_number required")
	}
	p, err := s.store.GetPartner(ctx, partnerID)
	if err != nil {
		if errors.Is(err, store.ErrPartnerNotFound) {
			return nil, fmt.Errorf("not_found: partner")
		}
		return nil, err
	}
	if p.UserID != userID {
		return nil, fmt.Errorf("forbidden: partner does not belong to user")
	}
	v, err := s.store.CreateVehicle(ctx, store.CreateVehicleInput{
		PartnerID:          partnerID,
		VehicleType:        req.VehicleType,
		RegistrationNumber: reg,
		Brand:              req.Brand,
		Model:              req.Model,
		Color:              req.Color,
		ManufactureYear:    req.ManufactureYear,
		SeatCount:          req.SeatCount,
		FuelType:           req.FuelType,
		IsEV:               req.IsEV,
	})
	if err != nil {
		// Surface unique-violation as a friendly invalid: error.
		if strings.Contains(err.Error(), "ux_rider_vehicle_registration_active") {
			return nil, fmt.Errorf("invalid: registration_number already registered")
		}
		return nil, fmt.Errorf("create vehicle: %w", err)
	}
	if perr := s.producer.PublishPartnerVehicleAdded(ctx, partnerID, v.ID, v.VehicleType, v.RegistrationNumber); perr != nil {
		slog.Warn("rider: publish vehicle.added failed", "vehicle_id", v.ID, "error", perr)
	}
	return v, nil
}

// ListMyVehicles returns vehicles owned by the user's partner profile.
func (s *Service) ListMyVehicles(ctx context.Context, userID uuid.UUID) ([]store.Vehicle, error) {
	p, err := s.GetMyPartner(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.store.ListVehiclesByPartner(ctx, p.ID)
}

// SubmitVehicleDocumentRequest is the input for SubmitVehicleDocument.
type SubmitVehicleDocumentRequest struct {
	DocumentType   string
	DocumentNumber *string
	FileURL        string
	ExpiresAt      *time.Time
}

// SubmitVehicleDocument adds a doc row for the vehicle in `pending` status.
func (s *Service) SubmitVehicleDocument(ctx context.Context, userID, vehicleID uuid.UUID, req SubmitVehicleDocumentRequest) (*store.VehicleDocument, error) {
	if !allowedDocumentTypes[req.DocumentType] {
		return nil, fmt.Errorf("invalid: document_type must be a known rider_document_type")
	}
	if strings.TrimSpace(req.FileURL) == "" {
		return nil, fmt.Errorf("invalid: file_url required")
	}
	v, err := s.store.GetVehicle(ctx, vehicleID)
	if err != nil {
		if errors.Is(err, store.ErrVehicleNotFound) {
			return nil, fmt.Errorf("not_found: vehicle")
		}
		return nil, err
	}
	p, err := s.store.GetPartner(ctx, v.PartnerID)
	if err != nil {
		return nil, err
	}
	if p.UserID != userID {
		return nil, fmt.Errorf("forbidden: vehicle does not belong to user")
	}
	doc, err := s.store.CreateVehicleDocument(ctx, store.CreateVehicleDocumentInput{
		VehicleID:      vehicleID,
		DocumentType:   req.DocumentType,
		DocumentNumber: req.DocumentNumber,
		FileURL:        req.FileURL,
		ExpiresAt:      req.ExpiresAt,
	})
	if err != nil {
		return nil, fmt.Errorf("create vehicle document: %w", err)
	}
	return doc, nil
}
