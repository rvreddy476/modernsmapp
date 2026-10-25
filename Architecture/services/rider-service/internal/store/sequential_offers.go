package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/atpost/shared/outbox"
	"github.com/atpost/shared/partnerwork"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CreateSequentialRideOffer is the production dispatch path. The ride lock
// serializes competing workers; the partner lock bounds pending choices to three
// from different rides. A nil result means the candidate cannot be offered
// this ride now. Old offers are never returned as newly created offers.
func (s *Store) CreateSequentialRideOffer(ctx context.Context, in CreateOfferInput) (*RideOffer, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	var status string
	if err = tx.QueryRow(ctx, `SELECT status::text FROM rider_rides WHERE id=$1 FOR UPDATE`, in.RideID).Scan(&status); err != nil {
		return nil, err
	}
	if status != "requested" && status != "searching_partner" {
		return nil, nil
	}
	var live bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM rider_ride_offers WHERE ride_id=$1 AND status='sent' AND expires_at>clock_timestamp())`, in.RideID).Scan(&live); err != nil {
		return nil, err
	}
	if live {
		return nil, nil
	}
	var userID uuid.UUID
	var eligible bool
	if err = tx.QueryRow(ctx, `SELECT user_id, is_online AND status='approved' AND kyc_status='approved' FROM rider_partners WHERE id=$1 FOR UPDATE`, in.PartnerID).Scan(&userID, &eligible); err != nil {
		return nil, err
	}
	if !eligible {
		return nil, nil
	}
	if err = tx.QueryRow(ctx, `SELECT
	 (SELECT count(*) FROM rider_ride_offers WHERE partner_id=$1 AND status='sent' AND expires_at>clock_timestamp())>=3 OR
	 EXISTS(SELECT 1 FROM rider_rides WHERE partner_id=$1 AND status IN ('partner_assigned','partner_arriving','arrived','otp_verified','in_progress'))`, in.PartnerID).Scan(&live); err != nil {
		return nil, err
	}
	if live {
		return nil, nil
	}
	// Use the database clock after acquiring locks. Client/config supplied
	// durations cannot turn the agreed 20-second offer into a longer window.
	offer, err := scanOffer(tx.QueryRow(ctx, `INSERT INTO rider_ride_offers
	 (ride_id,partner_id,score,distance_km,expires_at,status)
	 VALUES($1,$2,$3,$4,clock_timestamp()+interval '20 seconds','sent')
	 ON CONFLICT(ride_id,partner_id) DO NOTHING
	 RETURNING id,ride_id,partner_id,score,distance_km,expires_at,status,decided_at,created_at`, in.RideID, in.PartnerID, in.Score, in.DistanceKM))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if partnerwork.CandidateUnavailable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{
		"ride_id": offer.RideID, "offer_id": offer.ID, "partner_id": offer.PartnerID,
		"partner_user_id": userID, "score": offer.Score, "expires_at": offer.ExpiresAt, "offered_at": offer.CreatedAt,
	})
	if err != nil {
		return nil, err
	}
	// This is the queue drained by the running publisher, not the legacy
	// rider_outbox_events table. Offer + notification intent commit together.
	if err = outbox.NewQueuer("rider").Enqueue(ctx, tx, "rider.ride.offered", "rider.partner."+in.PartnerID.String()+".offers", body); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return offer, nil
}

// ListRidesAwaitingOffer also recovers requested events missed during a broker
// outage. Old rides remain subject to the existing five-minute expiry policy.
func (s *Store) ListRidesAwaitingOffer(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `SELECT r.id FROM rider_rides r
	 WHERE r.status IN ('requested','searching_partner') AND r.requested_at>clock_timestamp()-interval '5 minutes'
	 AND NOT EXISTS(SELECT 1 FROM rider_ride_offers o WHERE o.ride_id=r.id AND o.status='sent' AND o.expires_at>clock_timestamp())
	 ORDER BY COALESCE((SELECT max(o.created_at) FROM rider_ride_offers o WHERE o.ride_id=r.id),r.requested_at),r.id LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// FairRideCandidates preserves the matcher ranking as a tie breaker but
// rotates opportunities toward partners offered the fewest jobs today.
func (s *Store) FairRideCandidates(ctx context.Context, ids []uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `SELECT p.id FROM unnest($1::uuid[]) WITH ORDINALITY p(id,rank)
	 LEFT JOIN LATERAL (SELECT count(*) AS n,max(created_at) AS last FROM rider_ride_offers
	 WHERE partner_id=p.id AND created_at>=date_trunc('day',clock_timestamp() AT TIME ZONE 'Asia/Kolkata') AT TIME ZONE 'Asia/Kolkata') h ON TRUE
	 ORDER BY h.n,h.last NULLS FIRST,p.rank`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

const SequentialOfferWindow = 20 * time.Second
