package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/doorstep-service/internal/events"
	"github.com/atpost/doorstep-service/internal/payments"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ApplyPaymentEvent applies one payments-service event in ONE transaction
// (food/rider pattern):
//
//  1. INSERT the doorstep.payment_inbox row; a conflict on event_id means it
//     was applied before -> OutcomeDuplicate, nothing else runs;
//  2. read the booking and its booking payment row FOR UPDATE;
//  3. payments.Decide (CheckCapture / CheckRefund inside);
//  4. apply the effect;
//  5. record the outcome on the inbox row; COMMIT.
//
// A mismatch commits the inbox row and the attention flag with no booking
// effect, so it is recorded once and never retried into a confirmation. Any
// error rolls the inbox row back with the rest, so a retry applies once.
func (s *Store) ApplyPaymentEvent(ctx context.Context, ev payments.Event) (payments.Applied, error) {
	applied := payments.Applied{BookingID: ev.BookingID}
	if strings.TrimSpace(ev.EventID) == "" {
		return applied, paymentevents.ErrNoEventID
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return applied, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claim := paymentevents.Claim{EventID: ev.EventID, EventType: ev.EventType, IntentID: ev.IntentID,
		ReferenceID: ev.BookingID, AmountMinor: ev.AmountMinor, Currency: ev.Currency}
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
	return applied, mapErr(tx.Commit(ctx))
}

// paymentInbox is doorstep.payment_inbox, the dedupe half of ApplyOnce.
type paymentInbox struct{}

func (paymentInbox) Claim(ctx context.Context, tx pgx.Tx, c paymentevents.Claim) (bool, error) {
	tag, err := tx.Exec(ctx, `
		INSERT INTO doorstep.payment_inbox (event_id, event_type, reference_type, reference_id, outcome)
		VALUES ($1, $2, 'doorstep_booking', $3, 'claimed')
		ON CONFLICT (event_id) DO NOTHING`, c.EventID, c.EventType, c.ReferenceID)
	if err != nil {
		return false, fmt.Errorf("record payment event: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

type paymentLock struct {
	snap        payments.Snapshot
	paymentID   uuid.UUID
	reservedPro *uuid.UUID
	slotStart   time.Time
	blockEnd    time.Time
	paidPaise   int64
}

func (s *Store) applyPaymentEffectTx(ctx context.Context, tx pgx.Tx, ev payments.Event, applied *payments.Applied) error {
	at := s.clock()
	var l paymentLock
	var intent *string
	err := tx.QueryRow(ctx, `
		SELECT b.status, b.customer_user_id, b.reserved_pro_id, b.slot_start,
		       b.slot_end + make_interval(mins => z.travel_buffer_minutes), b.paid_paise,
		       p.id, p.amount_paise, p.status, p.payments_intent_id,
		       EXISTS (SELECT 1 FROM doorstep.pro_calendar_blocks k WHERE k.booking_id = b.id AND k.kind = 'hold' AND k.active)
		FROM doorstep.bookings b
		JOIN doorstep.zones z ON z.id = b.zone_id
		JOIN doorstep.payments p ON p.booking_id = b.id AND p.reference_type = 'doorstep_booking'
		WHERE b.id = $1
		FOR UPDATE OF b, p`, ev.BookingID).Scan(&l.snap.BookingStatus, &l.snap.CustomerID, &l.reservedPro, &l.slotStart,
		&l.blockEnd, &l.paidPaise, &l.paymentID, &l.snap.AmountPaise, &l.snap.PaymentStatus, &intent, &l.snap.HoldActive)
	if errors.Is(err, pgx.ErrNoRows) {
		applied.Decision = payments.Decision{Outcome: payments.OutcomeBookingNotFound, Detail: "no such booking"}
		return recordInboxTx(ctx, tx, ev.EventID, applied.Decision)
	}
	if err != nil {
		return fmt.Errorf("read booking for payment event: %w", err)
	}
	if intent != nil {
		l.snap.IntentID = *intent
	}

	d := payments.Decide(l.snap, ev)
	switch d.Effect {
	case payments.EffectConfirm:
		err = s.confirmPaidTx(ctx, tx, ev, l, at, false)
	case payments.EffectLateCapture:
		d, err = s.lateCaptureTx(ctx, tx, ev, l, at, applied)
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
	if err != nil {
		return fmt.Errorf("apply %s (%s): %w", ev.EventType, d.Outcome, err)
	}
	applied.Decision = d
	return recordInboxTx(ctx, tx, ev.EventID, d)
}

func recordInboxTx(ctx context.Context, tx pgx.Tx, eventID string, d payments.Decision) error {
	_, err := tx.Exec(ctx, `UPDATE doorstep.payment_inbox SET outcome = $2, detail = NULLIF($3, '') WHERE event_id = $1`,
		eventID, string(d.Outcome), d.Detail)
	return err
}

// markCapturedTx records the capture on the payment row and the booking.
func markCapturedTx(ctx context.Context, tx pgx.Tx, ev payments.Event, l paymentLock, at time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE doorstep.payments SET status = 'succeeded', provider_payment_id = NULLIF($2, ''),
		payments_intent_id = COALESCE(payments_intent_id, NULLIF($3, '')), captured_at = $4, updated_at = $4 WHERE id = $1`,
		l.paymentID, ev.ProviderRef, ev.IntentID, at); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET paid_paise = $2, updated_at = $3 WHERE id = $1`, ev.BookingID, ev.AmountMinor, at)
	return err
}

// confirmPaidTx: captured, booking confirmed, the hold becomes the booking
// block (late: a fresh booking block was already inserted).
func (s *Store) confirmPaidTx(ctx context.Context, tx pgx.Tx, ev payments.Event, l paymentLock, at time.Time, late bool) error {
	if err := markCapturedTx(ctx, tx, ev, l, at); err != nil {
		return err
	}
	if !late {
		tag, err := tx.Exec(ctx, `UPDATE doorstep.pro_calendar_blocks SET kind = 'booking', expires_at = NULL
			WHERE booking_id = $1 AND kind = 'hold' AND active`, ev.BookingID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errors.New("store: confirming a booking whose hold is gone")
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET status = 'confirmed', confirmed_at = $2, hold_expires_at = NULL,
		version = version + 1, updated_at = $2 WHERE id = $1`, ev.BookingID, at); err != nil {
		return err
	}
	from := l.snap.BookingStatus
	var reason *string
	if late {
		r := "payment captured after the hold lapsed; the professional was still free"
		reason = &r
	}
	if err := historyTx(ctx, tx, ev.BookingID, &from, "confirmed", "payment_event", nil, reason, at); err != nil {
		return err
	}
	core, err := bookingCoreTx(ctx, tx, ev.BookingID)
	if err != nil {
		return err
	}
	return s.enqueueBookingEvent(ctx, tx, events.BookingConfirmed, core, at,
		events.BookingConfirmedData{BookingCore: core, PaidPaise: ev.AmountMinor, PaymentID: l.paymentID})
}

// lateCaptureTx: money arrived for a booking that is no longer waiting. An
// expired booking whose slot is still ahead is confirmed when the reserved
// professional's calendar still admits it (the exclusion constraint decides,
// in a savepoint); otherwise the capture is recorded and refunded in full.
func (s *Store) lateCaptureTx(ctx context.Context, tx pgx.Tx, ev payments.Event, l paymentLock, at time.Time,
	applied *payments.Applied) (payments.Decision, error) {
	if (l.snap.BookingStatus == "expired" || l.snap.BookingStatus == "pending_payment") && l.reservedPro != nil && l.slotStart.After(at) {
		if _, err := holdFirstTx(ctx, tx, ev.BookingID, []uuid.UUID{*l.reservedPro}, "booking", l.slotStart, l.blockEnd, nil); err == nil {
			return payments.Decision{Outcome: payments.OutcomeLateCaptureConfirmed, Effect: payments.EffectConfirm},
				s.confirmPaidTx(ctx, tx, ev, l, at, true)
		} else if !errors.Is(err, ErrSlotTaken) {
			return payments.Decision{}, err
		}
	}
	if err := markCapturedTx(ctx, tx, ev, l, at); err != nil {
		return payments.Decision{}, err
	}
	rid, err := insertRefundTx(ctx, tx, l.paymentID, ev.BookingID, payments.CauseLateCapture,
		payments.RefundKey(ev.BookingID, payments.CauseLateCapture), ev.AmountMinor, nil, at)
	if err != nil {
		return payments.Decision{}, err
	}
	applied.RefundIDs = append(applied.RefundIDs, rid)
	if l.snap.BookingStatus == "pending_payment" {
		// The hold is gone without the sweeper: close the booking too.
		if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET status = 'expired', hold_expires_at = NULL, version = version + 1,
			updated_at = $2 WHERE id = $1`, ev.BookingID, at); err != nil {
			return payments.Decision{}, err
		}
		from, reason := "pending_payment", "the hold lapsed before payment"
		if err := historyTx(ctx, tx, ev.BookingID, &from, "expired", "payment_event", nil, &reason, at); err != nil {
			return payments.Decision{}, err
		}
	}
	return payments.Decision{Outcome: payments.OutcomeLateCaptureRefund, Effect: payments.EffectLateCapture,
		Detail: "captured on a " + l.snap.BookingStatus + " booking; full refund requested"}, nil
}

// settleRefundTx: a refund settled. The refund row is the command's (else
// the oldest open one of that amount; else an external refund is recorded);
// the payment and booking totals move; doorstep.booking.refunded.
func (s *Store) settleRefundTx(ctx context.Context, tx pgx.Tx, ev payments.Event, l paymentLock, at time.Time) error {
	var rid uuid.UUID
	var cause string
	err := tx.QueryRow(ctx, `
		UPDATE doorstep.refunds SET status = 'succeeded', payments_refund_id = COALESCE(payments_refund_id, NULLIF($3, '')), updated_at = $4
		 WHERE id = (SELECT id FROM doorstep.refunds
		              WHERE booking_id = $1 AND payment_id = $5 AND status IN ('requested', 'pending', 'failed')
		                AND ((NULLIF($3, '') IS NOT NULL AND payments_refund_id = $3)
		                  OR (amount_paise = $2 AND (payments_refund_id IS NULL OR NULLIF($3, '') IS NULL)))
		              ORDER BY (payments_refund_id = NULLIF($3, '')) DESC NULLS LAST, created_at LIMIT 1)
		RETURNING id, cause`, ev.BookingID, ev.AmountMinor, ev.CommandID, at, l.paymentID).Scan(&rid, &cause)
	if errors.Is(err, pgx.ErrNoRows) {
		cause = payments.CauseExternalPrefix + shortHash(ev.EventID)
		err = tx.QueryRow(ctx, `
			INSERT INTO doorstep.refunds (payment_id, booking_id, cause, idempotency_key, amount_paise, status, payments_refund_id,
			                              created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, 'succeeded', NULLIF($6, ''), $7, $7) RETURNING id`,
			l.paymentID, ev.BookingID, cause, payments.RefundKey(ev.BookingID, cause), ev.AmountMinor, ev.CommandID, at).Scan(&rid)
	}
	if err != nil {
		return mapErr(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE doorstep.payments SET refunded_paise = LEAST(amount_paise, refunded_paise + $2),
		       status = CASE WHEN refunded_paise + $2 >= amount_paise THEN 'refunded' ELSE 'partially_refunded' END, updated_at = $3
		 WHERE id = $1`, l.paymentID, ev.AmountMinor, at); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET refunded_paise = LEAST(paid_paise, refunded_paise + $2), updated_at = $3
		WHERE id = $1`, ev.BookingID, ev.AmountMinor, at); err != nil {
		return err
	}
	core, err := bookingCoreTx(ctx, tx, ev.BookingID)
	if err != nil {
		return err
	}
	return s.enqueueBookingEvent(ctx, tx, events.BookingRefunded, core, at,
		events.BookingRefundData{BookingCore: core, RefundID: rid, AmountPaise: ev.AmountMinor, Cause: cause})
}

// refundFailedTx: payments-service could not make a refund. The row is
// failed (the worker never resubmits it: the same key would only return the
// same failed command), the booking needs attention and ops hear of it.
func (s *Store) refundFailedTx(ctx context.Context, tx pgx.Tx, ev payments.Event, l paymentLock, at time.Time) error {
	var rid uuid.UUID
	var cause string
	var amount int64
	err := tx.QueryRow(ctx, `
		UPDATE doorstep.refunds SET status = 'failed', last_error = $3, updated_at = $4
		 WHERE id = (SELECT id FROM doorstep.refunds WHERE booking_id = $1 AND payment_id = $5 AND status IN ('requested', 'pending')
		              AND (payments_refund_id = NULLIF($2, '') OR NULLIF($2, '') IS NULL)
		              ORDER BY created_at LIMIT 1)
		RETURNING id, cause, amount_paise`, ev.BookingID, ev.CommandID, truncateText(ev.Reason, 300), at, l.paymentID).Scan(&rid, &cause, &amount)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	reason := "refund failed at payments-service: " + truncateText(ev.Reason, 200)
	if err := s.flagAttentionTx(ctx, tx, ev, reason, at); err != nil {
		return err
	}
	if rid == uuid.Nil {
		return nil
	}
	core, err := bookingCoreTx(ctx, tx, ev.BookingID)
	if err != nil {
		return err
	}
	r := truncateText(ev.Reason, 200)
	return s.enqueueBookingEvent(ctx, tx, events.BookingRefundFailed, core, at,
		events.BookingRefundData{BookingCore: core, RefundID: rid, AmountPaise: amount, Cause: cause, Reason: &r})
}

// flagAttentionTx marks the booking for ops and tells the live board.
func (s *Store) flagAttentionTx(ctx context.Context, tx pgx.Tx, ev payments.Event, detail string, at time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET needs_attention = TRUE, attention_reason = $2, updated_at = $3 WHERE id = $1`,
		ev.BookingID, truncateText(detail, 500), at); err != nil {
		return err
	}
	core, err := bookingCoreTx(ctx, tx, ev.BookingID)
	if err != nil {
		return err
	}
	return s.enqueueBookingEvent(ctx, tx, events.BookingAttention, core, at,
		events.BookingAttentionData{BookingCore: core, EventType: ev.EventType, Detail: truncateText(detail, 500)})
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
