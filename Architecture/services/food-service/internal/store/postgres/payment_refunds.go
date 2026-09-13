package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/atpost/food-service/internal/orderstate"
	"github.com/atpost/food-service/internal/payments"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrRefundNotEligible: the order has no captured payment (no payment row at
// all, never captured, cash on delivery, or already fully refunded).
// HTTP 409 FOOD_REFUND_NOT_ELIGIBLE.
var ErrRefundNotEligible = errors.New("order has no captured payment to refund")

// RefundPlan is a durable refund request the service then submits.
type RefundPlan struct {
	RefundID      uuid.UUID
	OrderID       uuid.UUID
	PaymentMethod string
	// IntentID is the payments-service intent (food.payments.provider_payment_id).
	IntentID    string
	AmountMinor int64
	// Status is the food.refunds row status: PENDING, SUBMITTED or PROCESSED.
	Status      string
	OrderStatus string
}

// Body is the admin API response for the plan.
func (p *RefundPlan) Body() map[string]any {
	return map[string]any{
		"id":             p.RefundID,
		"order_id":       p.OrderID,
		"amount":         float64(p.AmountMinor) / 100,
		"amount_minor":   p.AmountMinor,
		"status":         p.Status,
		"order_status":   p.OrderStatus,
		"payment_method": p.PaymentMethod,
	}
}

// AdminRequestRefund records an admin refund REQUEST. It never marks anything
// refunded: a full refund moves the order to REFUND_PENDING through the guard
// (admin actor) and the payment.refunded event finalises REFUNDED. A partial
// refund leaves the order status alone.
//
// Retrying with the same idempotency key, or asking again while the order is
// REFUND_PENDING, returns the SAME open refund so the service resubmits it
// under the same deterministic payments key instead of opening a second one.
func (s *Store) AdminRequestRefund(ctx context.Context, adminID, orderID uuid.UUID, reason string, amount float64, idempotencyKey string) (*RefundPlan, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "admin refund"
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	body, handled, err := s.lockGenericIdempotency(ctx, tx, adminID, idempotencyKey)
	if err != nil {
		return nil, err
	}
	if handled {
		refundID, perr := uuid.Parse(fmt.Sprint(body["id"]))
		if perr != nil {
			return nil, fmt.Errorf("%w: idempotency key was used for another request", ErrIdempotencyInProgress)
		}
		plan, err := loadRefundPlanTx(ctx, tx, `r.id = $1`, refundID)
		if err != nil {
			return nil, err
		}
		return plan, tx.Commit(ctx)
	}

	var status, paymentStatus, method, intentID string
	var paymentID *uuid.UUID
	var orderMinor, refundedMinor int64
	// The payment is a LATERAL left join scanned into a pointer: an order
	// with no payment row is "not eligible", not a NULL-scan error.
	if err := tx.QueryRow(ctx, `
		SELECT o.status::text, o.payment_status::text, o.payment_method::text,
			COALESCE(o.final_amount_paise, ROUND(o.final_amount * 100)::bigint),
			p.id, COALESCE(p.provider_payment_id, ''),
			COALESCE((
				SELECT SUM((amount * 100)::bigint) FROM food.refunds
				WHERE order_id = o.id AND status IN ('PENDING', 'SUBMITTED', 'PROCESSED')
			), 0)::bigint
		FROM food.orders o
		LEFT JOIN LATERAL (
			SELECT id, provider_payment_id FROM food.payments
			WHERE order_id = o.id ORDER BY created_at DESC LIMIT 1
		) p ON TRUE
		WHERE o.id = $1
		FOR UPDATE OF o
	`, orderID).Scan(&status, &paymentStatus, &method, &orderMinor, &paymentID, &intentID, &refundedMinor); err != nil {
		return nil, err
	}
	if paymentID == nil {
		return nil, fmt.Errorf("%w: no payment recorded", ErrRefundNotEligible)
	}
	switch paymentStatus {
	case "CAPTURED", "PARTIALLY_REFUNDED", "REFUND_PENDING":
	default:
		return nil, fmt.Errorf("%w: payment is %s", ErrRefundNotEligible, paymentStatus)
	}
	if method == payments.StoreOnline && intentID == "" {
		return nil, fmt.Errorf("%w: no payments intent recorded", ErrRefundNotEligible)
	}

	if status == orderstate.RefundPending {
		plan, err := loadRefundPlanTx(ctx, tx, `r.order_id = $1 AND r.status IN ('PENDING', 'SUBMITTED')`, orderID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: refund already in flight with no open request", ErrRefundNotEligible)
		}
		if err != nil {
			return nil, err
		}
		if err := s.completeGenericIdempotency(ctx, tx, adminID, idempotencyKey, 202, plan.Body()); err != nil {
			return nil, err
		}
		return plan, tx.Commit(ctx)
	}

	// Eligibility is the transition table: cancelled, rejected or delivered.
	// An order still being fulfilled is cancelled first.
	if err := orderstate.Validate(orderstate.ActorAdmin, status, orderstate.RefundPending); err != nil {
		return nil, err
	}
	remaining := orderMinor - refundedMinor
	if remaining <= 0 {
		return nil, fmt.Errorf("%w: nothing left to refund", ErrRefundNotEligible)
	}
	amountMinor := int64(math.Round(amount * 100))
	if amountMinor <= 0 || amountMinor > remaining {
		amountMinor = remaining
	}

	orderStatus := status
	if amountMinor == remaining {
		if err := transitionOrderTx(ctx, tx, OrderTransition{
			OrderID: orderID, From: status, To: orderstate.RefundPending,
			Actor: orderstate.ActorAdmin, ChangedBy: &adminID, Reason: reason,
		}); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE food.orders SET payment_status = 'REFUND_PENDING' WHERE id = $1`, orderID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE food.payments SET status = 'REFUND_PENDING' WHERE id = $1`, *paymentID); err != nil {
			return nil, err
		}
		orderStatus = orderstate.RefundPending
	} else if err := enqueueOrderEventTx(ctx, tx, orderID, eventRefundRequested, ""); err != nil {
		// A partial refund changes no status (the REFUND_PENDING transition
		// above announces a full one) but is still announced.
		return nil, err
	}

	var refundID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO food.refunds (order_id, payment_id, amount, reason, requested_by, status)
		VALUES ($1, $2, ($3::bigint)::numeric / 100, $4, $5, 'PENDING')
		RETURNING id
	`, orderID, *paymentID, amountMinor, reason, adminID).Scan(&refundID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.admin_audit_logs (actor_user_id, action, entity_type, entity_id, new_value)
		VALUES ($1, 'order.refund_requested', 'order', $2,
			jsonb_build_object('amount_minor', $3::bigint, 'reason', $4::text, 'refund_id', $5::text))
	`, adminID, orderID, amountMinor, reason, refundID.String()); err != nil {
		return nil, err
	}
	plan := &RefundPlan{
		RefundID: refundID, OrderID: orderID, PaymentMethod: method, IntentID: intentID,
		AmountMinor: amountMinor, Status: "PENDING", OrderStatus: orderStatus,
	}
	if err := s.completeGenericIdempotency(ctx, tx, adminID, idempotencyKey, 202, plan.Body()); err != nil {
		return nil, err
	}
	return plan, tx.Commit(ctx)
}

