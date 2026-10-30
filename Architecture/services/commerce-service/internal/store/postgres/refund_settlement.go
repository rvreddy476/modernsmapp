package postgres

// Refund settlement (audit 2026-10-30 M2).
//
// payment.refunded is published once per settled provider refund and carries
// THIS refund's amount, not the intent's total. The previous SettleRefund took
// only the order id: any refund event naming the order marked it fully
// refunded and closed every open refund command, so a ₹200 dashboard partial
// on a ₹2,000 order closed a pending cancel refund and the ₹1,800 still owed
// was never sent. It also had no inbox, so a redelivery wrote a second
// commerce.order.refunded.
//
// Now, in one transaction:
//
//  1. the event's payment_event_inbox row is claimed (redelivery = no-op);
//  2. the order is locked and the refund checked with
//     paymentevents.CheckRefund against the order total and its bound intent,
//     as every other domain does;
//  3. the refund is recorded in order_refund_settlements, unique on the
//     provider refund id (a second event for the same refund = no-op);
//  4. decideRefund (pure, below) says which command it settles and where
//     payment_status goes, from the CUMULATIVE amount refunded.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RefundEvent is a settled refund as payment.refunded carries it.
type RefundEvent struct {
	// EventID is the envelope id: the inbox key.
	EventID string
	OrderID uuid.UUID
	// IntentID is the refunded intent.
	IntentID string
	// RefundID is payments' provider_refund_id (manual:<command_id> for a
	// manual resolution). Blank falls back to the event id.
	RefundID string
	// AmountMinor is THIS refund's amount.
	AmountMinor money.Paise
	// ProviderStatus is the intent's status after the refund, as payments
	// reports it. Recorded, not trusted for the decision: commerce decides
	// full versus partial from its own cumulative sum.
	ProviderStatus string
}

// RefundSettlement is what SettleRefund did, for logging.
type RefundSettlement struct {
	// Duplicate: this provider refund was already recorded (under another
	// event id). Nothing changed.
	Duplicate bool
	// RefundedTotal is the order's cumulative refunded amount after this one.
	RefundedTotal money.Paise
	// Full: refunds now cover the order total.
	Full bool
	// PaymentStatus is the order's payment_status after the event.
	PaymentStatus string
}

// refundOrder is the locked order row a refund decision reads.
type refundOrder struct {
	PaymentStatus string
	TotalMinor    money.Paise
	IntentID      string // "" when none is bound
}

// openRefundCommand is an unsettled order_refund_commands row.
type openRefundCommand struct {
	ID          uuid.UUID
	IntentID    string // "" when the command did not name one
	AmountMinor money.Paise
}

// refundDecision is decideRefund's verdict.
type refundDecision struct {
	// SettleCommand is the one command this refund settles, if any.
	SettleCommand *uuid.UUID
	// CloseAll: refunds cover the total, so every still-open command is moot
	// (payments would refuse it against a fully refunded intent) and is
	// closed with this event.
	CloseAll bool
	// NewTotal is the cumulative refunded amount including this refund.
	NewTotal money.Paise
	Full     bool
	// PaymentStatus is the target payment_status ("" = leave as is).
	PaymentStatus string
	// OutboxType is the event to enqueue ("" = none).
	OutboxType string
}

// refundRecordableStatuses are the payment statuses in which money was taken,
// so a refund moves the order's payment state. In any other state (pending,
// processing, failed) the refund is still recorded, because money moved, but
// the order's payment state is left alone and an operator is told.
var refundRecordableStatuses = map[string]bool{
	"paid": true, "refund_pending": true, "partially_refunded": true, "refunded": true,
}

