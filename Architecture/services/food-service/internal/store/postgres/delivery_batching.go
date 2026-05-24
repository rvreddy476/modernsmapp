package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DeliveryBatch is one pickup group. Members are the (order_id,
// sequence) tuples the partner picks/drops in order.
type DeliveryBatch struct {
	ID           uuid.UUID         `json:"id"`
	RestaurantID uuid.UUID         `json:"restaurant_id"`
	Status       string            `json:"status"`
	CreatedAt    string            `json:"created_at"`
	AssignedAt   *string           `json:"assigned_at,omitempty"`
	CompletedAt  *string           `json:"completed_at,omitempty"`
	Members      []BatchMember     `json:"members,omitempty"`
}

// BatchMember is one order's slot within a batch.
type BatchMember struct {
	OrderID  uuid.UUID `json:"order_id"`
	Sequence int       `json:"sequence"`
}

// ReadyOrderForBatching is what the worker needs to group orders. We
// pull restaurant_id + placed_at so it can window orders together
// regardless of how many tick cycles they spanned.
type ReadyOrderForBatching struct {
	OrderID      uuid.UUID
	RestaurantID uuid.UUID
	PlacedAt     time.Time
}

// ListUnbatchedReadyOrders returns DELIVERY_ASSIGNING orders that don't
// yet belong to a pending batch. The dispatch worker groups these by
// restaurant within a time window.
func (s *Store) ListUnbatchedReadyOrders(ctx context.Context, batch int) ([]ReadyOrderForBatching, error) {
	if batch <= 0 {
		batch = 25
	}
	rows, err := s.db.Query(ctx, `
		SELECT o.id, o.restaurant_id, o.placed_at
		FROM food.orders o
		LEFT JOIN food.delivery_assignments da
			ON da.order_id = o.id AND da.status NOT IN ('CANCELLED', 'CREATED')
		LEFT JOIN food.delivery_offers offers
			ON offers.order_id = o.id AND offers.status = 'pending'
		WHERE o.status = 'DELIVERY_ASSIGNING'
		  AND da.id IS NULL
		  AND offers.id IS NULL
		ORDER BY o.restaurant_id, o.placed_at ASC
		LIMIT $1
	`, batch)
	if err != nil {
		return nil, fmt.Errorf("list unbatched ready orders: %w", err)
	}
	defer rows.Close()
	var out []ReadyOrderForBatching
	for rows.Next() {
		var r ReadyOrderForBatching
		if err := rows.Scan(&r.OrderID, &r.RestaurantID, &r.PlacedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateBatch inserts a delivery_batch row and points every member
// order's existing delivery_assignment row at it (or upserts one if
// the assignment row doesn't exist yet — the existing flow only
// creates it on accept, so we provision it here too).
//
// The members slice arrives in pickup order (oldest placed_at first);
// we record `batch_sequence = index + 1` so the partner UI can render
// them in the right order without re-sorting.
func (s *Store) CreateBatch(ctx context.Context, restaurantID uuid.UUID, orderIDs []uuid.UUID) (*DeliveryBatch, error) {
	if len(orderIDs) < 2 {
		return nil, fmt.Errorf("batch needs >= 2 orders, got %d", len(orderIDs))
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var batchID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO food.delivery_batches (restaurant_id) VALUES ($1) RETURNING id
	`, restaurantID).Scan(&batchID); err != nil {
		return nil, fmt.Errorf("insert batch: %w", err)
	}
	for i, orderID := range orderIDs {
		seq := i + 1
		// Upsert a CREATED assignment row pointing at this batch.
		// Accept-time later flips it to ASSIGNED and stamps the partner.
		if _, err := tx.Exec(ctx, `
			INSERT INTO food.delivery_assignments (order_id, status, batch_id, batch_sequence)
			VALUES ($1, 'CREATED', $2, $3)
			ON CONFLICT (order_id) DO UPDATE
			SET batch_id = EXCLUDED.batch_id,
			    batch_sequence = EXCLUDED.batch_sequence
			WHERE food.delivery_assignments.status = 'CREATED'
		`, orderID, batchID, seq); err != nil {
			return nil, fmt.Errorf("link order %s to batch: %w", orderID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	out := &DeliveryBatch{
		ID:           batchID,
		RestaurantID: restaurantID,
		Status:       "pending",
		Members:      make([]BatchMember, 0, len(orderIDs)),
	}
	for i, id := range orderIDs {
		out.Members = append(out.Members, BatchMember{OrderID: id, Sequence: i + 1})
	}
	return out, nil
}

// CreateDeliveryOfferForBatch mints one offer for the partner that
// covers every order in the batch. The offer carries batch_id so
// AcceptDeliveryOfferTx knows to claim the whole group atomically.
//
// We pin the order_id column to the first (earliest-placed) order so
// the existing UNIQUE(order_id, partner_id) constraint still acts as
// the dedup guard. Sibling orders are reached via the batch link.
func (s *Store) CreateDeliveryOfferForBatch(ctx context.Context, batchID, anchorOrderID, partnerID uuid.UUID, expiresAt time.Time) (*DeliveryOffer, error) {
	var o DeliveryOffer
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.delivery_offers (order_id, delivery_partner_id, batch_id, expires_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (order_id, delivery_partner_id) DO UPDATE SET expires_at = food.delivery_offers.expires_at
		RETURNING id, order_id, delivery_partner_id, status::text, distance_km, expires_at::text,
			responded_at::text, reject_reason, created_at::text
	`, anchorOrderID, partnerID, batchID, expiresAt).Scan(
		&o.ID, &o.OrderID, &o.DeliveryPartnerID, &o.Status, &o.DistanceKM,
		&o.ExpiresAt, &o.RespondedAt, &o.RejectReason, &o.CreatedAt,
	); err != nil {
		return nil, err
	}
	return &o, nil
}

// AcceptBatchOfferTx accepts an offer that points at a batch and
// flips every member order to DELIVERY_ASSIGNED in one transaction.
// Returns the batch (with members + sequence) and the partner ID so
// the caller can mint per-order OTPs and emit the assignment event.
//
// Idempotent on already-accepted: re-running is a no-op once status is
// 'assigned'.
func (s *Store) AcceptBatchOfferTx(ctx context.Context, userID, offerID uuid.UUID) (*DeliveryBatch, uuid.UUID, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, uuid.Nil, err
	}
	defer tx.Rollback(ctx)
	var partnerID uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT id FROM food.delivery_partners WHERE user_id = $1 AND status = 'ACTIVE'
	`, userID).Scan(&partnerID); err != nil {
		return nil, uuid.Nil, fmt.Errorf("partner lookup: %w", err)
	}
	var batchID uuid.UUID
	var offerStatus string
	if err := tx.QueryRow(ctx, `
		SELECT batch_id, status::text FROM food.delivery_offers
		WHERE id = $1 AND delivery_partner_id = $2 AND batch_id IS NOT NULL
		FOR UPDATE
	`, offerID, partnerID).Scan(&batchID, &offerStatus); err != nil {
		return nil, uuid.Nil, fmt.Errorf("offer lookup: %w", err)
	}
	if offerStatus != "pending" {
		return nil, uuid.Nil, fmt.Errorf("offer not pending: %s", offerStatus)
	}
	// Lock the batch + every member assignment for the duration.
	var restaurantID uuid.UUID
	var batchStatus string
	if err := tx.QueryRow(ctx, `
		SELECT restaurant_id, status FROM food.delivery_batches
		WHERE id = $1 FOR UPDATE
	`, batchID).Scan(&restaurantID, &batchStatus); err != nil {
		return nil, uuid.Nil, fmt.Errorf("batch lookup: %w", err)
	}
	if batchStatus != "pending" {
		return nil, uuid.Nil, fmt.Errorf("batch not pending: %s", batchStatus)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.delivery_offers SET status = 'accepted', responded_at = NOW()
		WHERE id = $1
	`, offerID); err != nil {
		return nil, uuid.Nil, err
	}
	// All sibling offers for the same batch become superseded.
	if _, err := tx.Exec(ctx, `
		UPDATE food.delivery_offers SET status = 'superseded', responded_at = NOW()
		WHERE batch_id = $1 AND id <> $2 AND status = 'pending'
	`, batchID, offerID); err != nil {
		return nil, uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.delivery_batches SET status = 'assigned', assigned_at = NOW()
		WHERE id = $1
	`, batchID); err != nil {
		return nil, uuid.Nil, err
	}
	// Flip every member assignment to ASSIGNED with this partner.
	if _, err := tx.Exec(ctx, `
		UPDATE food.delivery_assignments
		SET delivery_partner_id = $1, status = 'ASSIGNED', accepted_at = NOW()
		WHERE batch_id = $2 AND status = 'CREATED'
	`, partnerID, batchID); err != nil {
		return nil, uuid.Nil, err
	}
	// Move every member order to DELIVERY_ASSIGNED.
	if _, err := tx.Exec(ctx, `
		UPDATE food.orders SET status = 'DELIVERY_ASSIGNED'
		WHERE id IN (
			SELECT order_id FROM food.delivery_assignments WHERE batch_id = $1
		) AND status = 'DELIVERY_ASSIGNING'
	`, batchID); err != nil {
		return nil, uuid.Nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT order_id, COALESCE(batch_sequence, 0) FROM food.delivery_assignments
		WHERE batch_id = $1 ORDER BY batch_sequence ASC
	`, batchID)
	if err != nil {
		return nil, uuid.Nil, err
	}
	var members []BatchMember
	for rows.Next() {
		var m BatchMember
		if err := rows.Scan(&m.OrderID, &m.Sequence); err != nil {
			rows.Close()
			return nil, uuid.Nil, err
		}
		members = append(members, m)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return nil, uuid.Nil, err
	}
	return &DeliveryBatch{
		ID:           batchID,
		RestaurantID: restaurantID,
		Status:       "assigned",
		Members:      members,
	}, partnerID, nil
}

// GetBatchForOrder returns the batch a given order belongs to (and its
// members), or nil if the order isn't batched. Partner + customer UI
// uses this to render "X of Y" + sequence + the sibling order summary.
func (s *Store) GetBatchForOrder(ctx context.Context, orderID uuid.UUID) (*DeliveryBatch, error) {
	var b DeliveryBatch
	err := s.db.QueryRow(ctx, `
		SELECT b.id, b.restaurant_id, b.status, b.created_at::text,
			b.assigned_at::text, b.completed_at::text
		FROM food.delivery_batches b
		JOIN food.delivery_assignments da ON da.batch_id = b.id
		WHERE da.order_id = $1
	`, orderID).Scan(&b.ID, &b.RestaurantID, &b.Status, &b.CreatedAt, &b.AssignedAt, &b.CompletedAt)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT order_id, COALESCE(batch_sequence, 0)
		FROM food.delivery_assignments
		WHERE batch_id = $1 ORDER BY batch_sequence ASC
	`, b.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m BatchMember
		if err := rows.Scan(&m.OrderID, &m.Sequence); err != nil {
			return nil, err
		}
		b.Members = append(b.Members, m)
	}
	return &b, rows.Err()
}