func loadRefundPlanTx(ctx context.Context, q rowQuerier, where string, arg any) (*RefundPlan, error) {
	var p RefundPlan
	err := q.QueryRow(ctx, `
		SELECT r.id, r.order_id, (r.amount * 100)::bigint, r.status,
			o.payment_method::text, o.status::text, COALESCE(p.provider_payment_id, '')
		FROM food.refunds r
		JOIN food.orders o ON o.id = r.order_id
		LEFT JOIN food.payments p ON p.id = r.payment_id
		WHERE `+where+`
		ORDER BY r.created_at DESC
		LIMIT 1
	`, arg).Scan(&p.RefundID, &p.OrderID, &p.AmountMinor, &p.Status, &p.PaymentMethod, &p.OrderStatus, &p.IntentID)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// MarkRefundSubmitted records that payments accepted the refund command.
func (s *Store) MarkRefundSubmitted(ctx context.Context, refundID uuid.UUID, raw map[string]any) error {
	rawJSON, _ := json.Marshal(raw)
	tag, err := s.db.Exec(ctx, `
		UPDATE food.refunds
		SET status = CASE WHEN status = 'PENDING' THEN 'SUBMITTED' ELSE status END,
			raw_response = raw_response || $2::jsonb
		WHERE id = $1
	`, refundID, rawJSON)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// FinalizeWalletRefund settles a wallet refund after the monetization reversal
// succeeded (wallet refunds have no payments-service event).
func (s *Store) FinalizeWalletRefund(ctx context.Context, refundID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var orderID uuid.UUID
	var refundMinor, orderMinor, processedMinor int64
	var refundStatus, orderStatus, method string
	var paymentID *uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT r.order_id, (r.amount * 100)::bigint, r.status, o.status::text, o.payment_method::text,
			COALESCE(o.final_amount_paise, ROUND(o.final_amount * 100)::bigint), r.payment_id,
			COALESCE((
				SELECT SUM((amount * 100)::bigint) FROM food.refunds
				WHERE order_id = o.id AND status = 'PROCESSED'
			), 0)::bigint
		FROM food.refunds r
		JOIN food.orders o ON o.id = r.order_id
		WHERE r.id = $1
		FOR UPDATE OF o, r
	`, refundID).Scan(&orderID, &refundMinor, &refundStatus, &orderStatus, &method, &orderMinor, &paymentID, &processedMinor); err != nil {
		return err
	}
	if method != payments.StoreWallet {
		return fmt.Errorf("%w: not a wallet order", ErrRefundNotEligible)
	}
	if refundStatus == "PROCESSED" {
		return tx.Commit(ctx)
	}
	if err := settleRefundTx(ctx, tx, refundSettlement{
		OrderID: orderID, OrderStatus: orderStatus, PaymentID: paymentID,
		Full: processedMinor+refundMinor >= orderMinor, AmountMinor: refundMinor, RefundID: &refundID,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
