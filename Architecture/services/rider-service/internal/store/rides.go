package store

import (
	"context"
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

// CreateRideInput captures the fields collected by POST /v1/rider/rides.
type CreateRideInput struct {
	CustomerUserID       uuid.UUID
	CityID               *uuid.UUID
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
	PaymentMethod        *string
	// ScheduledFor — when non-nil + future, the row is inserted at
	// status='scheduled' with scheduled_for set. The activation worker
	// promotes it to 'requested' ≈ T-15 min before pickup.
	ScheduledFor *time.Time
}

// CreateRide inserts a row in `requested` status. Sprint 2 fills in matching,
// state machine, and cancellation paths. G4.5 adds optional ScheduledFor to
// park the row in `scheduled` until the activation worker takes it live.
func (s *Store) CreateRide(ctx context.Context, in CreateRideInput) (*Ride, error) {
	// PostGIS notes: ST_MakePoint takes (lng, lat) — that order is part of
	// the geography contract. Cast through geography(POINT,4326) to keep the
	// column happy.
	status := "requested"
	if in.ScheduledFor != nil && in.ScheduledFor.After(time.Now()) {
		status = "scheduled"
	}
	const q = `
        INSERT INTO rider_rides (
            customer_user_id, city_id, vehicle_type, status,
            pickup_address, pickup_location, drop_address, drop_location,
            estimated_distance_km, estimated_duration_min, estimated_fare, payment_method,
            scheduled_for
        ) VALUES (
            $1, $2, $3::rider_vehicle_type, $14::rider_ride_status,
            $4, ST_SetSRID(ST_MakePoint($5, $6), 4326)::geography,
            $7, ST_SetSRID(ST_MakePoint($8, $9), 4326)::geography,
            $10, $11, $12, $13, $15
        )
        RETURNING id, customer_user_id, partner_id, vehicle_id, city_id, vehicle_type, status,
                  pickup_address, ST_Y(pickup_location::geometry), ST_X(pickup_location::geometry),
                  drop_address, ST_Y(drop_location::geometry), ST_X(drop_location::geometry),
                  estimated_distance_km, estimated_duration_min, estimated_fare, payment_method,
                  otp_expires_at, requested_at, created_at, updated_at`
	row := s.db.QueryRow(ctx, q,
		in.CustomerUserID, in.CityID, in.VehicleType,
		in.PickupAddress, in.PickupLng, in.PickupLat,
		in.DropAddress, in.DropLng, in.DropLat,
		in.EstimatedDistanceKM, in.EstimatedDurationMin, in.EstimatedFare, in.PaymentMethod,
		status, in.ScheduledFor,
	)
	return scanRide(row)
}

// ListScheduledRidesDue returns scheduled rides whose
// scheduled_for - scheduled_lead_min minutes is in the past — i.e.
// ready for activation. SKIP LOCKED keeps concurrent workers safe.
func (s *Store) ListScheduledRidesDue(ctx context.Context, batch int) ([]Ride, error) {
	if batch <= 0 {
		batch = 25
	}
	const q = `
        SELECT id, customer_user_id, partner_id, vehicle_id, city_id, vehicle_type, status,
               pickup_address, ST_Y(pickup_location::geometry), ST_X(pickup_location::geometry),
               drop_address, ST_Y(drop_location::geometry), ST_X(drop_location::geometry),
               estimated_distance_km, estimated_duration_min, estimated_fare, payment_method,
               otp_expires_at, requested_at, created_at, updated_at
        FROM rider_rides
        WHERE status = 'scheduled'
          AND scheduled_for IS NOT NULL
          AND scheduled_for - (scheduled_lead_min * INTERVAL '1 minute') <= NOW()
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

// ActivateScheduledRide promotes the row from 'scheduled' to
// 'requested' so the dispatch consumer picks it up. Idempotent —
// returns ErrRideNotFound if the row already moved.
func (s *Store) ActivateScheduledRide(ctx context.Context, rideID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE rider_rides
		SET status = 'requested', activated_at = NOW()
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

// GetRide returns one ride by id.
func (s *Store) GetRide(ctx context.Context, id uuid.UUID) (*Ride, error) {
	const q = `
        SELECT id, customer_user_id, partner_id, vehicle_id, city_id, vehicle_type, status,
               pickup_address, ST_Y(pickup_location::geometry), ST_X(pickup_location::geometry),
               drop_address, ST_Y(drop_location::geometry), ST_X(drop_location::geometry),
               estimated_distance_km, estimated_duration_min, estimated_fare, payment_method,
               otp_expires_at, requested_at, created_at, updated_at
        FROM rider_rides
        WHERE id = $1`
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

// ListRidesByCustomer returns recent rides for the customer.
func (s *Store) ListRidesByCustomer(ctx context.Context, customerID uuid.UUID, limit int) ([]Ride, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const q = `
        SELECT id, customer_user_id, partner_id, vehicle_id, city_id, vehicle_type, status,
               pickup_address, ST_Y(pickup_location::geometry), ST_X(pickup_location::geometry),
               drop_address, ST_Y(drop_location::geometry), ST_X(drop_location::geometry),
               estimated_distance_km, estimated_duration_min, estimated_fare, payment_method,
               otp_expires_at, requested_at, created_at, updated_at
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

func scanRide(row pgx.Row) (*Ride, error) {
	var r Ride
	if err := row.Scan(
		&r.ID, &r.CustomerUserID, &r.PartnerID, &r.VehicleID, &r.CityID, &r.VehicleType, &r.Status,
		&r.PickupAddress, &r.PickupLat, &r.PickupLng,
		&r.DropAddress, &r.DropLat, &r.DropLng,
		&r.EstimatedDistanceKM, &r.EstimatedDurationMin, &r.EstimatedFare, &r.PaymentMethod,
		&r.OTPExpiresAt, &r.RequestedAt, &r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &r, nil
}

// --- Sprint 2: state transitions, OTP, history ----------------------------

// TransitionRide updates the ride status only when the current status equals
// `from`, returning ErrInvalidTransition otherwise. The caller (service-layer
// state machine) has already validated the (from, to) pair against the
// allow-table — this guards against concurrent writers (two partners hitting
// arrived at the same time) by checking the row's current status atomically.
//
// Runs inside the caller's transaction when one is provided via WithTx.
func (s *Store) TransitionRide(ctx context.Context, rideID uuid.UUID, from, to string) error {
	const q = `
        UPDATE rider_rides
        SET status     = $3::rider_ride_status,
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
		return nil, fmt.Errorf("list ride status history: %w", err)
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

// AssignRidePartner sets partner_id + vehicle_id + otp_hash + otp_expires_at
// in one update. Used immediately after the matched partner accepts an offer.
func (s *Store) AssignRidePartner(ctx context.Context, rideID, partnerID, vehicleID uuid.UUID, otpHash string, otpExpires time.Time) error {
	const q = `
        UPDATE rider_rides
        SET partner_id     = $2,
            vehicle_id     = $3,
            otp_hash       = $4,
            otp_expires_at = $5,
            assigned_at    = NOW(),
            updated_at     = NOW()
        WHERE id = $1`
	tag, err := s.db.Exec(ctx, q, rideID, partnerID, vehicleID, otpHash, otpExpires)
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
	const q = `UPDATE rider_rides SET partner_arriving_at = NOW(), updated_at = NOW() WHERE id = $1`
	_, err := s.db.Exec(ctx, q, rideID)
	return err
}

// SetArrivedAt stamps arrived_at = now().
func (s *Store) SetArrivedAt(ctx context.Context, rideID uuid.UUID) error {
	const q = `UPDATE rider_rides SET arrived_at = NOW(), updated_at = NOW() WHERE id = $1`
	_, err := s.db.Exec(ctx, q, rideID)
	return err
}

// SetStartedAt stamps started_at = now().
func (s *Store) SetStartedAt(ctx context.Context, rideID uuid.UUID) error {
	const q = `UPDATE rider_rides SET started_at = NOW(), updated_at = NOW() WHERE id = $1`
	_, err := s.db.Exec(ctx, q, rideID)
	return err
}

// CompleteRideInput captures the partner-supplied final telemetry.
type CompleteRideInput struct {
	RideID            uuid.UUID
	FinalDistanceKM   float64
	FinalDurationMin  int
	FinalFareINR      float64
	FinalFarePaise    int64
	FlaggedForReview  bool
}

// FinalizeRide stamps the final fare + telemetry + completed_at.
func (s *Store) FinalizeRide(ctx context.Context, in CompleteRideInput) error {
	const q = `
        UPDATE rider_rides
        SET final_distance_km   = $2,
            final_duration_min  = $3,
            final_fare          = $4,
            final_fare_paise    = $5,
            flagged_for_review  = $6,
            completed_at        = NOW(),
            updated_at          = NOW()
        WHERE id = $1`
	tag, err := s.db.Exec(ctx, q, in.RideID, in.FinalDistanceKM, in.FinalDurationMin, in.FinalFareINR, in.FinalFarePaise, in.FlaggedForReview)
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

// MarkRideCancelled stamps cancellation fields. Status is set by the caller
// via TransitionRide so the state-machine guard fires.
func (s *Store) MarkRideCancelled(ctx context.Context, in CancelRideInput) error {
	const q = `
        UPDATE rider_rides
        SET cancellation_fee_paise = $2,
            cancellation_reason    = $3,
            cancelled_by_kind      = $4,
            cancelled_at           = NOW(),
            updated_at             = NOW()
        WHERE id = $1`
	tag, err := s.db.Exec(ctx, q, in.RideID, in.CancellationFeePaise, in.Reason, in.CancelledByKind)
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
	const q = `UPDATE rider_rides SET rating = $2, rating_comment = $3, updated_at = NOW() WHERE id = $1`
	tag, err := s.db.Exec(ctx, q, rideID, rating, comment)
	if err != nil {
		return fmt.Errorf("set rating: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRideNotFound
	}
	return nil
}

// SetShareToken sets a one-time share token for the ride. No-op if a token
// is already present (idempotent — same URL across calls).
func (s *Store) SetShareToken(ctx context.Context, rideID uuid.UUID, token string) (string, error) {
	const q = `
        UPDATE rider_rides
        SET share_token = COALESCE(share_token, $2),
            updated_at  = NOW()
        WHERE id = $1
        RETURNING share_token`
	var got string
	if err := s.db.QueryRow(ctx, q, rideID, token).Scan(&got); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrRideNotFound
		}
		return "", fmt.Errorf("set share token: %w", err)
	}
	return got, nil
}

// GetRideWithOTP returns the OTP hash + expiry alongside the ride row.
// Used by StartRide to verify the partner-typed OTP.
func (s *Store) GetRideWithOTP(ctx context.Context, rideID uuid.UUID) (*Ride, *string, *time.Time, error) {
	const q = `
        SELECT id, customer_user_id, partner_id, vehicle_id, city_id, vehicle_type, status,
               pickup_address, ST_Y(pickup_location::geometry), ST_X(pickup_location::geometry),
               drop_address, ST_Y(drop_location::geometry), ST_X(drop_location::geometry),
               estimated_distance_km, estimated_duration_min, estimated_fare, payment_method,
               otp_expires_at, requested_at, created_at, updated_at,
               otp_hash
        FROM rider_rides
        WHERE id = $1`
	row := s.db.QueryRow(ctx, q, rideID)
	var r Ride
	var otpHash *string
	if err := row.Scan(
		&r.ID, &r.CustomerUserID, &r.PartnerID, &r.VehicleID, &r.CityID, &r.VehicleType, &r.Status,
		&r.PickupAddress, &r.PickupLat, &r.PickupLng,
		&r.DropAddress, &r.DropLat, &r.DropLng,
		&r.EstimatedDistanceKM, &r.EstimatedDurationMin, &r.EstimatedFare, &r.PaymentMethod,
		&r.OTPExpiresAt, &r.RequestedAt, &r.CreatedAt, &r.UpdatedAt,
		&otpHash,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil, ErrRideNotFound
		}
		return nil, nil, nil, err
	}
	return &r, otpHash, r.OTPExpiresAt, nil
}

// ListStaleRides returns rides stuck in `requested` or `searching_partner` for
// longer than `olderThan`. Used by the cron expirer.
func (s *Store) ListStaleRides(ctx context.Context, olderThan time.Duration) ([]Ride, error) {
	const q = `
        SELECT id, customer_user_id, partner_id, vehicle_id, city_id, vehicle_type, status,
               pickup_address, ST_Y(pickup_location::geometry), ST_X(pickup_location::geometry),
               drop_address, ST_Y(drop_location::geometry), ST_X(drop_location::geometry),
               estimated_distance_km, estimated_duration_min, estimated_fare, payment_method,
               otp_expires_at, requested_at, created_at, updated_at
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

// ListRidesByPartner returns recent rides for the partner (today / week / etc.
// is filtered by the caller via `since`).
func (s *Store) ListRidesByPartner(ctx context.Context, partnerID uuid.UUID, since time.Time, limit int) ([]Ride, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const q = `
        SELECT id, customer_user_id, partner_id, vehicle_id, city_id, vehicle_type, status,
               pickup_address, ST_Y(pickup_location::geometry), ST_X(pickup_location::geometry),
               drop_address, ST_Y(drop_location::geometry), ST_X(drop_location::geometry),
               estimated_distance_km, estimated_duration_min, estimated_fare, payment_method,
               otp_expires_at, requested_at, created_at, updated_at
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

// PartnerEarnings returns ride count + sum(final_fare_paise) for the partner
// across the [since, now) window. completed-rides only.
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
