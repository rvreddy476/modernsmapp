package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/food-service/internal/orderstate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Refunds the system requests (Wave 1 B3). A paid order rejected by the SLA
// worker or by the restaurant must never be left in RESTAURANT_REJECTED with
// nobody asking for the customer's money back. In the SAME transaction as the
// rejection this records a durable refund request and moves the order to
// REFUND_PENDING through the guard (system actor). The service then submits
// it to payments; only the payment.refunded event finalises REFUNDED.
//
// A system request is a food.refunds row with requested_by NULL (admin
// requests carry the admin's id). ListUnsubmittedSystemRefunds lets the SLA
// worker resubmit one whose first submission failed.

// requestSystemRefundTx runs right after the caller moved orderID to
// RESTAURANT_REJECTED in tx. It returns nil when nothing was paid (cash on
// delivery, unpaid, already fully refunded or requested).
func requestSystemRefundTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, reason string) (*RefundPlan, error) {
	var status, paymentStatus, method, intentID string
	var paymentID *uuid.UUID
	var orderMinor, requestedMinor int64
	if err := tx.QueryRow(ctx, `
		SELECT o.status::text, o.payment_status::text, o.payment_method::text,
			COALESCE(o.final_amount_paise, ROUND(o.final_amount * 100)::bigint),
			p.id, COALESCE(p.provider_payment_id, ''),
			COALESCE((
				SELECT SUM(ROUND(amount * 100)::bigint) FROM food.refunds
				WHERE order_id = o.id AND status IN ('PENDING', 'SUBMITTED', 'PROCESSED')
			), 0)::bigint
		FROM food.orders o
		LEFT JOIN LATERAL (
			SELECT id, provider_payment_id FROM food.payments
			WHERE order_id = o.id ORDER BY created_at DESC LIMIT 1
		) p ON TRUE
		WHERE o.id = $1
		FOR UPDATE OF o
	`, orderID).Scan(&status, &paymentStatus, &method, &orderMinor, &paymentID, &intentID, &requestedMinor); err != nil {
		return nil, err
	}
	if status != orderstate.RestaurantRejected {
		return nil, fmt.Errorf("%w: a system refund needs a rejected order, not %s", ErrOrderStatusConflict, status)
	}
	switch paymentStatus {
	case "CAPTURED", "PARTIALLY_REFUNDED":
	default:
		return nil, nil
	}
	remaining := orderMinor - requestedMinor
	if remaining <= 0 {
		return nil, nil
	}
	if err := transitionOrderTx(ctx, tx, OrderTransition{
		OrderID: orderID, From: orderstate.RestaurantRejected, To: orderstate.RefundPending,
		Actor: orderstate.ActorSystem, Reason: "refund requested: " + reason,
	}); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE food.orders SET payment_status = 'REFUND_PENDING' WHERE id = $1`, orderID); err != nil {
		return nil, err
	}
	if paymentID != nil {
		if _, err := tx.Exec(ctx, `UPDATE food.payments SET status = 'REFUND_PENDING' WHERE id = $1`, *paymentID); err != nil {
			return nil, err
		}
	}
	var refundID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO food.refunds (order_id, payment_id, amount, reason, requested_by, status)
		VALUES ($1, $2, ($3::bigint)::numeric / 100, $4, NULL, 'PENDING')
		RETURNING id
	`, orderID, paymentID, remaining, "system: "+reason).Scan(&refundID); err != nil {
		return nil, err
	}
	return &RefundPlan{
		RefundID: refundID, OrderID: orderID, PaymentMethod: method, IntentID: intentID,
		AmountMinor: remaining, Status: "PENDING", OrderStatus: orderstate.RefundPending,
	}, nil
}

// OpenRefundPlan returns the order's latest refund that is not yet processed
// (PENDING or SUBMITTED), or pgx.ErrNoRows.
func (s *Store) OpenRefundPlan(ctx context.Context, orderID uuid.UUID) (*RefundPlan, error) {
	return loadRefundPlanTx(ctx, s.db, `r.order_id = $1 AND r.status IN ('PENDING', 'SUBMITTED')`, orderID)
}

// ListUnsubmittedSystemRefunds returns system refund requests still PENDING
// (never accepted by payments) that are at least olderThan old.
func (s *Store) ListUnsubmittedSystemRefunds(ctx context.Context, olderThan time.Duration, limit int) ([]RefundPlan, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
		SELECT r.id, r.order_id, ROUND(r.amount * 100)::bigint, r.status,
			o.payment_method::text, o.status::text, COALESCE(p.provider_payment_id, '')
		FROM food.refunds r
		JOIN food.orders o ON o.id = r.order_id
		LEFT JOIN food.payments p ON p.id = r.payment_id
		WHERE r.status = 'PENDING' AND r.requested_by IS NULL
			AND r.created_at <= NOW() - ($1::float8 * INTERVAL '1 second')
		ORDER BY r.created_at
		LIMIT $2
	`, olderThan.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RefundPlan
	for rows.Next() {
		var p RefundPlan
		if err := rows.Scan(&p.RefundID, &p.OrderID, &p.AmountMinor, &p.Status, &p.PaymentMethod, &p.OrderStatus, &p.IntentID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
