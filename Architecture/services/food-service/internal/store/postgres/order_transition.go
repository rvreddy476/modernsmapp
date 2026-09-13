package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/food-service/internal/foodevents"
	"github.com/atpost/food-service/internal/orderstate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrOrderTransitionNotAllowed: the edge does not exist or the actor may
	// not take it. HTTP 409 FOOD_ORDER_TRANSITION_NOT_ALLOWED.
	ErrOrderTransitionNotAllowed = orderstate.ErrTransitionNotAllowed
	// ErrOrderStatusConflict: the order is no longer in the status the writer
	// read (a concurrent writer won). HTTP 409 FOOD_ORDER_STATUS_CONFLICT.
	ErrOrderStatusConflict = errors.New("order status changed concurrently")
	// ErrDeliveryOTPRequired: pickup and delivery complete only through the
	// OTP verifies. HTTP 409 FOOD_DELIVERY_OTP_REQUIRED.
	ErrDeliveryOTPRequired = errors.New("pickup and delivery are completed only by OTP verification")
	// ErrDeliveryPartnerNotActive: the caller is a delivery partner, but not an
	// ACTIVE one. HTTP 403 FOOD_DELIVERY_PARTNER_NOT_ACTIVE.
	ErrDeliveryPartnerNotActive = errors.New("delivery partner is not active")
	// ErrDeliveryCodeInvalid: missing or wrong pickup/delivery OTP.
	ErrDeliveryCodeInvalid = errors.New("delivery code is invalid")
	// ErrAssignmentNotReady: the assignment is not at the step the verify needs
	// (unclaimed, not yet picked up, already delivered).
	ErrAssignmentNotReady = errors.New("delivery assignment is not at this step")
)

// OrderTransition is one guarded status change.
type OrderTransition struct {
	OrderID   uuid.UUID
	From      string
	To        string
	Actor     orderstate.Actor
	ChangedBy *uuid.UUID
	// Reason is written to order_status_history.reason.
	Reason string
	// CancelReason is written to orders.cancellation_reason when To is a
	// cancellation; it defaults to Reason.
	CancelReason string
	// Event overrides the Kafka event this edge announces
	// (foodevents.ForTransition). A captured payment announces
	// food.order.payment_succeeded rather than food.order.confirmed.
	Event string
}

// transitionOrderTx is the ONLY non-payment writer of food.orders.status
// (TestNoRawOrderStatusUpdates enforces that). In the caller's transaction it:
//
//  1. validates the edge and the actor (orderstate.Validate);
//  2. updates the row only if it is still in tr.From, so a concurrent writer
//     cannot be overwritten;
//  3. treats zero affected rows as ErrOrderStatusConflict, which the caller
//     returns, rolling back everything else it did in the transaction;
//  4. records history with the real from_status in the same transaction;
//  5. when the order ends before delivery, closes its delivery assignment and
//     pending offers (closeOpenAssignmentsTx);
//  6. writes the lifecycle event to the outbox in the same transaction
//     (enqueueOrderEventTx), so notification-service hears of exactly the
//     changes that committed.
func transitionOrderTx(ctx context.Context, tx pgx.Tx, tr OrderTransition) error {
	if err := orderstate.Validate(tr.Actor, tr.From, tr.To); err != nil {
		return err
	}
	cancel := orderstate.IsCancellation(tr.To)
	cancelReason := tr.CancelReason
	if cancelReason == "" {
		cancelReason = tr.Reason
	}
	tag, err := tx.Exec(ctx, `
		UPDATE food.orders
		SET status = $3::text::food.order_status,
			delivered_at = CASE WHEN $3::text = 'DELIVERED' THEN NOW() ELSE delivered_at END,
			cancellation_reason = CASE WHEN $4::boolean THEN $5::text ELSE cancellation_reason END,
			cancelled_by = CASE WHEN $4::boolean THEN $6::uuid ELSE cancelled_by END,
			cancelled_at = CASE WHEN $4::boolean THEN NOW() ELSE cancelled_at END
		WHERE id = $1 AND status = $2::text::food.order_status
	`, tr.OrderID, tr.From, tr.To, cancel, cancelReason, tr.ChangedBy)
	if err != nil {
		return fmt.Errorf("transition order %s -> %s: %w", tr.From, tr.To, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: order is no longer %s", ErrOrderStatusConflict, tr.From)
	}
	// clock_timestamp(), not the NOW() default: chained transitions in one
	// transaction would otherwise share a timestamp and sort arbitrarily.
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.order_status_history (order_id, from_status, to_status, changed_by, reason, created_at)
		VALUES ($1, $2::text::food.order_status, $3::text::food.order_status, $4, $5, clock_timestamp())
	`, tr.OrderID, tr.From, tr.To, tr.ChangedBy, tr.Reason); err != nil {
		return fmt.Errorf("record order history: %w", err)
	}
	if cancel {
		if err := closeOpenAssignmentsTx(ctx, tx, tr.OrderID); err != nil {
			return err
		}
	}
	eventType := tr.Event
	if eventType == "" {
		eventType = foodevents.ForTransition(tr.From, tr.To)
	}
	if eventType != "" {
		if err := enqueueOrderEventTx(ctx, tx, tr.OrderID, eventType, tr.From); err != nil {
			return err
		}
	}
	return nil
}

func uuidPtr(id uuid.UUID) *uuid.UUID { return &id }
