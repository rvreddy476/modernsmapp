package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/doorstep-service/internal/payments"
	sharedevents "github.com/atpost/shared/events"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Payment events on doorstep_extras (B1): the difference bill of a change
// of professional. Applied once through doorstep.payment_inbox in the same
// transaction as the effect, like the booking payment:
//
//   - payment.succeeded matching the bill (amount, currency, payer, intent):
//     the bill is paid and, when its change is still pending on a
//     pro_unavailable booking, the change applies (the hold becomes the
//     booking block — or, if it lapsed, a new block when the professional is
//     still free) and the booking is confirmed again. Otherwise (the change
//     was abandoned, the booking ended, the professional was taken) the
//     money is refunded in full. Money that does not match is flagged and
//     never applied.
//   - payment.failed: the payment row is failed (the hold lapses).
//   - payment.refunded / refund_failed: the extras payment's refund rows.
//
// A visit-extras bill (A5) is left unclaimed: no inbox row, no effect.

type extrasInbox struct{}

func (extrasInbox) Claim(ctx context.Context, tx pgx.Tx, c paymentevents.Claim) (bool, error) {
	tag, err := tx.Exec(ctx, `
		INSERT INTO doorstep.payment_inbox (event_id, event_type, reference_type, reference_id, outcome)
		VALUES ($1, $2, 'doorstep_extras', $3, 'claimed')
		ON CONFLICT (event_id) DO NOTHING`, c.EventID, c.EventType, c.ReferenceID)
	if err != nil {
		return false, fmt.Errorf("record payment event: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ApplyExtrasPaymentEvent applies one doorstep_extras event (see above).
func (s *Store) ApplyExtrasPaymentEvent(ctx context.Context, ev payments.Event) (payments.Applied, error) {
	applied := payments.Applied{}
	if strings.TrimSpace(ev.EventID) == "" {
		return applied, paymentevents.ErrNoEventID
	}
	var kind string
	var booking uuid.UUID
	err := s.db.QueryRow(ctx, `SELECT kind, booking_id FROM doorstep.extras_bills WHERE id = $1`, ev.ExtrasBillID).Scan(&kind, &booking)
	if errors.Is(err, pgx.ErrNoRows) {
		applied.Decision = payments.Decision{Outcome: payments.OutcomeBookingNotFound, Detail: "no such extras bill"}
		return applied, nil
	}
	if err != nil {
		return applied, err
	}
	if kind != "pro_change" && kind != "visit_extras" {
		applied.Decision = payments.Decision{Outcome: payments.OutcomeUnclaimed, Detail: "visit extras are the visit lane's"}
		return applied, nil
	}
	applied.BookingID = booking
	ev.BookingID = booking
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return applied, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claim := paymentevents.Claim{EventID: ev.EventID, EventType: ev.EventType, IntentID: ev.IntentID,
		ReferenceID: ev.ExtrasBillID, AmountMinor: ev.AmountMinor, Currency: ev.Currency}
	err = paymentevents.ApplyOnce(ctx, tx, extrasInbox{}, claim, func(ctx context.Context, tx pgx.Tx) error {
		return s.applyExtrasEffectTx(ctx, tx, ev, &applied)
	})
	switch {
	case errors.Is(err, paymentevents.ErrDuplicate):
		applied.Decision = payments.Decision{Outcome: payments.OutcomeDuplicate}
		return applied, nil
	case err != nil:
		return applied, err
	}
	return applied, mapErr(tx.Commit(ctx))
}

func (s *Store) applyExtrasEffectTx(ctx context.Context, tx pgx.Tx, ev payments.Event, applied *payments.Applied) error {
	at := s.clock()
	var bStatus string
	var customer uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT status, customer_user_id FROM doorstep.bookings WHERE id = $1 FOR UPDATE`, ev.BookingID).
		Scan(&bStatus, &customer); err != nil {
		return fmt.Errorf("read booking for extras event: %w", err)
	}
	var l paymentLock
	var intent *string
	if err := tx.QueryRow(ctx, `SELECT id, amount_paise, status, payments_intent_id FROM doorstep.payments
		WHERE extras_bill_id = $1 AND reference_type = 'doorstep_extras' FOR UPDATE`, ev.ExtrasBillID).
		Scan(&l.paymentID, &l.snap.AmountPaise, &l.snap.PaymentStatus, &intent); err != nil {
		return fmt.Errorf("read extras payment: %w", err)
	}
	l.snap.BookingStatus, l.snap.CustomerID = bStatus, customer
	if intent != nil {
		l.snap.IntentID = *intent
	}
	var d payments.Decision
	var err error
	switch ev.EventType {
	case sharedevents.EventPaymentSucceeded:
		var kind string
		if err = tx.QueryRow(ctx, `SELECT kind FROM doorstep.extras_bills WHERE id=$1`, ev.ExtrasBillID).Scan(&kind); err != nil {
			return err
		}
		if kind == "visit_extras" {
			d, err = s.visitExtrasSucceededTx(ctx, tx, ev, l, at, applied)
		} else {
			d, err = s.extrasSucceededTx(ctx, tx, ev, l, at, applied)
		}
	default:
		// failed / refunded / refund_failed: the booking table's own rules,
		// on this payment row.
		d = payments.Decide(l.snap, ev)
		switch d.Effect {
		case payments.EffectMarkFailed:
			_, err = tx.Exec(ctx, `UPDATE doorstep.payments SET status = 'failed', updated_at = $2 WHERE id = $1 AND status IN ('created', 'pending')`,
				l.paymentID, at)
		case payments.EffectRefunded:
			err = s.settleRefundTx(ctx, tx, ev, l, at)
		case payments.EffectRefundFailed:
			err = s.refundFailedTx(ctx, tx, ev, l, at)
		case payments.EffectAttention:
			err = s.flagAttentionTx(ctx, tx, ev, d.Detail, at)
		}
	}
	if err != nil {
		return fmt.Errorf("apply extras %s (%s): %w", ev.EventType, d.Outcome, err)
	}
	applied.Decision = d
	return recordInboxTx(ctx, tx, ev.EventID, d)
}

func (s *Store) extrasSucceededTx(ctx context.Context, tx pgx.Tx, ev payments.Event, l paymentLock, at time.Time,
	applied *payments.Applied) (payments.Decision, error) {
	if err := paymentevents.CheckCapture(
		paymentevents.Expected{AmountMinor: l.snap.AmountPaise, Currency: payments.Currency, PayerID: l.snap.CustomerID, IntentID: l.snap.IntentID},
		paymentevents.Observed{AmountMinor: ev.AmountMinor, Currency: ev.Currency, PayerID: ev.PayerID, IntentID: ev.IntentID},
		paymentevents.RequireStated,
	); err != nil {
		d := payments.Decision{Outcome: payments.OutcomeMismatch, Effect: payments.EffectAttention, Detail: err.Error()}
		return d, s.flagAttentionTx(ctx, tx, ev, "change-of-professional payment: "+err.Error(), at)
	}
	switch l.snap.PaymentStatus {
	case "succeeded", "refunded", "partially_refunded":
		return payments.Decision{Outcome: payments.OutcomeAlreadyPaid, Detail: "payment already " + l.snap.PaymentStatus}, nil
	}
	// Captured: the payment, the bill and the booking's paid total.
	if _, err := tx.Exec(ctx, `UPDATE doorstep.payments SET status = 'succeeded', provider_payment_id = NULLIF($2, ''),
		payments_intent_id = COALESCE(payments_intent_id, NULLIF($3, '')), captured_at = $4, updated_at = $4 WHERE id = $1`,
		l.paymentID, ev.ProviderRef, ev.IntentID, at); err != nil {
		return payments.Decision{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.extras_bills SET status = 'paid', paid_at = $2, updated_at = $2 WHERE id = $1`,
		ev.ExtrasBillID, at); err != nil {
		return payments.Decision{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET paid_paise = paid_paise + $2, updated_at = $3 WHERE id = $1`,
		ev.BookingID, ev.AmountMinor, at); err != nil {
		return payments.Decision{}, err
	}
	c, err := scanChange(tx.QueryRow(ctx, `SELECT `+changeCols+` FROM doorstep.booking_pro_changes WHERE extras_bill_id = $1 FOR UPDATE`,
		ev.ExtrasBillID))
	if err != nil {
		return payments.Decision{}, err
	}
	if c.status == "pending_payment" && l.snap.BookingStatus == "pro_unavailable" {
		tag, err := tx.Exec(ctx, `UPDATE doorstep.pro_calendar_blocks SET kind = 'booking', expires_at = NULL
			WHERE booking_id = $1 AND pro_id = $2 AND kind = 'hold' AND active`, c.booking, c.toPro)
		if err != nil {
			return payments.Decision{}, err
		}
		held := tag.RowsAffected() > 0
		if !held {
			// The hold lapsed: the professional may still be free.
			if _, err := holdFirstTx(ctx, tx, c.booking, []uuid.UUID{c.toPro}, "booking", c.blockStart, c.blockEnd, nil); err == nil {
				held = true
			} else if !errors.Is(err, ErrSlotTaken) {
				return payments.Decision{}, err
			}
		}
		if held {
			var toUser uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT user_id FROM doorstep.professionals WHERE id = $1`, c.toPro).Scan(&toUser); err != nil {
				return payments.Decision{}, err
			}
			if err := s.commitChangeTx(ctx, tx, c, "pro_unavailable", "payment_event", nil, toUser, at); err != nil {
				return payments.Decision{}, err
			}
			return payments.Decision{Outcome: payments.OutcomeProChanged, Effect: payments.EffectConfirm}, nil
		}
	}
	// Nothing to apply it to: refund it in full, on this payment.
	if c.status == "pending_payment" {
		if _, err := tx.Exec(ctx, `UPDATE doorstep.booking_pro_changes SET status = 'abandoned', abandoned_at = $2, updated_at = $2
			WHERE id = $1`, c.id, at); err != nil {
			return payments.Decision{}, err
		}
	}
	cause := payments.CauseLateChangePrefix + shortHash(ev.ExtrasBillID.String())
	rid, err := insertRefundTx(ctx, tx, l.paymentID, ev.BookingID, cause, payments.RefundKey(ev.BookingID, cause), ev.AmountMinor, nil, at)
	if err != nil {
		return payments.Decision{}, err
	}
	applied.RefundIDs = append(applied.RefundIDs, rid)
	return payments.Decision{Outcome: payments.OutcomeLateCaptureRefund, Effect: payments.EffectLateCapture,
		Detail: "change-of-professional payment after the change lapsed (" + c.status + ", booking " + l.snap.BookingStatus + "); full refund requested"}, nil
}
