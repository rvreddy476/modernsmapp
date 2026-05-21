package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

// CreateRideOperation is the idempotency-table operation label.
const CreateRideOperation = "ride_create"

// CreateRideRequest is the input shape for POST /v1/rider/rides.
type CreateRideRequest struct {
	PickupAddress  string
	PickupLat      float64
	PickupLng      float64
	DropAddress    string
	DropLat        float64
	DropLng        float64
	VehicleType    string
	CityID         *uuid.UUID
	PaymentMethod  string
	IdempotencyKey string
}

// CreateRide creates a `requested`-status ride row. Sprint 2 fills in the
// matching algorithm, state machine, OTP, and completion paths.
//
// Idempotent on idempotency_key — replays return the cached ride row.
func (s *Service) CreateRide(ctx context.Context, customerID uuid.UUID, req CreateRideRequest) (*store.Ride, error) {
	if customerID == uuid.Nil {
		return nil, fmt.Errorf("invalid: customer id required")
	}
	if !allowedVehicleTypes[req.VehicleType] {
		return nil, fmt.Errorf("invalid: vehicle_type must be one of bike, auto, mini_cab, sedan, suv, premium, ev_bike, ev_car")
	}
	if !validLatLng(req.PickupLat, req.PickupLng) || !validLatLng(req.DropLat, req.DropLng) {
		return nil, fmt.Errorf("invalid: pickup and drop coordinates must be valid")
	}
	if strings.TrimSpace(req.PickupAddress) == "" || strings.TrimSpace(req.DropAddress) == "" {
		return nil, fmt.Errorf("invalid: pickup_address and drop_address required")
	}
	if req.IdempotencyKey == "" {
		return nil, fmt.Errorf("invalid: idempotency_key required")
	}

	// Replay path.
	if existing, err := s.store.FindIdempotency(ctx, req.IdempotencyKey, customerID, CreateRideOperation); err == nil {
		if existing.ResourceID != nil {
			return s.store.GetRide(ctx, *existing.ResourceID)
		}
	} else if !errors.Is(err, store.ErrIdempotencyKeyNotFound) {
		return nil, err
	}

	// Pre-compute fare estimate so the row carries the headline numbers.
	var distKM, durMin, fareINR *float64
	if req.CityID != nil {
		est, err := s.EstimateFare(ctx, FareEstimateRequest{
			PickupLat:   req.PickupLat,
			PickupLng:   req.PickupLng,
			DropLat:     req.DropLat,
			DropLng:     req.DropLng,
			VehicleType: req.VehicleType,
			CityID:      *req.CityID,
		})
		if err == nil {
			d := est.EstimatedDistanceKM
			t := est.EstimatedDurationMin
			f := est.FareEstimateINR
			distKM, durMin, fareINR = &d, &t, &f
		} else {
			// Fare-rule miss is non-fatal in Sprint 1; ride row is created
			// without the estimate. Surface as a warning so admin can spot
			// missing rules in the dashboard.
			slog.Warn("rider: fare estimate skipped during ride create",
				"city_id", req.CityID, "vehicle_type", req.VehicleType, "error", err)
		}
	}

	method := req.PaymentMethod
	if method == "" {
		method = "cash"
	}
	ride, err := s.store.CreateRide(ctx, store.CreateRideInput{
		CustomerUserID:       customerID,
		CityID:               req.CityID,
		VehicleType:          req.VehicleType,
		PickupAddress:        req.PickupAddress,
		PickupLat:            req.PickupLat,
		PickupLng:            req.PickupLng,
		DropAddress:          req.DropAddress,
		DropLat:              req.DropLat,
		DropLng:              req.DropLng,
		EstimatedDistanceKM:  distKM,
		EstimatedDurationMin: durMin,
		EstimatedFare:        fareINR,
		PaymentMethod:        &method,
	})
	if err != nil {
		return nil, fmt.Errorf("create ride: %w", err)
	}
	cityID := ""
	if ride.CityID != nil {
		cityID = ride.CityID.String()
	}
	if perr := s.producer.PublishRideRequested(ctx, ride.ID, customerID, ride.VehicleType, cityID); perr != nil {
		slog.Warn("rider: publish ride.requested failed", "ride_id", ride.ID, "error", perr)
	}
	s.emit(ctx, "rider.ride."+ride.ID.String(), "rider.ride.requested", ride)
	s.publishRealtime(ctx, "rider.admin.live_rides", "rider.ride.requested", ride)
	if body, merr := json.Marshal(ride); merr == nil {
		_ = s.store.RecordIdempotency(ctx, req.IdempotencyKey, customerID, CreateRideOperation, &ride.ID, body)
	}
	return ride, nil
}

// GetRide returns a ride by id. Customers can read their own; partners can
// read rides assigned to them (S2). For Sprint 1 we enforce only customer
// ownership.
func (s *Service) GetRide(ctx context.Context, customerID, rideID uuid.UUID) (*store.Ride, error) {
	r, err := s.store.GetRide(ctx, rideID)
	if err != nil {
		if errors.Is(err, store.ErrRideNotFound) {
			return nil, fmt.Errorf("not_found: ride")
		}
		return nil, err
	}
	if r.CustomerUserID != customerID {
		return nil, fmt.Errorf("forbidden: ride does not belong to user")
	}
	return r, nil
}

// ListMyRides returns recent rides for the customer.
func (s *Service) ListMyRides(ctx context.Context, customerID uuid.UUID, limit int) ([]store.Ride, error) {
	if customerID == uuid.Nil {
		return nil, fmt.Errorf("invalid: customer id required")
	}
	return s.store.ListRidesByCustomer(ctx, customerID, limit)
}
