package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrOfferNotFound is returned when GetOffer finds no row.
var ErrOfferNotFound = errors.New("offer: not found")

// ErrOfferAlreadyDecided is returned when AcceptOffer / RejectOffer find the
// row in any state other than 'sent'. Idempotent guard against double-accept.
var ErrOfferAlreadyDecided = errors.New("offer: already decided")

// CreateOfferInput is the input for CreateRideOffer.
type CreateOfferInput struct {
	RideID     uuid.UUID
	PartnerID  uuid.UUID
	Score      float64
	DistanceKM *float64
	ExpiresAt  time.Time
}

// CreateRideOffer inserts one offer row. ON CONFLICT (ride_id, partner_id)
// DO NOTHING — re-running the matcher with overlapping candidate sets is a
// no-op rather than an error.
func (s *Store) CreateRideOffer(ctx context.Context, in CreateOfferInput) (*RideOffer, error) {
	const q = `
        INSERT INTO rider_ride_offers (ride_id, partner_id, score, distance_km, expires_at, status)
        VALUES ($1, $2, $3, $4, $5, 'sent')
        ON CONFLICT (ride_id, partner_id) DO NOTHING
        RETURNING id, ride_id, partner_id, score, distance_km, expires_at, status, decided_at, created_at`
	row := s.db.QueryRow(ctx, q, in.RideID, in.PartnerID, in.Score, in.DistanceKM, in.ExpiresAt)
	o, err := scanOffer(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Conflict — pull the existing row so the caller has a stable id.
			return s.GetOfferByRidePartner(ctx, in.RideID, in.PartnerID)
		}
		return nil, fmt.Errorf("create offer: %w", err)
	}
	return o, nil
}

// GetOffer returns one offer by id.
func (s *Store) GetOffer(ctx context.Context, id uuid.UUID) (*RideOffer, error) {
	const q = `
        SELECT id, ride_id, partner_id, score, distance_km, expires_at, status, decided_at, created_at
        FROM rider_ride_offers
        WHERE id = $1`
	row := s.db.QueryRow(ctx, q, id)
	o, err := scanOffer(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOfferNotFound
		}
		return nil, err
	}
	return o, nil
}

// GetOfferByRidePartner is the lookup used by the ON CONFLICT replay in
// CreateRideOffer.
func (s *Store) GetOfferByRidePartner(ctx context.Context, rideID, partnerID uuid.UUID) (*RideOffer, error) {
	const q = `
        SELECT id, ride_id, partner_id, score, distance_km, expires_at, status, decided_at, created_at
        FROM rider_ride_offers
        WHERE ride_id = $1 AND partner_id = $2`
	row := s.db.QueryRow(ctx, q, rideID, partnerID)
	o, err := scanOffer(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOfferNotFound
		}
		return nil, err
	}
	return o, nil
}

