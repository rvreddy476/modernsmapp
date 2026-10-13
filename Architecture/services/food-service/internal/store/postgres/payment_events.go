package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/food-service/internal/orderstate"
	"github.com/atpost/food-service/internal/payments"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ApplyPaymentEvent applies one payments-service event in ONE transaction:
//
//  1. INSERT the food.payment_event_inbox row; a conflict on event_id means
//     the event was already applied -> OutcomeDuplicate, nothing else runs;
//  2. read the order (and its latest payment) FOR UPDATE;
//  3. payments.Decide;
//  4. apply the effect (every order status change through transitionOrderTx);
//  5. record the outcome on the inbox row; COMMIT.
//
// Steps 1 and 2-5 are paymentevents.ApplyOnce over paymentInbox, inside this
// transaction. A mismatch commits the inbox row with no order effect, so it is
// recorded once and not retried. Any error rolls back the inbox row with the
// rest, so a retry is applied exactly once.
func (s *Store) ApplyPaymentEvent(ctx context.Context, ev payments.Event) (payments.Applied, error) {
	applied := payments.Applied{OrderID: ev.OrderID}
	if strings.TrimSpace(ev.EventID) == "" {
		return applied, fmt.Errorf("payment event has no event_id")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return applied, err
	}
	defer tx.Rollback(ctx)

	claim := paymentevents.Claim{
		EventID: ev.EventID, EventType: ev.EventType, IntentID: ev.IntentID,
		ReferenceID: ev.OrderID, AmountMinor: ev.AmountMinor, Currency: ev.Currency,
	}
	err = paymentevents.ApplyOnce(ctx, tx, paymentInbox{}, claim, func(ctx context.Context, tx pgx.Tx) error {
		return s.applyPaymentEffectTx(ctx, tx, ev, &applied)
	})
	switch {
	case errors.Is(err, paymentevents.ErrDuplicate):
		applied.Decision = payments.Decision{Outcome: payments.OutcomeDuplicate}
		return applied, nil
	case err != nil:
		return applied, err
	}
	return applied, tx.Commit(ctx)
}

// paymentInbox is food.payment_event_inbox, the dedupe half of
// paymentevents.ApplyOnce.
type paymentInbox struct{}

