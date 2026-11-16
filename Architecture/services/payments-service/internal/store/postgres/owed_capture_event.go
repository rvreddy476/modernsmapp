package postgres

// payment.owed_capture — the buyer-visible half of payments.refund_required.
//
// A capture an intent cannot accept is recorded as owed back
// (refund_required) and refunded by the refund-required worker; the signed
// refund webhook resolves the row. None of that ever reached the domain that
// owns the payment: payment.refunded is deliberately never published for an
// owed capture (its money is not the order's), so commerce could not tell the
// buyer that a second payment, a late payment or a mismatched payment had been
// taken and was being returned, nor when it came back.
//
// This event says so. It is published, in the transaction that changes the
// row, whenever:
//
//   - a row is recorded (late capture on a FAILED intent, a second provider
//     payment on one intent, a duplicate order capture commerce reported, a
//     capture whose money did not match the intent): state "refund_required";
//   - a signed refund of the owed payment is counted: state
//     "partially_refunded" or "refunded".
//
// It never moves money and nothing consumes it as a ledger entry: it is the
// owner domain's view of a refund payments-service already performs. Every
// figure comes from the refund_required row, never from the triggering event.
// A row the worker parks is announced by payment.refund_failed
// (reference_type "refund_required", reference_id the row id).
//
// Partitioned by the intent's reference (the order), so a refund's event
// cannot overtake the record of the payment it returns.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// EventPaymentOwedCapture is the event type. Local to payments-service like
// EventPaymentRefundPending: no consumer outside commerce reads it yet.
const EventPaymentOwedCapture = "payment.owed_capture"

// Owed-capture states, from the row's money alone (owedCaptureState).
const (
	OwedStateRefundRequired    = "refund_required"
	OwedStatePartiallyRefunded = "partially_refunded"
	OwedStateRefunded          = "refunded"
)

// reasonCaptureMismatch is the refund_required reason for a capture whose
// money did not match its intent (RecordMismatchedCapture).
const reasonCaptureMismatch = "capture_mismatch"

// owedCaptureState names a row's state. Resolved is authoritative: a row is
// refunded once the signed refunds cover it (applyOwedCaptureRefundTx sets
// resolved_at then). Anything refunded short of that is partial. It has no I/O.
func owedCaptureState(amountMinor, refundedMinor int64, resolved bool) string {
	switch {
	case resolved:
		return OwedStateRefunded
	case refundedMinor > 0 && (amountMinor <= 0 || refundedMinor < amountMinor):
		return OwedStatePartiallyRefunded
	case refundedMinor > 0:
		return OwedStateRefunded
	}
	return OwedStateRefundRequired
}

// OwedCapturePayload is the payment.owed_capture payload.
type OwedCapturePayload struct {
	// ID and IntentID are the intent the owed payment was made on.
	ID       uuid.UUID `json:"id"`
	IntentID uuid.UUID `json:"intent_id"`
	// RefundRequiredID is the refund_required row: the stable identity of
	// this owed payment (one row per provider payment).
	RefundRequiredID string `json:"refund_required_id"`
	// Reason is the row's reason: late_capture_after_failed,
	// duplicate_capture, duplicate_order_capture or capture_mismatch.
	Reason string `json:"reason"`
	// AmountMinor is the MONEY owed back: what the provider captured on this
	// payment (less than the order value on a bank-offer capture).
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	// RefundedMinor is the money signed refund events have returned so far.
	RefundedMinor int64 `json:"refunded_minor"`
	Resolved      bool  `json:"resolved"`
	// State is refund_required, partially_refunded or refunded.
	State         string    `json:"state"`
	DetectedAt    time.Time `json:"detected_at"`
	ReferenceType string    `json:"reference_type"`
	ReferenceID   string    `json:"reference_id"`
	PayerID       string    `json:"payer_id,omitempty"`
	ApplicationID string    `json:"application_id,omitempty"`
}

// enqueueOwedCaptureTx publishes payment.owed_capture for one refund_required
// row as it now stands, in the caller's transaction. A failure rolls the
// caller back with it: the state change and its announcement are one fact.
func enqueueOwedCaptureTx(ctx context.Context, tx pgx.Tx, rowID int64) error {
	var (
		p        OwedCapturePayload
		id       int64
		currency string
		refType  string
		refID    string
		payer    *uuid.UUID
		appID    string
	)
	err := tx.QueryRow(ctx,
		`SELECT r.id, r.intent_id, r.reason, r.amount_minor, COALESCE(NULLIF(btrim(r.currency),''), i.currency, ''),
		        r.refunded_minor, r.resolved_at IS NOT NULL, r.detected_at,
		        COALESCE(i.reference_type,''), COALESCE(i.reference_id::text,''), i.payer_id,
		        COALESCE(i.application_id,'')
		   FROM payments.refund_required r
		   JOIN payments.payment_intents i ON i.id = r.intent_id
		  WHERE r.id = $1`, rowID).
		Scan(&id, &p.IntentID, &p.Reason, &p.AmountMinor, &currency, &p.RefundedMinor, &p.Resolved,
			&p.DetectedAt, &refType, &refID, &payer, &appID)
	if err != nil {
		return fmt.Errorf("payments: read owed capture %d for its event: %w", rowID, err)
	}
	p.ID = p.IntentID
	p.RefundRequiredID = strconv.FormatInt(id, 10)
	p.Currency = strings.TrimSpace(currency)
	p.State = owedCaptureState(p.AmountMinor, p.RefundedMinor, p.Resolved)
	p.DetectedAt = p.DetectedAt.UTC()
	p.ReferenceType, p.ReferenceID, p.ApplicationID = refType, refID, appID
	if payer != nil && *payer != uuid.Nil {
		p.PayerID = payer.String()
	}
	key := refID
	if key == "" {
		key = p.IntentID.String()
	}
	return enqueueOutboxTx(ctx, tx, EventPaymentOwedCapture, key, nil, p)
}
