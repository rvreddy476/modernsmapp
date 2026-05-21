package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DeliveryOffer mirrors a row in food.delivery_offers.
type DeliveryOffer struct {
	ID                uuid.UUID `json:"id"`
	OrderID           uuid.UUID `json:"order_id"`
	DeliveryPartnerID uuid.UUID `json:"delivery_partner_id"`
	Status            string    `json:"status"`
	DistanceKM        *float64  `json:"distance_km,omitempty"`
	ExpiresAt         string    `json:"expires_at"`
	RespondedAt       *string   `json:"responded_at,omitempty"`
	RejectReason      *string   `json:"reject_reason,omitempty"`
	CreatedAt         string    `json:"created_at"`
}

// ListUnassignedReadyOrders returns orders that are waiting for a
// delivery offer fan-out. We treat `DELIVERY_ASSIGNING` as the queue
// for this — the existing partner-status transition sets it.
func (s *Store) ListUnassignedReadyOrders(ctx context.Context, batch int) ([]uuid.UUID, error) {
	if batch <= 0 {
		batch = 25
	}
	rows, err := s.db.Query(ctx, `
		SELECT o.id
		FROM food.orders o
		LEFT JOIN food.delivery_assignments da
			ON da.order_id = o.id AND da.status NOT IN ('CANCELLED', 'CREATED')
		WHERE o.status = 'DELIVERY_ASSIGNING'
		  AND da.id IS NULL
		ORDER BY o.placed_at ASC
		LIMIT $1
	`, batch)
	if err != nil {
		return nil, fmt.Errorf("list unassigned ready orders: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ListEligibleDeliveryPartners returns online + APPROVED delivery
// partners in the restaurant's city. The dispatch worker calls this
// then mints up to N offers.
func (s *Store) ListEligibleDeliveryPartners(ctx context.Context, restaurantCity string, limit int) ([]uuid.UUID, error) {
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	rows, err := s.db.Query(ctx, `
		SELECT id FROM food.delivery_partners
		WHERE status = 'ACTIVE' AND is_online = TRUE
		  AND (city = $1 OR $1 = '')
		ORDER BY updated_at DESC
		LIMIT $2
	`, restaurantCity, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// CreateDeliveryOffer is idempotent on (order_id, delivery_partner_id)
// via the unique constraint; a re-insert returns the existing row.
func (s *Store) CreateDeliveryOffer(ctx context.Context, orderID, partnerID uuid.UUID, expiresAt time.Time) (*DeliveryOffer, error) {
	var o DeliveryOffer
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.delivery_offers (order_id, delivery_partner_id, expires_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (order_id, delivery_partner_id) DO UPDATE SET expires_at = food.delivery_offers.expires_at
		RETURNING id, order_id, delivery_partner_id, status::text, distance_km, expires_at::text,
			responded_at::text, reject_reason, created_at::text
	`, orderID, partnerID, expiresAt).Scan(
		&o.ID, &o.OrderID, &o.DeliveryPartnerID, &o.Status, &o.DistanceKM,
		&o.ExpiresAt, &o.RespondedAt, &o.RejectReason, &o.CreatedAt,
	); err != nil {
		return nil, err
	}
	return &o, nil
}

// ListMyPendingDeliveryOffers returns offers awaiting the partner's
// accept/reject. `userID` is the partner's user_id; we resolve to
// delivery_partner.id via a sub-select.
func (s *Store) ListMyPendingDeliveryOffers(ctx context.Context, userID uuid.UUID) ([]DeliveryOffer, error) {
	rows, err := s.db.Query(ctx, `
		SELECT o.id, o.order_id, o.delivery_partner_id, o.status::text,
			o.distance_km, o.expires_at::text, o.responded_at::text, o.reject_reason, o.created_at::text
		FROM food.delivery_offers o
		JOIN food.delivery_partners dp ON dp.id = o.delivery_partner_id
		WHERE dp.user_id = $1
		  AND o.status = 'pending'
		  AND o.expires_at > NOW()
		ORDER BY o.expires_at ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeliveryOffer
	for rows.Next() {
		var o DeliveryOffer
		if err := rows.Scan(&o.ID, &o.OrderID, &o.DeliveryPartnerID, &o.Status,
			&o.DistanceKM, &o.ExpiresAt, &o.RespondedAt, &o.RejectReason, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// AcceptDeliveryOfferTx atomically:
//  1. Resolves the partner from userID, validating ownership.
//  2. Marks this offer accepted; supersedes all sibling offers.
//  3. Inserts/updates food.delivery_assignments to this partner.
//  4. Moves the order to DELIVERY_ASSIGNED.
// First caller wins via SELECT ... FOR UPDATE on the order row.
func (s *Store) AcceptDeliveryOfferTx(ctx context.Context, userID, offerID uuid.UUID) (*DeliveryOffer, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var partnerID uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT id FROM food.delivery_partners WHERE user_id = $1 AND status = 'ACTIVE'
	`, userID).Scan(&partnerID); err != nil {
		return nil, fmt.Errorf("partner lookup: %w", err)
	}
	var offer DeliveryOffer
	if err := tx.QueryRow(ctx, `
		SELECT id, order_id, delivery_partner_id, status::text, distance_km,
			expires_at::text, responded_at::text, reject_reason, created_at::text
		FROM food.delivery_offers
		WHERE id = $1 AND delivery_partner_id = $2
		FOR UPDATE
	`, offerID, partnerID).Scan(&offer.ID, &offer.OrderID, &offer.DeliveryPartnerID, &offer.Status,
		&offer.DistanceKM, &offer.ExpiresAt, &offer.RespondedAt, &offer.RejectReason, &offer.CreatedAt); err != nil {
		return nil, err
	}
	if offer.Status != "pending" {
		return nil, fmt.Errorf("offer not pending: %s", offer.Status)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.delivery_offers
		SET status = 'accepted', responded_at = NOW()
		WHERE id = $1
	`, offerID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.delivery_offers
		SET status = 'superseded', responded_at = NOW()
		WHERE order_id = $1 AND id <> $2 AND status = 'pending'
	`, offer.OrderID, offerID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.delivery_assignments (order_id, delivery_partner_id, status, accepted_at)
		VALUES ($1, $2, 'ASSIGNED', NOW())
		ON CONFLICT (order_id) DO UPDATE
		SET delivery_partner_id = EXCLUDED.delivery_partner_id,
			status = 'ASSIGNED',
			accepted_at = NOW()
	`, offer.OrderID, partnerID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.orders SET status = 'DELIVERY_ASSIGNED' WHERE id = $1 AND status = 'DELIVERY_ASSIGNING'
	`, offer.OrderID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	offer.Status = "accepted"
	return &offer, nil
}

// RejectDeliveryOffer marks an offer rejected. The next dispatch tick
// will fan out fresh offers (or expire the order) if no one accepts.
func (s *Store) RejectDeliveryOffer(ctx context.Context, userID, offerID uuid.UUID, reason string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE food.delivery_offers o
		SET status = 'rejected', responded_at = NOW(), reject_reason = NULLIF($3, '')
		FROM food.delivery_partners dp
		WHERE o.id = $1 AND dp.id = o.delivery_partner_id
		  AND dp.user_id = $2 AND o.status = 'pending'
	`, offerID, userID, reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ExpireDeliveryOffers transitions any pending offer past its
// expires_at into the `expired` status. Returns affected count.
func (s *Store) ExpireDeliveryOffers(ctx context.Context) (int, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE food.delivery_offers
		SET status = 'expired', responded_at = NOW()
		WHERE status = 'pending' AND expires_at <= NOW()
	`)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
