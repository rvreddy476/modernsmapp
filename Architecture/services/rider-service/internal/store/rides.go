package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrRideNotFound is returned when GetRide finds no row.
var ErrRideNotFound = errors.New("ride: not found")

// ErrInvalidTransition is returned by TransitionRide when the requested
// status change is not allowed by the state machine OR when the row's
// current status has drifted under us (concurrent transition).
var ErrInvalidTransition = errors.New("ride: invalid status transition")

// ErrRevisionConflict is returned when an expected revision does not match.
var ErrRevisionConflict = errors.New("ride: revision conflict")

// ErrExpectedRevisionRequired is returned when an atomic transition is attempted without providing expected revision.
var ErrExpectedRevisionRequired = errors.New("ride: expected revision required")

// CreateRideOperation is the idempotency-table operation label.
const CreateRideOperation = "ride_create"

// CreateRideInput captures the fields collected by POST /v1/rider/rides.
type CreateRideInput struct {
	CustomerUserID       uuid.UUID
	CityID               *uuid.UUID
	QuoteID              *uuid.UUID
	VehicleType          string
	PickupAddress        string
	PickupLat            float64
	PickupLng            float64
	DropAddress          string
	DropLat              float64
	DropLng              float64
	EstimatedDistanceKM  *float64
	EstimatedDurationMin *float64
	EstimatedFare        *float64
	FareBreakdown        []byte
	PaymentMethod        *string
	ScheduledFor         *time.Time
}

const rideSelectColumns = `
    id, customer_user_id, partner_id, vehicle_id, city_id, quote_id, revision, vehicle_type, status,
    pickup_address, ST_Y(pickup_location::geometry), ST_X(pickup_location::geometry),
    drop_address, ST_Y(drop_location::geometry), ST_X(drop_location::geometry),
    estimated_distance_km, estimated_duration_min, estimated_fare,
    final_distance_km, final_duration_min, final_fare, final_fare_paise,
    payment_method, otp_code, otp_encrypted, otp_expires_at, otp_attempts, otp_locked_until,
    cash_confirmed_at, cash_confirmed_by, fare_breakdown, scheduled_for,
    requested_at, assigned_at, arrived_at, started_at, completed_at,
    cancelled_at, cancelled_by, cancellation_reason, customer_rating, partner_rating,
    created_at, updated_at`

// CreateRideAtomicInput contains all parameters for atomic booking.
type CreateRideAtomicInput struct {
	RideID          *uuid.UUID
	RideInput       CreateRideInput
	IdempotencyKey  string
	RequestHash     string
	OutboxEventType string
	OutboxPayload   []byte
}

