package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/food-service/internal/orderstate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// KitchenOrder is one row in the partner kitchen queue. Mirrors the
// minimal projection the partner mobile app needs to render the queue
// (item count, gross total, ETA-to-breach, customer instruction).
type KitchenOrder struct {
	ID                  uuid.UUID `json:"id"`
	OrderNumber         string    `json:"order_number"`
	Status              string    `json:"status"`
	FinalAmount         float64   `json:"final_amount"`
	ItemCount           int       `json:"item_count"`
	CustomerInstruction *string   `json:"customer_instruction,omitempty"`
	PlacedAt            string    `json:"placed_at"`
	AcceptDeadlineAt    *string   `json:"accept_deadline_at,omitempty"`
	SecondsToBreach     *int      `json:"seconds_to_breach,omitempty"`
	// B8 (additive): the paise sibling of final_amount, and the deadline in
	// RFC 3339 UTC. accept_deadline_at stays the Postgres text it always was.
	FinalAmountPaise        int64   `json:"final_amount_paise"`
	AcceptDeadlineAtRFC3339 *string `json:"accept_deadline_at_rfc3339,omitempty"`
}

// ListKitchenQueue returns CONFIRMED orders awaiting partner accept
// for the restaurant, ordered by accept_deadline_at ASC (oldest /
// most-urgent first). Partner ownership is enforced.
func (s *Store) ListKitchenQueue(ctx context.Context, ownerID, restaurantID uuid.UUID) ([]KitchenOrder, error) {
	// First confirm the restaurant belongs to the owner — keep the
	// confidential-info leak vector closed.
	var ownerCount int
	if err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM food.restaurants
		WHERE id = $1 AND owner_user_id = $2
	`, restaurantID, ownerID).Scan(&ownerCount); err != nil {
		return nil, fmt.Errorf("verify ownership: %w", err)
	}
	if ownerCount == 0 {
		return nil, pgx.ErrNoRows
	}
	rows, err := s.db.Query(ctx, `
		SELECT
			o.id,
			o.order_number,
			o.status::text,
			o.final_amount::float8,
			COALESCE((SELECT COUNT(*) FROM food.order_items WHERE order_id = o.id), 0) AS item_count,
			o.customer_instruction,
			o.placed_at::text,
			o.accept_deadline_at::text,
			CASE
				WHEN o.accept_deadline_at IS NULL THEN NULL
				ELSE GREATEST(0, EXTRACT(EPOCH FROM (o.accept_deadline_at - NOW()))::int)
			END AS seconds_to_breach,
			o.accept_deadline_at
		FROM food.orders o
		WHERE o.restaurant_id = $1
		  AND o.status = 'CONFIRMED'
		-- NULLS LAST keeps deadline-less rows after timed ones; the
		-- placed_at + id tie-breakers make the order fully
		-- deterministic so pagination + UI diffing are stable.
		ORDER BY o.accept_deadline_at ASC NULLS LAST, o.placed_at ASC, o.id ASC
		LIMIT 200
	`, restaurantID)
	if err != nil {
		return nil, fmt.Errorf("kitchen queue: %w", err)
	}
	defer rows.Close()
	var out []KitchenOrder
	for rows.Next() {
		var k KitchenOrder
		var deadline, instruction *string
		var secs *int
		var deadlineAt *time.Time
		if err := rows.Scan(&k.ID, &k.OrderNumber, &k.Status, &k.FinalAmount, &k.ItemCount,
			&instruction, &k.PlacedAt, &deadline, &secs, &deadlineAt); err != nil {
			return nil, err
		}
		k.CustomerInstruction = instruction
		k.AcceptDeadlineAt = deadline
		k.SecondsToBreach = secs
		k.FillDerived(deadlineAt)
		out = append(out, k)
	}
	return out, rows.Err()
}

// AutoRejectExpiredOrders finds CONFIRMED orders whose accept_deadline
// passed and transitions them to RESTAURANT_REJECTED with reason
// 'sla_breach'. Returns the affected order ids so the worker can
// publish events for each.
func (s *Store) AutoRejectExpiredOrders(ctx context.Context, batch int) ([]uuid.UUID, error) {
	if batch <= 0 {
		batch = 50
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// SKIP LOCKED keeps two workers from fighting over the same rows; each
	// locked id then goes through the guarded writer in this transaction.
	rows, err := tx.Query(ctx, `
		SELECT id FROM food.orders
		WHERE status = 'CONFIRMED'
		  AND accept_deadline_at IS NOT NULL
		  AND accept_deadline_at <= NOW()
		ORDER BY accept_deadline_at ASC
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`, batch)
	if err != nil {
		return nil, fmt.Errorf("auto-reject expired: %w", err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if err := transitionOrderTx(ctx, tx, OrderTransition{
			OrderID: id, From: orderstate.Confirmed, To: orderstate.RestaurantRejected,
			Actor: orderstate.ActorSystem, Reason: "sla_breach",
			CancelReason: "sla_breach: restaurant did not accept in time",
		}); err != nil {
			return nil, err
		}
		// Wave 1 B3: a paid order is never left rejected with nobody asking
		// for the money back; its refund is requested in this transaction.
		if _, err := requestSystemRefundTx(ctx, tx, id, "sla_breach"); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return ids, nil
}
