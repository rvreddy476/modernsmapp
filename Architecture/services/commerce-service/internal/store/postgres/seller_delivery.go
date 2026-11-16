// A seller marks their own self-shipped order delivered (migration 054).
//
// A shipment the seller recorded with their own courier has no carrier
// webhook behind it, so before this nothing ever moved its order past
// `shipped`. The write here is the courier path's (AdvanceDelivery) with the
// seller as the actor: the matrix, the history row with the seller's user id,
// every line following to `delivered` (delivered_at stamped by the server
// clock, which anchors the return window exactly as a courier delivery does)
// in ONE transaction with the shipment's own status, its timeline event and
// the record that the SELLER, not a courier, confirmed it.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrShipmentNotSellerManaged refuses a seller's mark on an order whose
// parcel a courier (or the local delivery partner) is tracking, or that has
// no shipment: only the carrier's own scan may say that one was delivered.
var ErrShipmentNotSellerManaged = errors.New("this order's shipment is tracked by a courier; only the courier can mark it delivered")

// SellerDeliveryMark is one seller's "Mark delivered".
type SellerDeliveryMark struct {
	OrderID     uuid.UUID
	SellerID    uuid.UUID
	ActorUserID uuid.UUID
	// DeliveredAt is the validated moment of hand-over (the service has
	// already refused a future one and one before shipped_at).
	DeliveredAt  time.Time
	Note         *string
	ProofMediaID *uuid.UUID
}

// SellerDeliveryNote is the order_status_history note and the shipment
// event remark of a seller's mark, so the timeline says who confirmed it.
const SellerDeliveryNote = "marked delivered by seller"

// MarkDeliveredBySeller moves a shipped (or out_for_delivery) order to
// delivered as the seller. Every shipment on the order must be this
// seller's and seller-managed, checked under the order's row lock.
// A repeat on an already delivered order is Applied=false, nil.
func (s *Store) MarkDeliveredBySeller(ctx context.Context, m SellerDeliveryMark) (StatusTransition, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return StatusTransition{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// The order first, under its row lock, so the shipment check and the
	// transition below see one state.
	var from string
	if err := tx.QueryRow(ctx, `SELECT status FROM orders WHERE id = $1 FOR UPDATE`, m.OrderID).Scan(&from); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StatusTransition{}, ErrOrderNotFoundP0
		}
		return StatusTransition{}, err
	}

	// Every shipment on the order is this seller's and seller-managed, and
	// there is at least one. A courier-tracked parcel is never the seller's
	// to call delivered, whatever the order's state.
	var total, markable int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE seller_managed AND seller_id = $2)
		  FROM shipments WHERE order_id = $1`, m.OrderID, m.SellerID).Scan(&total, &markable); err != nil {
		return StatusTransition{}, err
	}
	if total == 0 || markable != total {
		return StatusTransition{From: from, To: "delivered"}, ErrShipmentNotSellerManaged
	}

	t, err := transitionOrderStatusTx(ctx, tx, m.OrderID, "delivered", &m.ActorUserID, "seller", SellerDeliveryNote)
	if err != nil {
		return t, err
	}
	if !t.Applied {
		return t, tx.Commit(ctx)
	}

	// The shipments follow, with who confirmed it. delivered_at is the
	// seller's stated hand-over time; delivery_confirmed_at is the server's.
	rows, err := tx.Query(ctx, `
		UPDATE shipments
		   SET status = 'delivered', delivered_at = $3, last_event_at = $3, updated_at = clock_timestamp(),
		       delivery_confirmed_by = $4, delivery_confirmed_by_user = $5, delivery_confirmed_at = clock_timestamp(),
		       delivery_note = $6, delivery_proof_media_id = $7
		 WHERE order_id = $1 AND seller_id = $2 AND seller_managed AND status <> 'delivered'
		 RETURNING id`,
		m.OrderID, m.SellerID, m.DeliveredAt, ConfirmedBySeller, m.ActorUserID, m.Note, m.ProofMediaID)
	if err != nil {
		return t, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return t, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return t, err
	}
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `
			INSERT INTO shipment_events (shipment_id, status, location, remark, occurred_at)
			VALUES ($1, 'delivered', '', $2, $3)`, id, SellerDeliveryNote, m.DeliveredAt); err != nil {
			return t, fmt.Errorf("shipment event: %w", err)
		}
	}
	if err := followOrderItemsTx(ctx, tx, m.OrderID, "delivered"); err != nil {
		return t, err
	}
	return t, tx.Commit(ctx)
}
