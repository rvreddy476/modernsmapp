package store

import (
	"context"
	"github.com/atpost/doorstep-service/internal/events"
	"github.com/atpost/doorstep-service/internal/payments"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

func (s *Store) visitExtrasSucceededTx(ctx context.Context, tx pgx.Tx, ev payments.Event, l paymentLock, at time.Time, applied *payments.Applied) (payments.Decision, error) {
	if err := paymentevents.CheckCapture(paymentevents.Expected{AmountMinor: l.snap.AmountPaise, Currency: payments.Currency, PayerID: l.snap.CustomerID, IntentID: l.snap.IntentID}, paymentevents.Observed{AmountMinor: ev.AmountMinor, Currency: ev.Currency, PayerID: ev.PayerID, IntentID: ev.IntentID}, paymentevents.RequireStated); err != nil {
		d := payments.Decision{Outcome: payments.OutcomeMismatch, Effect: payments.EffectAttention, Detail: err.Error()}
		return d, s.flagAttentionTx(ctx, tx, ev, "visit extras: "+err.Error(), at)
	}
	if l.snap.PaymentStatus == "succeeded" || l.snap.PaymentStatus == "partially_refunded" || l.snap.PaymentStatus == "refunded" {
		return payments.Decision{Outcome: payments.OutcomeAlreadyPaid}, nil
	}
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM doorstep.extras_bills WHERE id=$1 FOR UPDATE`, ev.ExtrasBillID).Scan(&status); err != nil {
		return payments.Decision{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.payments SET status='succeeded',provider_payment_id=NULLIF($2,''),payments_intent_id=COALESCE(payments_intent_id,NULLIF($3,'')),captured_at=$4,updated_at=$4 WHERE id=$1`, l.paymentID, ev.ProviderRef, ev.IntentID, at); err != nil {
		return payments.Decision{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET paid_paise=paid_paise+$2,version=version+1,updated_at=$3 WHERE id=$1`, ev.BookingID, ev.AmountMinor, at); err != nil {
		return payments.Decision{}, err
	}
	if status != "open" && status != "payment_pending" && status != "outstanding" {
		cause := "visit_late_" + shortHash(ev.ExtrasBillID.String())
		rid, err := insertRefundTx(ctx, tx, l.paymentID, ev.BookingID, cause, payments.RefundKey(ev.BookingID, cause), ev.AmountMinor, nil, at)
		if err != nil {
			return payments.Decision{}, err
		}
		applied.RefundIDs = append(applied.RefundIDs, rid)
		return payments.Decision{Outcome: payments.OutcomeLateCaptureRefund}, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.extras_bills SET status='paid',paid_at=$2,updated_at=$2 WHERE id=$1`, ev.ExtrasBillID, at); err != nil {
		return payments.Decision{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.outstanding SET status='paid',settled_at=$2 WHERE extras_bill_id=$1 AND status='open'`, ev.ExtrasBillID, at); err != nil {
		return payments.Decision{}, err
	}
	core, err := bookingCoreTx(ctx, tx, ev.BookingID)
	if err != nil {
		return payments.Decision{}, err
	}
	if err = s.enqueueBookingEvent(ctx, tx, events.BookingExtrasPaid, core, at, events.BookingExtrasBillData{BookingCore: core, BillID: ev.ExtrasBillID, AmountPaise: ev.AmountMinor}); err != nil {
		return payments.Decision{}, err
	}
	return payments.Decision{Outcome: payments.OutcomeVisitExtrasPaid}, nil
}

// BillPaymentID is a private lookup for opening an already-authorized bill.
func (s *Store) BillPaymentID(ctx context.Context, bill uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.db.QueryRow(ctx, `SELECT id FROM doorstep.payments WHERE extras_bill_id=$1`, bill).Scan(&id)
	return id, mapErr(err)
}