// decideRefund is the refund decision table. It has no I/O.
//
// o is the locked order, ev the refund, already the order's cumulative
// refunded amount before ev, and open the order's unsettled commands, oldest
// first. A non-nil error is a money mismatch: the event is not this order's
// refund, or it would refund more than was paid.
func decideRefund(o refundOrder, ev RefundEvent, already money.Paise, open []openRefundCommand) (refundDecision, error) {
	if err := paymentevents.CheckRefund(
		paymentevents.Expected{AmountMinor: o.TotalMinor.Int64(), IntentID: o.IntentID},
		paymentevents.Observed{AmountMinor: ev.AmountMinor.Int64(), IntentID: ev.IntentID},
	); err != nil {
		return refundDecision{}, err
	}
	total := already.Add(ev.AmountMinor)
	if total > o.TotalMinor {
		return refundDecision{}, &paymentevents.MismatchError{
			Field: paymentevents.FieldAmount,
			Detail: fmt.Sprintf("refunds total %d after refund %d, more than order %d",
				total.Int64(), ev.AmountMinor.Int64(), o.TotalMinor.Int64()),
		}
	}
	d := refundDecision{NewTotal: total, Full: total == o.TotalMinor}

	if d.Full {
		d.CloseAll = true
	} else {
		// A partial settles only a command for exactly this amount on this
		// intent. A smaller partial never closes a larger command: that money
		// is still owed and the command must keep going.
		for i := range open {
			c := open[i]
			if c.AmountMinor == ev.AmountMinor && (c.IntentID == "" || c.IntentID == ev.IntentID) {
				id := c.ID
				d.SettleCommand = &id
				break
			}
		}
	}

	if !refundRecordableStatuses[o.PaymentStatus] {
		return d, nil
	}
	stillOwed := false
	if !d.Full {
		for _, c := range open {
			if d.SettleCommand == nil || c.ID != *d.SettleCommand {
				stillOwed = true
				break
			}
		}
	}
	switch {
	case d.Full:
		d.PaymentStatus = "refunded"
		d.OutboxType = "commerce.order.refunded"
	case stillOwed:
		// A refund commerce owes is still in flight: the order stays
		// refund_pending (or moves there from paid) until it settles.
		d.PaymentStatus = "refund_pending"
		d.OutboxType = "commerce.order.partially_refunded"
	default:
		d.PaymentStatus = "partially_refunded"
		d.OutboxType = "commerce.order.partially_refunded"
	}
	if d.PaymentStatus == o.PaymentStatus || !PaymentStatusTransitionAllowed(o.PaymentStatus, d.PaymentStatus) {
		d.PaymentStatus = ""
	}
	return d, nil
}

