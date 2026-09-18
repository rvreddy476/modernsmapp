package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RideCancellationFacts is what the refund rules read about a cancelled
// ride: who cancelled it and when, against when the captain was assigned.
type RideCancellationFacts struct {
	RideID          uuid.UUID
	CustomerUserID  uuid.UUID
	CityID          *uuid.UUID
	VehicleType     string
	Status          string
	CancelledByKind string
	AssignedAt      *time.Time
	CancelledAt     *time.Time
	// CancellationFeePaise is the fee charged at cancellation.
	CancellationFeePaise int64
}

// GetRideCancellationFacts reads the cancellation facts of a ride.
func (s *Store) GetRideCancellationFacts(ctx context.Context, rideID uuid.UUID) (*RideCancellationFacts, error) {
	var f RideCancellationFacts
	err := s.db.QueryRow(ctx, `
        SELECT id, customer_user_id, city_id, vehicle_type::text, status::text, COALESCE(cancelled_by_kind, ''),
               assigned_at, cancelled_at, COALESCE(cancellation_fee_paise, 0)
        FROM rider_rides WHERE id = $1`, rideID,
	).Scan(&f.RideID, &f.CustomerUserID, &f.CityID, &f.VehicleType, &f.Status, &f.CancelledByKind, &f.AssignedAt, &f.CancelledAt, &f.CancellationFeePaise)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRideNotFound
		}
		return nil, fmt.Errorf("ride cancellation facts: %w", err)
	}
	return &f, nil
}

// GetOutstandingByRide returns the outstanding fee row a ride created.
func (s *Store) GetOutstandingByRide(ctx context.Context, rideID uuid.UUID) (*CustomerOutstanding, error) {
	o, err := scanOutstanding(s.db.QueryRow(ctx, `SELECT `+outstandingColumns+` FROM rider_customer_outstanding WHERE ride_id = $1`, rideID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOutstandingNotFound
		}
		return nil, err
	}
	return o, nil
}