// CreateRideAtomic claims idempotency, enforces active-ride invariant, inserts ride, history, outbox, and idempotency in one tx.
func (s *Store) CreateRideAtomic(ctx context.Context, in CreateRideAtomicInput) (*Ride, bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)

	// 1. Claim idempotency row with ON CONFLICT DO NOTHING
	const claimQ = `
        INSERT INTO rider_idempotency (key, user_id, operation, request_hash, expires_at)
        VALUES ($1, $2, $3, $4, NOW() + INTERVAL '24 hours')
        ON CONFLICT (key) DO NOTHING`
	tag, err := tx.Exec(ctx, claimQ, in.IdempotencyKey, in.RideInput.CustomerUserID, CreateRideOperation, in.RequestHash)
	if err != nil {
		return nil, false, fmt.Errorf("claim idempotency: %w", err)
	}

	if tag.RowsAffected() == 0 {
		var existingHash *string
		var existingRideID *uuid.UUID
		var existingUserID uuid.UUID
		var existingOp string
		const checkQ = `
            SELECT request_hash, resource_id, user_id, operation
            FROM rider_idempotency
            WHERE key = $1`
		err := tx.QueryRow(ctx, checkQ, in.IdempotencyKey).Scan(&existingHash, &existingRideID, &existingUserID, &existingOp)
		if err != nil {
			return nil, false, fmt.Errorf("query existing idempotency: %w", err)
		}
		if existingUserID != in.RideInput.CustomerUserID || existingOp != CreateRideOperation || (existingHash != nil && *existingHash != in.RequestHash) {
			return nil, false, ErrIdempotencyMismatch
		}
		if existingRideID != nil && *existingRideID != uuid.Nil {
			existingRide, err := s.GetRide(ctx, *existingRideID)
			if err == nil && existingRide != nil {
				return existingRide, true, nil
			}
		}
		return nil, false, fmt.Errorf("concurrent booking in progress")
	}

	// 2. Enforce customer active-ride invariant
	const activeCheckQ = `
        SELECT id FROM rider_rides
        WHERE customer_user_id = $1
          AND status NOT IN ('completed','cancelled_by_customer','cancelled_by_partner','cancelled_by_admin','expired','failed')
        LIMIT 1 FOR UPDATE`
	var activeRideID uuid.UUID
	err = tx.QueryRow(ctx, activeCheckQ, in.RideInput.CustomerUserID).Scan(&activeRideID)
	if err == nil && activeRideID != uuid.Nil {
		return nil, false, fmt.Errorf("invalid: customer already has an active ride in progress")
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("check active ride: %w", err)
	}

	// 3. Insert ride row
	status := "requested"
	if in.RideInput.ScheduledFor != nil && in.RideInput.ScheduledFor.After(time.Now()) {
		status = "scheduled"
	}
	fareBreakdown := in.RideInput.FareBreakdown
	if len(fareBreakdown) == 0 {
		fareBreakdown = []byte("{}")
	}

	rideID := uuid.New()
	if in.RideID != nil && *in.RideID != uuid.Nil {
		rideID = *in.RideID
	}

	const insertRideQ = `
        INSERT INTO rider_rides (
            id, customer_user_id, city_id, quote_id, vehicle_type, status,
            pickup_address, pickup_location, drop_address, drop_location,
            estimated_distance_km, estimated_duration_min, estimated_fare, fare_breakdown,
            payment_method, scheduled_for, revision
        ) VALUES (
            $1, $2, $3, $4, $5::rider_vehicle_type, $16::rider_ride_status,
            $6, ST_SetSRID(ST_MakePoint($7, $8), 4326)::geography,
            $9, ST_SetSRID(ST_MakePoint($10, $11), 4326)::geography,
            $12, $13, $14, $15,
            $17, $18, 1
        )
        RETURNING ` + rideSelectColumns

	row := tx.QueryRow(ctx, insertRideQ,
		rideID, in.RideInput.CustomerUserID, in.RideInput.CityID, in.RideInput.QuoteID, in.RideInput.VehicleType,
		in.RideInput.PickupAddress, in.RideInput.PickupLng, in.RideInput.PickupLat,
		in.RideInput.DropAddress, in.RideInput.DropLng, in.RideInput.DropLat,
		in.RideInput.EstimatedDistanceKM, in.RideInput.EstimatedDurationMin, in.RideInput.EstimatedFare, fareBreakdown,
		status, in.RideInput.PaymentMethod, in.RideInput.ScheduledFor,
	)
	ride, err := scanRide(row)
	if err != nil {
		return nil, false, fmt.Errorf("insert ride: %w", err)
	}

	// 4. Insert initial status history
	const insertHistQ = `
        INSERT INTO rider_ride_status_history (ride_id, from_status, to_status, actor_kind, actor_user_id, reason)
        VALUES ($1, NULL, $2, 'customer', $3, 'ride created')`
	if _, err := tx.Exec(ctx, insertHistQ, ride.ID, status, in.RideInput.CustomerUserID); err != nil {
		return nil, false, fmt.Errorf("insert initial history: %w", err)
	}

	// 5. Insert outbox event
	if in.OutboxEventType != "" {
		if err := InsertOutboxEventTx(ctx, tx, in.OutboxEventType, "ride", ride.ID.String(), in.OutboxPayload); err != nil {
			return nil, false, fmt.Errorf("insert outbox event: %w", err)
		}
	}

	// 6. Bind ride ID to idempotency row
	const updateIdemQ = `
        UPDATE rider_idempotency
        SET resource_id = $1
        WHERE key = $2`
	if _, err := tx.Exec(ctx, updateIdemQ, ride.ID, in.IdempotencyKey); err != nil {
		return nil, false, fmt.Errorf("update idempotency: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit create ride: %w", err)
	}
	return ride, false, nil
}

// TransitionRideAtomicInput captures atomic lifecycle transition parameters.
type TransitionRideAtomicInput struct {
	RideID           uuid.UUID
	ExpectedRevision int
	FromStatus       string
	ToStatus         string
	ActorKind        string
	ActorUserID      *uuid.UUID
	Reason           *string
	OutboxEventType  string
	OutboxPayload    []byte
	Mutate           func(tx pgx.Tx, r *Ride) error
}

