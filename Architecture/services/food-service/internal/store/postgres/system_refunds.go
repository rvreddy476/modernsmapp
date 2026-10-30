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
// requests carry the admin's id). The existing retry worker now retries all
// unsubmitted requests, including admin and approved support refunds.

// requestSystemRefundTx runs right after the caller moved orderID to
// RESTAURANT_REJECTED in tx. It returns nil when nothing was paid (cash on
// delivery, unpaid, already fully refunded or requested).
func requestSystemRefundTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, reason string) (*RefundPlan, error) {
	return requestSystemRefundFromTx(ctx, tx, orderID, orderstate.RestaurantRejected, reason)
}

// systemRefundSources are the statuses a system refund request may start
// from: a rejection (B3), since B4 a customer's own cancellation, and since
// the 2026-10-30 audit (M1) an admin's cancellation of a paid order.
var systemRefundSources = map[string]bool{
	orderstate.RestaurantRejected:  true,
	orderstate.CancelledByCustomer: true,
	orderstate.CancelledByAdmin:    true,
}

// refundRequest is one durable request for the refund of whatever was
// captured on an order, written in the caller's transaction.
type refundRequest struct {
	OrderID uuid.UUID
	// From is the status the caller just moved the order to (or, for a late
	// capture, the status it already rests in).
	From   string
	Reason string
	// Actor takes the From -> REFUND_PENDING edge. Admin cancels use
	// ActorAdmin (the only actor the state table allows from
	// CANCELLED_BY_ADMIN); everything else is ActorSystem.
	Actor     orderstate.Actor
	ChangedBy *uuid.UUID
	// KeepOrderStatus records the refund without moving the order: a late
	// capture on a closed order (DRAFT, FAILED, any cancellation) has no edge
	// to REFUND_PENDING and must never be revived.
	KeepOrderStatus bool
}

// requestSystemRefundFromTx runs right after the caller moved orderID to
// `from` in tx, and requests the refund of whatever was captured.
func requestSystemRefundFromTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, from, reason string) (*RefundPlan, error) {
	return requestCapturedRefundTx(ctx, tx, refundRequest{
		OrderID: orderID, From: from, Reason: reason, Actor: orderstate.ActorSystem,
	})
}

// requestLateCaptureRefundTx records a capture that landed on an order that
// will not be fulfilled (cancelled, rejected, failed, draft) and, in the same
// transaction, queues the refund of the whole capture through the durable
// refund path (food.refunds PENDING, submitted by the service and resubmitted
// by the SLA worker). The order status is left as it is. Idempotent per
// payment: once recorded the payment is REFUND_PENDING, so a redelivered or
// second capture event decides already_paid, and the remaining-balance cap
// refuses a second row.
func requestLateCaptureRefundTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, orderStatus string, paymentID *uuid.UUID, intentID string) (*RefundPlan, error) {
	if paymentID != nil {
		if _, err := tx.Exec(ctx, `
			UPDATE food.payments
			SET status = 'CAPTURED', paid_at = COALESCE(paid_at, NOW()),
				provider_payment_id = COALESCE(NULLIF(provider_payment_id, ''), NULLIF($2::text, ''))
			WHERE id = $1
		`, *paymentID, intentID); err != nil {
			return nil, err
		}
	} else if _, err := tx.Exec(ctx, `
		INSERT INTO food.payments (order_id, payment_method, status, provider, provider_payment_id, amount, currency, paid_at)
		SELECT id, payment_method, 'CAPTURED', 'payments-service', NULLIF($2::text, ''),
			(COALESCE(final_amount_paise, ROUND(final_amount * 100)::bigint))::numeric / 100, 'INR', NOW()
		FROM food.orders WHERE id = $1
	`, orderID, intentID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE food.orders SET payment_status = 'CAPTURED' WHERE id = $1`, orderID); err != nil {
		return nil, err
	}
	return requestCapturedRefundTx(ctx, tx, refundRequest{
		OrderID: orderID, From: orderStatus, Reason: "late_capture: captured on a " + orderStatus + " order",
		Actor: orderstate.ActorSystem, KeepOrderStatus: true,
	})
}

// requestCapturedRefundTx is the one durable refund writer for system-side
// requests. It locks the order, caps the request at what is still unrefunded,
// moves the payment (and, unless KeepOrderStatus, the order) to
// REFUND_PENDING, and inserts the PENDING food.refunds row. It returns nil
// when nothing captured is left to refund.
func requestCapturedRefundTx(ctx context.Context, tx pgx.Tx, r refundRequest) (*RefundPlan, error) {
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
	`, r.OrderID).Scan(&status, &paymentStatus, &method, &orderMinor, &paymentID, &intentID, &requestedMinor); err != nil {
		return nil, err
	}
	if status != r.From || (!r.KeepOrderStatus && !systemRefundSources[r.From]) {
		return nil, fmt.Errorf("%w: a system refund needs a rejected or cancelled order, not %s", ErrOrderStatusConflict, status)
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
	orderStatus := r.From
	if !r.KeepOrderStatus {
		if err := transitionOrderTx(ctx, tx, OrderTransition{
			OrderID: r.OrderID, From: r.From, To: orderstate.RefundPending,
			Actor: r.Actor, ChangedBy: r.ChangedBy, Reason: "refund requested: " + r.Reason,
		}); err != nil {
			return nil, err
		}
		orderStatus = orderstate.RefundPending
	} else if err := enqueueOrderEventTx(ctx, tx, r.OrderID, eventRefundRequested, ""); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE food.orders SET payment_status = 'REFUND_PENDING' WHERE id = $1`, r.OrderID); err != nil {
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
		VALUES ($1, $2, ($3::bigint)::numeric / 100, $4, $5, 'PENDING')
		RETURNING id
	`, r.OrderID, paymentID, remaining, "system: "+r.Reason, r.ChangedBy).Scan(&refundID); err != nil {
		return nil, err
	}
	return &RefundPlan{
		RefundID: refundID, OrderID: r.OrderID, PaymentMethod: method, IntentID: intentID,
		AmountMinor: remaining, Status: "PENDING", OrderStatus: orderStatus,
	}, nil
}

// OpenRefundPlan returns the order's latest refund that is not yet processed
// (PENDING or SUBMITTED), or pgx.ErrNoRows.
func (s *Store) OpenRefundPlan(ctx context.Context, orderID uuid.UUID) (*RefundPlan, error) {
	return loadRefundPlanTx(ctx, s.db, `r.order_id = $1 AND r.status IN ('PENDING', 'SUBMITTED')`, orderID)
}

// ListUnsubmittedSystemRefunds retains its existing worker interface, returning
// every refund still PENDING (never accepted by payments) at least olderThan old.
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
		WHERE r.status = 'PENDING'
			AND r.created_at <= NOW() - ($1::float8 * INTERVAL '1 second')
			AND (r.raw_response->>'next_submission_at' IS NULL OR (r.raw_response->>'next_submission_at')::timestamptz<=NOW())
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