func (paymentInbox) Claim(ctx context.Context, tx pgx.Tx, c paymentevents.Claim) (bool, error) {
	tag, err := tx.Exec(ctx, `
		INSERT INTO food.payment_event_inbox (event_id, event_type, intent_id, order_id, amount_minor, currency)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, NULLIF($6, ''))
		ON CONFLICT (event_id) DO NOTHING
	`, c.EventID, c.EventType, c.IntentID, c.ReferenceID, c.AmountMinor, c.Currency)
	if err != nil {
		return false, fmt.Errorf("record payment event: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// applyPaymentEffectTx is steps 2-5 for a freshly claimed event. It fills
// applied and never commits; ApplyPaymentEvent does.
func (s *Store) applyPaymentEffectTx(ctx context.Context, tx pgx.Tx, ev payments.Event, applied *payments.Applied) error {
	snap := payments.OrderSnapshot{OrderID: ev.OrderID}
	var paymentID *uuid.UUID
	var deliveryFee float64
	err := tx.QueryRow(ctx, `
		SELECT o.user_id, o.restaurant_id, o.status::text, o.payment_status::text,
			COALESCE(o.final_amount_paise, ROUND(o.final_amount * 100)::bigint), o.delivery_fee::float8,
			p.id, COALESCE(p.provider_payment_id, ''), COALESCE(btrim(p.currency::text), 'INR')
		FROM food.orders o
		LEFT JOIN LATERAL (
			SELECT id, provider_payment_id, currency
			FROM food.payments
			WHERE order_id = o.id
			ORDER BY created_at DESC
			LIMIT 1
		) p ON TRUE
		WHERE o.id = $1
		FOR UPDATE OF o
	`, ev.OrderID).Scan(&snap.UserID, &applied.RestaurantID, &snap.Status, &snap.PaymentStatus,
		&snap.AmountMinor, &deliveryFee, &paymentID, &snap.IntentID, &snap.Currency)
	if errors.Is(err, pgx.ErrNoRows) {
		// Recorded and committed, so it is not retried.
		applied.Decision = payments.Decision{Outcome: payments.OutcomeOrderNotFound, Detail: "no such food order"}
		return recordInboxOutcomeTx(ctx, tx, ev.EventID, applied.Decision)
	}
	if err != nil {
		return fmt.Errorf("read order for payment event: %w", err)
	}
	applied.UserID = snap.UserID
	if ev.EventType == "payment.refund_failed" {
		if ev.IntentID != snap.IntentID || ev.CommandID == "" || ev.AmountMinor <= 0 || ev.AmountMinor > snap.AmountMinor {
			applied.Decision = payments.Decision{Outcome: payments.OutcomeAmountMismatch, Detail: "refund failure does not match order payment"}
		} else {
			_, err = tx.Exec(ctx, `UPDATE food.refunds SET status='SUBMITTED',raw_response=raw_response || jsonb_build_object('last_submission_error','provider_failure_requires_finance_review','payments_command_id',$2::text)
			 WHERE order_id=$1 AND status IN ('PENDING','SUBMITTED') AND ROUND(amount*100)::bigint=$3
			 AND (raw_response->>'payments_command_id'=$2 OR raw_response->>'payments_command_id' IS NULL)`, ev.OrderID, ev.CommandID, ev.AmountMinor)
			if err != nil {
				return err
			}
			applied.Decision = payments.Decision{Outcome: payments.OutcomeRefundIgnored, Detail: "provider failure recorded for finance review"}
		}
		return recordInboxOutcomeTx(ctx, tx, ev.EventID, applied.Decision)
	}
	if ev.ProviderRefundID != "" {
		var duplicate bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM food.refunds WHERE order_id=$1 AND provider_refund_id=$2 AND status='PROCESSED')`, ev.OrderID, ev.ProviderRefundID).Scan(&duplicate); err != nil {
			return err
		}
		if duplicate {
			applied.Decision = payments.Decision{Outcome: payments.OutcomeDuplicate}
			return recordInboxOutcomeTx(ctx, tx, ev.EventID, applied.Decision)
		}
	}

	d := payments.Decide(snap, ev)
	applied.Decision = d
	switch d.Effect {
	case payments.EffectConfirm:
		err = s.confirmOrderPaidTx(ctx, tx, paidConfirmation{
			OrderID: snap.OrderID, From: snap.Status, PaymentID: paymentID, IntentID: ev.IntentID,
			PaymentStatus: "CAPTURED", DeliveryFee: deliveryFee, Reason: "payment captured",
		})
	case payments.EffectMarkFailed:
		err = markPaymentFailedTx(ctx, tx, snap, paymentID, ev.EventID)
	case payments.EffectRefund:
		err = settleRefundTx(ctx, tx, refundSettlement{
			OrderID: snap.OrderID, OrderStatus: snap.Status, PaymentID: paymentID, Full: true, AmountMinor: ev.AmountMinor, CommandID: ev.CommandID, ProviderRefundID: ev.ProviderRefundID,
		})
	case payments.EffectPartialRefund:
		err = settleRefundTx(ctx, tx, refundSettlement{
			OrderID: snap.OrderID, OrderStatus: snap.Status, PaymentID: paymentID, AmountMinor: ev.AmountMinor, CommandID: ev.CommandID, ProviderRefundID: ev.ProviderRefundID,
		})
	}
	if err != nil {
		return fmt.Errorf("apply %s (%s): %w", ev.EventType, d.Outcome, err)
	}
	return recordInboxOutcomeTx(ctx, tx, ev.EventID, d)
}

func recordInboxOutcomeTx(ctx context.Context, tx pgx.Tx, eventID string, d payments.Decision) error {
	_, err := tx.Exec(ctx, `
		UPDATE food.payment_event_inbox SET outcome = $2, detail = NULLIF($3, '') WHERE event_id = $1
	`, eventID, string(d.Outcome), d.Detail)
	if err != nil {
		return fmt.Errorf("record payment event outcome: %w", err)
	}
	return nil
}

func markPaymentFailedTx(ctx context.Context, tx pgx.Tx, snap payments.OrderSnapshot, paymentID *uuid.UUID, eventID string) error {
	if paymentID != nil {
		if _, err := tx.Exec(ctx, `
			UPDATE food.payments SET status = 'FAILED', failed_reason = $2 WHERE id = $1
		`, *paymentID, "payment.failed event "+eventID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE food.orders SET payment_status = 'FAILED' WHERE id = $1`, snap.OrderID); err != nil {
		return err
	}
	return transitionOrderTx(ctx, tx, OrderTransition{
		OrderID: snap.OrderID, From: snap.Status, To: orderstate.PaymentFailed,
		Actor: orderstate.ActorPayment, Reason: "payment failed",
	})
}

type refundSettlement struct {
	CommandID        string
	ProviderRefundID string
	OrderID          uuid.UUID
	OrderStatus      string
	PaymentID        *uuid.UUID
	// Full: the payment is now fully refunded. Otherwise partial.
	Full        bool
	AmountMinor int64
	// RefundID settles exactly this food.refunds row (wallet path); nil
	// settles the open row(s) the event corresponds to.
	RefundID  *uuid.UUID
	ChangedBy *uuid.UUID
}

// settleRefundTx records a settled refund. Only a full refund of a
// REFUND_PENDING order changes order status, through the guard.
func settleRefundTx(ctx context.Context, tx pgx.Tx, r refundSettlement) error {
	paymentStatus := "PARTIALLY_REFUNDED"
	if r.Full {
		paymentStatus = "REFUNDED"
	}
	if r.PaymentID != nil {
		if _, err := tx.Exec(ctx, `
			UPDATE food.payments SET status = $2::text::food.payment_status WHERE id = $1
		`, *r.PaymentID, paymentStatus); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.orders SET payment_status = $2::text::food.payment_status WHERE id = $1
	`, r.OrderID, paymentStatus); err != nil {
		return err
	}
	if r.Full && r.OrderStatus == orderstate.RefundPending {
		// The transition announces food.order.refunded.
		if err := transitionOrderTx(ctx, tx, OrderTransition{
			OrderID: r.OrderID, From: orderstate.RefundPending, To: orderstate.Refunded,
			Actor: orderstate.ActorPayment, ChangedBy: r.ChangedBy, Reason: "refund settled",
		}); err != nil {
			return err
		}
	} else if err := enqueueOrderEventTx(ctx, tx, r.OrderID, eventRefunded, ""); err != nil {
		// A partial refund (or a full one on an order not waiting for it)
		// changes no status but is still announced, in this transaction.
		return err
	}
	var err error
	switch {
	case r.RefundID != nil:
		_, err = tx.Exec(ctx, `
			UPDATE food.refunds SET status = 'PROCESSED', processed_at = NOW() WHERE id = $1
		`, *r.RefundID)
	default:
		_, err = tx.Exec(ctx, `
			UPDATE food.refunds SET status = 'PROCESSED', processed_at = NOW(), provider_refund_id=NULLIF($3,''),
			 raw_response=(raw_response - 'last_submission_error') || CASE WHEN $4::text<>'' THEN jsonb_build_object('payments_command_id',$4::text) ELSE '{}'::jsonb END
			WHERE id = (
				SELECT id FROM food.refunds
				WHERE order_id = $1 AND status IN ('PENDING', 'SUBMITTED') AND (amount * 100)::bigint = $2
				AND ($4::text='' OR raw_response->>'payments_command_id'=$4 OR raw_response->>'payments_command_id' IS NULL)
				ORDER BY (raw_response->>'payments_command_id'=$4) DESC NULLS LAST, created_at
				LIMIT 1
			)
		`, r.OrderID, r.AmountMinor, r.ProviderRefundID, r.CommandID)
	}
	if err != nil {
		return err
	}
	return reconcileSupportRefundsTx(ctx, tx, r.OrderID)
}