// ListPendingOffersForPartner returns the partner's open offers (status='sent'
// AND expires_at > now()), most-recent first.
func (s *Store) ListPendingOffersForPartner(ctx context.Context, partnerID uuid.UUID) ([]RideOffer, error) {
	const q = `
        SELECT id, ride_id, partner_id, score, distance_km, expires_at, status, decided_at, created_at
        FROM rider_ride_offers
        WHERE partner_id = $1 AND status = 'sent' AND expires_at > NOW()
        ORDER BY created_at DESC
        LIMIT 50`
	rows, err := s.db.Query(ctx, q, partnerID)
	if err != nil {
		return nil, fmt.Errorf("list pending offers: %w", err)
	}
	defer rows.Close()
	var out []RideOffer
	for rows.Next() {
		o, err := scanOffer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// ListOffersForRide returns every offer ever sent for the ride.
func (s *Store) ListOffersForRide(ctx context.Context, rideID uuid.UUID) ([]RideOffer, error) {
	const q = `
        SELECT id, ride_id, partner_id, score, distance_km, expires_at, status, decided_at, created_at
        FROM rider_ride_offers
        WHERE ride_id = $1
        ORDER BY created_at ASC`
	rows, err := s.db.Query(ctx, q, rideID)
	if err != nil {
		return nil, fmt.Errorf("list offers for ride: %w", err)
	}
	defer rows.Close()
	var out []RideOffer
	for rows.Next() {
		o, err := scanOffer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// AcceptOfferTx is the race-safe accept path. Inside one transaction:
//  1. SELECT … FOR UPDATE on the offer row to block siblings.
//  2. Validate it's still 'sent'.
//  3. Mark it 'accepted'.
//  4. Mark every other 'sent' offer for the same ride as 'superseded'.
//
// Returns ErrOfferAlreadyDecided if the offer is no longer 'sent' (another
// partner won the race / it was expired by the cron).
func (s *Store) AcceptOfferTx(ctx context.Context, offerID, partnerID uuid.UUID) (*RideOffer, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const lockQ = `
        SELECT id, ride_id, partner_id, score, distance_km, expires_at, status, decided_at, created_at
        FROM rider_ride_offers
        WHERE id = $1 AND partner_id = $2
        FOR UPDATE`
	row := tx.QueryRow(ctx, lockQ, offerID, partnerID)
	offer, err := scanOffer(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOfferNotFound
		}
		return nil, fmt.Errorf("lock offer: %w", err)
	}
	if offer.Status != "sent" {
		return nil, ErrOfferAlreadyDecided
	}
	if !offer.ExpiresAt.After(time.Now()) {
		return nil, ErrOfferAlreadyDecided
	}

	const acceptQ = `
        UPDATE rider_ride_offers
        SET status = 'accepted', decided_at = NOW()
        WHERE id = $1
        RETURNING id, ride_id, partner_id, score, distance_km, expires_at, status, decided_at, created_at`
	row = tx.QueryRow(ctx, acceptQ, offerID)
	updated, err := scanOffer(row)
	if err != nil {
		return nil, fmt.Errorf("accept offer: %w", err)
	}

	const supersedeQ = `
        UPDATE rider_ride_offers
        SET status = 'superseded', decided_at = NOW()
        WHERE ride_id = $1 AND id <> $2 AND status = 'sent'`
	if _, err := tx.Exec(ctx, supersedeQ, offer.RideID, offerID); err != nil {
		return nil, fmt.Errorf("supersede siblings: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit accept: %w", err)
	}
	return updated, nil
}

// RejectOffer flips an offer to 'rejected'. Returns ErrOfferAlreadyDecided
// when the row is not 'sent'.
func (s *Store) RejectOffer(ctx context.Context, offerID, partnerID uuid.UUID) error {
	const q = `
        UPDATE rider_ride_offers
        SET status = 'rejected', decided_at = NOW()
        WHERE id = $1 AND partner_id = $2 AND status = 'sent'`
	tag, err := s.db.Exec(ctx, q, offerID, partnerID)
	if err != nil {
		return fmt.Errorf("reject offer: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrOfferAlreadyDecided
	}
	return nil
}

// ExpireStaleOffers flips every 'sent' offer past its expiry to 'expired'.
// Returns the number of rows expired.
func (s *Store) ExpireStaleOffers(ctx context.Context) (int64, error) {
	const q = `
        UPDATE rider_ride_offers
        SET status = 'expired', decided_at = NOW()
        WHERE status = 'sent' AND expires_at <= NOW()`
	tag, err := s.db.Exec(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("expire offers: %w", err)
	}
	return tag.RowsAffected(), nil
}

// CountOffersForRide returns the count of every offer for a ride keyed by
// status. Useful for telemetry + the "advance to next batch" decision.
func (s *Store) CountOffersForRide(ctx context.Context, rideID uuid.UUID) (map[string]int, error) {
	const q = `
        SELECT status, COUNT(*)::int
        FROM rider_ride_offers
        WHERE ride_id = $1
        GROUP BY status`
	rows, err := s.db.Query(ctx, q, rideID)
	if err != nil {
		return nil, fmt.Errorf("count offers: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		out[status] = n
	}
	return out, rows.Err()
}

func scanOffer(row pgx.Row) (*RideOffer, error) {
	var o RideOffer
	if err := row.Scan(&o.ID, &o.RideID, &o.PartnerID, &o.Score, &o.DistanceKM, &o.ExpiresAt, &o.Status, &o.DecidedAt, &o.CreatedAt); err != nil {
		return nil, err
	}
	return &o, nil
}