// SettleRefund applies one payment.refunded to its order, exactly once.
//
// Errors: ErrDuplicatePaymentEvt for a redelivered event id (nothing
// written); ErrAmountMismatch when the refund is not this order's or exceeds
// what was paid (nothing written: the event goes to the DLQ and pages);
// ErrOrderNotFoundP0 for an unknown order.
func (s *Store) SettleRefund(ctx context.Context, ev RefundEvent) (RefundSettlement, error) {
	var out RefundSettlement
	if strings.TrimSpace(ev.EventID) == "" {
		return out, fmt.Errorf("refund event has no id; refusing to process without a dedupe key")
	}
	refundID := strings.TrimSpace(ev.RefundID)
	if refundID == "" {
		refundID = "event:" + ev.EventID
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT set_config('commerce.actor_type','system',true)`); err != nil {
		return out, err
	}

	pe := PaymentEvent{
		EventID: ev.EventID, EventType: paymentevents.TypeRefunded, IntentID: ev.IntentID,
		OrderID: ev.OrderID, AmountMinor: ev.AmountMinor,
	}
	err = applyPaymentOnce(ctx, tx, pe, func(ctx context.Context, tx pgx.Tx) error {
		var (
			o      refundOrder
			intent *string
		)
		err := tx.QueryRow(ctx,
			`SELECT payment_status, COALESCE(final_amount_minor,0), payment_intent_id::text
			   FROM orders WHERE id = $1 FOR UPDATE`, ev.OrderID).
			Scan(&o.PaymentStatus, &o.TotalMinor, &intent)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrOrderNotFoundP0
			}
			return err
		}
		if intent != nil {
			o.IntentID = *intent
		}
		out.PaymentStatus = o.PaymentStatus

		// The same provider refund under another event id: already counted.
		var seen bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM order_refund_settlements WHERE refund_id = $1)`, refundID).Scan(&seen); err != nil {
			return err
		}
		if seen {
			out.Duplicate = true
			return nil
		}

		var already money.Paise
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(SUM(amount_minor),0) FROM order_refund_settlements WHERE order_id = $1`,
			ev.OrderID).Scan(&already); err != nil {
			return err
		}
		open, err := openRefundCommandsTx(ctx, tx, ev.OrderID)
		if err != nil {
			return err
		}

		d, err := decideRefund(o, ev, already, open)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrAmountMismatch, err)
		}
		out.RefundedTotal, out.Full = d.NewTotal, d.Full

		if _, err := tx.Exec(ctx,
			`INSERT INTO order_refund_settlements
			     (order_id, refund_id, event_id, intent_id, amount_minor, provider_status, command_id)
			 VALUES ($1,$2,$3,NULLIF($4,''),$5,NULLIF($6,''),$7)`,
			ev.OrderID, refundID, ev.EventID, ev.IntentID, ev.AmountMinor.Int64(), ev.ProviderStatus, d.SettleCommand); err != nil {
			return err
		}
		switch {
		case d.CloseAll:
			if _, err := tx.Exec(ctx,
				`UPDATE order_refund_commands SET status = 'succeeded', settled_at = NOW(), updated_at = NOW()
				  WHERE order_id = $1 AND status <> 'succeeded'`, ev.OrderID); err != nil {
				return err
			}
		case d.SettleCommand != nil:
			if _, err := tx.Exec(ctx,
				`UPDATE order_refund_commands SET status = 'succeeded', settled_at = NOW(), updated_at = NOW()
				  WHERE id = $1`, *d.SettleCommand); err != nil {
				return err
			}
		}
		if d.PaymentStatus != "" {
			if _, err := tx.Exec(ctx,
				`UPDATE orders SET payment_status = $2, updated_at = NOW() WHERE id = $1`,
				ev.OrderID, d.PaymentStatus); err != nil {
				return err
			}
			out.PaymentStatus = d.PaymentStatus
		}
		if _, moved := refundRecordableStatuses[o.PaymentStatus]; !moved {
			slog.Error("commerce: refund settled on an order commerce never saw paid; recorded, payment state unchanged",
				"order_id", ev.OrderID, "payment_status", o.PaymentStatus, "refund_minor", ev.AmountMinor.Int64())
		}
		if d.OutboxType == "" {
			return nil
		}
		return enqueueOutboxTx(ctx, tx, d.OutboxType, ev.OrderID.String(), map[string]any{
			"order_id":             ev.OrderID,
			"intent_id":            ev.IntentID,
			"refund_id":            refundID,
			"amount_minor":         ev.AmountMinor.Int64(),
			"refunded_total_minor": d.NewTotal.Int64(),
			"order_total_minor":    o.TotalMinor.Int64(),
			"fully_refunded":       d.Full,
		})
	})
	return out, err
}

// openRefundCommandsTx lists the order's unsettled refund commands, oldest
// first, locked so the refund worker cannot submit one this event settles.
func openRefundCommandsTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) ([]openRefundCommand, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, COALESCE(intent_id,''), amount_minor FROM order_refund_commands
		  WHERE order_id = $1 AND status <> 'succeeded'
		  ORDER BY created_at, id
		  FOR UPDATE`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []openRefundCommand
	for rows.Next() {
		var c openRefundCommand
		if err := rows.Scan(&c.ID, &c.IntentID, &c.AmountMinor); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