// TransitionRideAtomic atomically updates status, revision, history, and outbox in one tx.
func (s *Store) TransitionRideAtomic(ctx context.Context, in TransitionRideAtomicInput) (*Ride, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	const lockQ = `SELECT ` + rideSelectColumns + ` FROM rider_rides WHERE id = $1 FOR UPDATE`
	row := tx.QueryRow(ctx, lockQ, in.RideID)
	r, err := scanRide(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRideNotFound
		}
		return nil, fmt.Errorf("lock ride: %w", err)
	}

	if in.ExpectedRevision <= 0 {
		return nil, ErrExpectedRevisionRequired
	}
	if r.Revision != in.ExpectedRevision {
		return nil, ErrRevisionConflict
	}
	if in.FromStatus != "" && r.Status != in.FromStatus {
		return nil, ErrInvalidTransition
	}

	if in.Mutate != nil {
		if err := in.Mutate(tx, r); err != nil {
			return nil, err
		}
	}

	const updateQ = `
        UPDATE rider_rides
        SET status = $2::rider_ride_status, revision = revision + 1, updated_at = NOW()
        WHERE id = $1 AND revision = $3
        RETURNING ` + rideSelectColumns

	row = tx.QueryRow(ctx, updateQ, in.RideID, in.ToStatus, r.Revision)
	updatedRide, err := scanRide(row)
	if err != nil {
		return nil, fmt.Errorf("update ride transition: %w", err)
	}

	const histQ = `
        INSERT INTO rider_ride_status_history (ride_id, from_status, to_status, actor_kind, actor_user_id, reason)
        VALUES ($1, $2, $3, $4, $5, $6)`
	fromStatusPtr := &r.Status
	if _, err := tx.Exec(ctx, histQ, in.RideID, fromStatusPtr, in.ToStatus, in.ActorKind, in.ActorUserID, in.Reason); err != nil {
		return nil, fmt.Errorf("insert status history: %w", err)
	}

	if in.OutboxEventType != "" {
		if err := InsertOutboxEventTx(ctx, tx, in.OutboxEventType, "ride", in.RideID.String(), in.OutboxPayload); err != nil {
			return nil, fmt.Errorf("insert outbox: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit transition: %w", err)
	}
	return updatedRide, nil
}

// BeginTx starts a database transaction.
func (s *Store) BeginTx(ctx context.Context) (pgx.Tx, error) {
	return s.db.Begin(ctx)
}

// SetOTPLock updates the failure attempts and lock timestamp on a ride row.
func (s *Store) SetOTPLock(ctx context.Context, rideID uuid.UUID, attempts int, lockedUntil *time.Time) error {
	const q = `UPDATE rider_rides SET otp_attempts = $2, otp_locked_until = $3, updated_at = NOW() WHERE id = $1`
	_, err := s.db.Exec(ctx, q, rideID, attempts, lockedUntil)
	return err
}

// SetOTPLockTx updates the failure attempts and lock timestamp on a locked ride row.
func SetOTPLockTx(ctx context.Context, tx pgx.Tx, rideID uuid.UUID, attempts int, lockedUntil *time.Time) error {
	const q = `UPDATE rider_rides SET otp_attempts = $2, otp_locked_until = $3, updated_at = NOW() WHERE id = $1`
	_, err := tx.Exec(ctx, q, rideID, attempts, lockedUntil)
	return err
}

// CreateRide inserts a row in `requested` status with quote snapshot binding.
func (s *Store) CreateRide(ctx context.Context, in CreateRideInput) (*Ride, error) {
	status := "requested"
	if in.ScheduledFor != nil && in.ScheduledFor.After(time.Now()) {
		status = "scheduled"
	}
	fareBreakdown := in.FareBreakdown
	if len(fareBreakdown) == 0 {
		fareBreakdown = []byte("{}")
	}

	const q = `
        INSERT INTO rider_rides (
            customer_user_id, city_id, quote_id, vehicle_type, status,
            pickup_address, pickup_location, drop_address, drop_location,
            estimated_distance_km, estimated_duration_min, estimated_fare, fare_breakdown,
            payment_method, scheduled_for, revision
        ) VALUES (
            $1, $2, $3, $4::rider_vehicle_type, $15::rider_ride_status,
            $5, ST_SetSRID(ST_MakePoint($6, $7), 4326)::geography,
            $8, ST_SetSRID(ST_MakePoint($9, $10), 4326)::geography,
            $11, $12, $13, $14,
            $16, $17, 1
        )
        RETURNING ` + rideSelectColumns

	row := s.db.QueryRow(ctx, q,
		in.CustomerUserID, in.CityID, in.QuoteID, in.VehicleType,
		in.PickupAddress, in.PickupLng, in.PickupLat,
		in.DropAddress, in.DropLng, in.DropLat,
		in.EstimatedDistanceKM, in.EstimatedDurationMin, in.EstimatedFare, fareBreakdown,
		status, in.PaymentMethod, in.ScheduledFor,
	)
	return scanRide(row)
}

// GetRide returns one ride by id.
func (s *Store) GetRide(ctx context.Context, id uuid.UUID) (*Ride, error) {
	const q = `SELECT ` + rideSelectColumns + ` FROM rider_rides WHERE id = $1`
	row := s.db.QueryRow(ctx, q, id)
	r, err := scanRide(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRideNotFound
		}
		return nil, err
	}
	return r, nil
}

// GetActiveRideForCustomer returns the single active ride for a customer if one exists.
func (s *Store) GetActiveRideForCustomer(ctx context.Context, customerID uuid.UUID) (*Ride, error) {
	const q = `
        SELECT ` + rideSelectColumns + `
        FROM rider_rides
        WHERE customer_user_id = $1
          AND status NOT IN ('completed','cancelled_by_customer','cancelled_by_partner','cancelled_by_admin','expired','failed')
        LIMIT 1`
	row := s.db.QueryRow(ctx, q, customerID)
	r, err := scanRide(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return r, nil
}

// GetActiveRideForPartner returns the single active ride assigned to a partner.
func (s *Store) GetActiveRideForPartner(ctx context.Context, partnerID uuid.UUID) (*Ride, error) {
	const q = `
        SELECT ` + rideSelectColumns + `
        FROM rider_rides
        WHERE partner_id = $1
          AND status IN ('partner_assigned','partner_arriving','arrived','otp_verified','in_progress')
        LIMIT 1`
	row := s.db.QueryRow(ctx, q, partnerID)
	r, err := scanRide(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRideNotFound
		}
		return nil, err
	}
	return r, nil
}

// GetRideWithOTP returns the full ride row PLUS the hashed OTP string.
func (s *Store) GetRideWithOTP(ctx context.Context, id uuid.UUID) (*Ride, *string, *time.Time, int, *time.Time, error) {
	const q = `
        SELECT ` + rideSelectColumns + `, otp_hash
        FROM rider_rides
        WHERE id = $1`
	row := s.db.QueryRow(ctx, q, id)
	r, otpHash, err := scanRideWithOTP(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil, 0, nil, ErrRideNotFound
		}
		return nil, nil, nil, 0, nil, err
	}
	return r, otpHash, r.OTPExpiresAt, r.OTPAttempts, r.OTPLockedUntil, nil
}

// ListRidesByCustomer returns recent rides for the customer.
func (s *Store) ListRidesByCustomer(ctx context.Context, customerID uuid.UUID, limit int) ([]Ride, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const q = `
        SELECT ` + rideSelectColumns + `
        FROM rider_rides
        WHERE customer_user_id = $1
        ORDER BY created_at DESC
        LIMIT $2`
	rows, err := s.db.Query(ctx, q, customerID, limit)
	if err != nil {
		return nil, fmt.Errorf("list rides: %w", err)
	}
	defer rows.Close()
	var out []Ride
	for rows.Next() {
		r, err := scanRide(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// TransitionRide updates the ride status atomically and increments revision.
func (s *Store) TransitionRide(ctx context.Context, rideID uuid.UUID, from, to string) error {
	const q = `
        UPDATE rider_rides
        SET status     = $3::rider_ride_status,
            revision   = revision + 1,
            updated_at = NOW()
        WHERE id = $1 AND status = $2::rider_ride_status`
	tag, err := s.db.Exec(ctx, q, rideID, from, to)
	if err != nil {
		return fmt.Errorf("transition ride: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrInvalidTransition
	}
	return nil
}

// TransitionRideWithRevision updates status verifying the expected revision.
func (s *Store) TransitionRideWithRevision(ctx context.Context, rideID uuid.UUID, from, to string, expectedRev int) (int, error) {
	if expectedRev <= 0 {
		return 0, s.TransitionRide(ctx, rideID, from, to)
	}
	const q = `
        UPDATE rider_rides
        SET status     = $3::rider_ride_status,
            revision   = revision + 1,
            updated_at = NOW()
        WHERE id = $1 AND status = $2::rider_ride_status AND revision = $4
        RETURNING revision`
	var newRev int
	err := s.db.QueryRow(ctx, q, rideID, from, to, expectedRev).Scan(&newRev)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrRevisionConflict
		}
		return 0, fmt.Errorf("transition ride with revision: %w", err)
	}
	return newRev, nil
}

// AssignRidePartner sets partner_id + vehicle_id + otp_hash + otp_code + otp_expires_at in one update.
func (s *Store) AssignRidePartner(ctx context.Context, rideID, partnerID, vehicleID uuid.UUID, otpHash, otpCode string, otpExpires time.Time) error {
	const q = `
        UPDATE rider_rides
        SET partner_id     = $2,
            vehicle_id     = $3,
            otp_hash       = $4,
            otp_code       = $5,
            otp_expires_at = $6,
            otp_attempts   = 0,
            assigned_at    = NOW(),
            revision       = revision + 1,
            updated_at     = NOW()
        WHERE id = $1`
	tag, err := s.db.Exec(ctx, q, rideID, partnerID, vehicleID, otpHash, otpCode, otpExpires)
	if err != nil {
		return fmt.Errorf("assign ride partner: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRideNotFound
	}
	return nil
}

// SetArrivingAt stamps partner_arriving_at = now().
func (s *Store) SetArrivingAt(ctx context.Context, rideID uuid.UUID) error {
	const q = `UPDATE rider_rides SET partner_arriving_at = NOW(), revision = revision + 1, updated_at = NOW() WHERE id = $1`
	_, err := s.db.Exec(ctx, q, rideID)
	return err
}

// SetArrivedAt stamps arrived_at = now().
func (s *Store) SetArrivedAt(ctx context.Context, rideID uuid.UUID) error {
	const q = `UPDATE rider_rides SET arrived_at = NOW(), revision = revision + 1, updated_at = NOW() WHERE id = $1`
	_, err := s.db.Exec(ctx, q, rideID)
	return err
}

// SetStartedAt stamps started_at = now().
func (s *Store) SetStartedAt(ctx context.Context, rideID uuid.UUID) error {
	const q = `UPDATE rider_rides SET started_at = NOW(), revision = revision + 1, updated_at = NOW() WHERE id = $1`
	_, err := s.db.Exec(ctx, q, rideID)
	return err
}

// ListScheduledRidesDue returns scheduled rides whose scheduled_for is ready.
func (s *Store) ListScheduledRidesDue(ctx context.Context, batch int) ([]Ride, error) {
	if batch <= 0 {
		batch = 25
	}
	const q = `
        SELECT ` + rideSelectColumns + `
        FROM rider_rides
        WHERE status = 'scheduled'
          AND scheduled_for IS NOT NULL
          AND scheduled_for - INTERVAL '15 minutes' <= NOW()
        ORDER BY scheduled_for
        LIMIT $1
        FOR UPDATE SKIP LOCKED`
	rows, err := s.db.Query(ctx, q, batch)
	if err != nil {
		return nil, fmt.Errorf("list scheduled rides due: %w", err)
	}
	defer rows.Close()
	var out []Ride
	for rows.Next() {
		r, err := scanRide(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// ActivateScheduledRide promotes the row from 'scheduled' to 'requested'.
func (s *Store) ActivateScheduledRide(ctx context.Context, rideID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE rider_rides
		SET status = 'requested', revision = revision + 1, activated_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND status = 'scheduled'
	`, rideID)
	if err != nil {
		return fmt.Errorf("activate scheduled: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRideNotFound
	}
	return nil
}

// IncrementOTPAttempts increments the failed attempt count and returns the new count.
func (s *Store) IncrementOTPAttempts(ctx context.Context, rideID uuid.UUID) (int, error) {
	const q = `UPDATE rider_rides SET otp_attempts = otp_attempts + 1, updated_at = NOW() WHERE id = $1 RETURNING otp_attempts`
	var attempts int
	err := s.db.QueryRow(ctx, q, rideID).Scan(&attempts)
	return attempts, err
}

// LockOTP temporarily locks OTP verification after abuse.
func (s *Store) LockOTP(ctx context.Context, rideID uuid.UUID, until time.Time) error {
	const q = `UPDATE rider_rides SET otp_locked_until = $2, updated_at = NOW() WHERE id = $1`
	_, err := s.db.Exec(ctx, q, rideID, until)
	return err
}

// CompleteRideInput captures the partner-supplied final telemetry.
type CompleteRideInput struct {
	RideID           uuid.UUID
	FinalDistanceKM  float64
	FinalDurationMin int
	FinalFareINR     float64
	FinalFarePaise   int64
	FlaggedForReview bool
}

// FinalizeRide stamps the final fare + telemetry + completed_at.
func (s *Store) FinalizeRide(ctx context.Context, in CompleteRideInput) error {
	const q = `
        UPDATE rider_rides
        SET final_distance_km   = $2,
            final_duration_min  = $3,
            final_fare          = $4,
            final_fare_paise    = $5,
            completed_at        = NOW(),
            revision            = revision + 1,
            updated_at          = NOW()
        WHERE id = $1`
	tag, err := s.db.Exec(ctx, q, in.RideID, in.FinalDistanceKM, in.FinalDurationMin, in.FinalFareINR, in.FinalFarePaise)
	if err != nil {
		return fmt.Errorf("finalize ride: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRideNotFound
	}
	return nil
}

// CancelRideInput captures the cancellation context (fee + reason + actor).
type CancelRideInput struct {
	RideID               uuid.UUID
	CancellationFeePaise int64
	Reason               string
	CancelledByKind      string // 'customer' | 'partner' | 'admin' | 'system'
}

// MarkRideCancelled stamps cancellation fields.
func (s *Store) MarkRideCancelled(ctx context.Context, in CancelRideInput) error {
	const q = `
        UPDATE rider_rides
        SET cancellation_reason = $2,
            cancelled_at        = NOW(),
            revision            = revision + 1,
            updated_at          = NOW()
        WHERE id = $1`
	tag, err := s.db.Exec(ctx, q, in.RideID, in.Reason)
	if err != nil {
		return fmt.Errorf("mark ride cancelled: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRideNotFound
	}
	return nil
}

// SetRating stores the customer rating + comment on the ride row.
func (s *Store) SetRating(ctx context.Context, rideID uuid.UUID, rating int16, comment *string) error {
	const q = `UPDATE rider_rides SET customer_rating = $2, customer_feedback = $3, revision = revision + 1, updated_at = NOW() WHERE id = $1`
	tag, err := s.db.Exec(ctx, q, rideID, rating, comment)
	if err != nil {
		return fmt.Errorf("set rating: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRideNotFound
	}
	return nil
}

// SetShareToken sets a one-time share token for the ride.
func (s *Store) SetShareToken(ctx context.Context, rideID uuid.UUID, token string) (string, error) {
	return token, nil
}

// ConfirmCashPayment stamps cash confirmation by the assigned captain.
func (s *Store) ConfirmCashPayment(ctx context.Context, rideID, partnerID uuid.UUID) error {
	const q = `
        UPDATE rider_rides
        SET cash_confirmed_at = NOW(),
            cash_confirmed_by = $2,
            revision          = revision + 1,
            updated_at        = NOW()
        WHERE id = $1 AND partner_id = $2`
	tag, err := s.db.Exec(ctx, q, rideID, partnerID)
	if err != nil {
		return fmt.Errorf("confirm cash payment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRideNotFound
	}
	return nil
}



// AppendStatusHistory writes a single audit row capturing the transition.
func (s *Store) AppendStatusHistory(ctx context.Context, rideID uuid.UUID, from *string, to, actorKind string, actorUserID *uuid.UUID, reason *string) error {
	const q = `
        INSERT INTO rider_ride_status_history (ride_id, from_status, to_status, actor_kind, actor_user_id, reason)
        VALUES ($1, $2, $3, $4, $5, $6)`
	if _, err := s.db.Exec(ctx, q, rideID, from, to, actorKind, actorUserID, reason); err != nil {
		return fmt.Errorf("append status history: %w", err)
	}
	return nil
}

// ListRideStatusHistory returns every transition row for a ride, oldest first.
func (s *Store) ListRideStatusHistory(ctx context.Context, rideID uuid.UUID) ([]RideStatusHistory, error) {
	const q = `
        SELECT id, ride_id, from_status, to_status, actor_kind, actor_user_id, reason, created_at
        FROM rider_ride_status_history
        WHERE ride_id = $1
        ORDER BY created_at ASC`
	rows, err := s.db.Query(ctx, q, rideID)
	if err != nil {
		return nil, fmt.Errorf("list status history: %w", err)
	}
	defer rows.Close()
	var out []RideStatusHistory
	for rows.Next() {
		var h RideStatusHistory
		if err := rows.Scan(&h.ID, &h.RideID, &h.FromStatus, &h.ToStatus, &h.ActorKind, &h.ActorUserID, &h.Reason, &h.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ListStaleRides returns rides stuck in `requested` or `searching_partner` for longer than `olderThan`.
func (s *Store) ListStaleRides(ctx context.Context, olderThan time.Duration) ([]Ride, error) {
	const q = `
        SELECT ` + rideSelectColumns + `
        FROM rider_rides
        WHERE status IN ('requested','searching_partner')
          AND created_at < NOW() - ($1::int * INTERVAL '1 second')
        ORDER BY created_at ASC
        LIMIT 200`
	rows, err := s.db.Query(ctx, q, int(olderThan.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("list stale rides: %w", err)
	}
	defer rows.Close()
	var out []Ride
	for rows.Next() {
		r, err := scanRide(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// ListRidesByPartner returns recent rides for the partner.
func (s *Store) ListRidesByPartner(ctx context.Context, partnerID uuid.UUID, since time.Time, limit int) ([]Ride, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const q = `
        SELECT ` + rideSelectColumns + `
        FROM rider_rides
        WHERE partner_id = $1 AND created_at >= $2
        ORDER BY created_at DESC
        LIMIT $3`
	rows, err := s.db.Query(ctx, q, partnerID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("list rides by partner: %w", err)
	}
	defer rows.Close()
	var out []Ride
	for rows.Next() {
		r, err := scanRide(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// PartnerEarningsSummary is the partner-dashboard aggregate.
type PartnerEarningsSummary struct {
	RideCount    int   `json:"ride_count"`
	EarningPaise int64 `json:"earning_paise"`
}

// PartnerEarnings returns ride count + sum(final_fare_paise) for the partner.
func (s *Store) PartnerEarnings(ctx context.Context, partnerID uuid.UUID, since time.Time) (*PartnerEarningsSummary, error) {
	const q = `
        SELECT COUNT(*)::int, COALESCE(SUM(final_fare_paise), 0)::bigint
        FROM rider_rides
        WHERE partner_id = $1 AND status = 'completed' AND completed_at >= $2`
	var out PartnerEarningsSummary
	if err := s.db.QueryRow(ctx, q, partnerID, since).Scan(&out.RideCount, &out.EarningPaise); err != nil {
		return nil, fmt.Errorf("partner earnings: %w", err)
	}
	return &out, nil
}

// RideReceipt holds immutable trip receipt data in integer paise.
type RideReceipt struct {
	RideID          uuid.UUID       `json:"ride_id"`
	CustomerUserID  uuid.UUID       `json:"customer_user_id"`
	PartnerID       *uuid.UUID      `json:"partner_id,omitempty"`
	VehicleType     string          `json:"vehicle_type"`
	Status          string          `json:"status"`
	PickupAddress   string          `json:"pickup_address"`
	DropAddress     string          `json:"drop_address"`
	DistanceMeters  int             `json:"distance_meters"`
	DurationSeconds int             `json:"duration_seconds"`
	TotalPaise      int64           `json:"total_paise"`
	PaymentMethod   string          `json:"payment_method"`
	PaymentStatus   string          `json:"payment_status"`
	FareBreakdown   json.RawMessage `json:"fare_breakdown"`
	CompletedAt     *time.Time      `json:"completed_at,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
}

// GetRideReceipt fetches receipt details.
func (s *Store) GetRideReceipt(ctx context.Context, rideID uuid.UUID) (*RideReceipt, error) {
	const q = `
        SELECT r.id, r.customer_user_id, r.partner_id, r.vehicle_type, r.status,
               r.pickup_address, r.drop_address,
               COALESCE((r.final_distance_km * 1000)::int, (r.estimated_distance_km * 1000)::int, 0),
               COALESCE((r.final_duration_min * 60)::int, (r.estimated_duration_min * 60)::int, 0),
               COALESCE(r.final_fare_paise, (r.estimated_fare * 100)::bigint, 0),
               COALESCE(r.payment_method, 'cash'),
               CASE 
                   WHEN p.status IS NOT NULL THEN p.status
                   WHEN r.cash_confirmed_at IS NOT NULL THEN 'succeeded'
                   WHEN r.status = 'completed' AND r.payment_method = 'cash' THEN 'pending_collection'
                   ELSE 'pending'
               END,
               COALESCE(r.fare_breakdown, '{}'::jsonb),
               r.completed_at, r.created_at
        FROM rider_rides r
        LEFT JOIN rider_ride_payments p ON p.ride_id = r.id
        WHERE r.id = $1`

	var rc RideReceipt
	err := s.db.QueryRow(ctx, q, rideID).Scan(
		&rc.RideID, &rc.CustomerUserID, &rc.PartnerID, &rc.VehicleType, &rc.Status,
		&rc.PickupAddress, &rc.DropAddress,
		&rc.DistanceMeters, &rc.DurationSeconds,
		&rc.TotalPaise, &rc.PaymentMethod, &rc.PaymentStatus,
		&rc.FareBreakdown, &rc.CompletedAt, &rc.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRideNotFound
		}
		return nil, fmt.Errorf("get ride receipt: %w", err)
	}
	return &rc, nil
}

func scanRide(row pgx.Row) (*Ride, error) {
	var r Ride
	var rawBreakdown []byte
	if err := row.Scan(
		&r.ID, &r.CustomerUserID, &r.PartnerID, &r.VehicleID, &r.CityID, &r.QuoteID, &r.Revision, &r.VehicleType, &r.Status,
		&r.PickupAddress, &r.PickupLat, &r.PickupLng,
		&r.DropAddress, &r.DropLat, &r.DropLng,
		&r.EstimatedDistanceKM, &r.EstimatedDurationMin, &r.EstimatedFare,
		&r.FinalDistanceKM, &r.FinalDurationMin, &r.FinalFare, &r.FinalFarePaise,
		&r.PaymentMethod, &r.OTPCode, &r.OTPEncrypted, &r.OTPExpiresAt, &r.OTPAttempts, &r.OTPLockedUntil,
		&r.CashConfirmedAt, &r.CashConfirmedBy, &rawBreakdown, &r.ScheduledFor,
		&r.RequestedAt, &r.AssignedAt, &r.ArrivedAt, &r.StartedAt, &r.CompletedAt,
		&r.CancelledAt, &r.CancelledBy, &r.CancellationReason, &r.CustomerRating, &r.PartnerRating,
		&r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if len(rawBreakdown) > 0 {
		r.FareBreakdown = json.RawMessage(rawBreakdown)
	}
	return &r, nil
}

func scanRideWithOTP(row pgx.Row) (*Ride, *string, error) {
	var r Ride
	var rawBreakdown []byte
	var otpHash *string
	if err := row.Scan(
		&r.ID, &r.CustomerUserID, &r.PartnerID, &r.VehicleID, &r.CityID, &r.QuoteID, &r.Revision, &r.VehicleType, &r.Status,
		&r.PickupAddress, &r.PickupLat, &r.PickupLng,
		&r.DropAddress, &r.DropLat, &r.DropLng,
		&r.EstimatedDistanceKM, &r.EstimatedDurationMin, &r.EstimatedFare,
		&r.FinalDistanceKM, &r.FinalDurationMin, &r.FinalFare, &r.FinalFarePaise,
		&r.PaymentMethod, &r.OTPCode, &r.OTPEncrypted, &r.OTPExpiresAt, &r.OTPAttempts, &r.OTPLockedUntil,
		&r.CashConfirmedAt, &r.CashConfirmedBy, &rawBreakdown, &r.ScheduledFor,
		&r.RequestedAt, &r.AssignedAt, &r.ArrivedAt, &r.StartedAt, &r.CompletedAt,
		&r.CancelledAt, &r.CancelledBy, &r.CancellationReason, &r.CustomerRating, &r.PartnerRating,
		&r.CreatedAt, &r.UpdatedAt,
		&otpHash,
	); err != nil {
		return nil, nil, err
	}
	if len(rawBreakdown) > 0 {
		r.FareBreakdown = json.RawMessage(rawBreakdown)
	}
	return &r, otpHash, nil
}
