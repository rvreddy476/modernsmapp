package postgres

// Money owed back on a capture the intent could not accept (migration 018).
//
// payments.refund_required holds a late capture on a FAILED intent and a
// duplicate capture (a second, distinct provider payment) on a paid one. Two
// things act on a row, and nothing else may:
//
//   - the refund-required worker claims it (SKIP LOCKED, next_attempt_at as
//     the lease) and asks the provider to refund THAT payment, under an
//     idempotency key derived from the row id (OwedCapture.IdempotencyKey);
//   - the provider's signed refund webhook for that payment settles it, in
//     ApplyRefundWebhookAtomically. The intent is never touched and
//     payment.refunded is never published: the order was not refunded, the
//     buyer's second payment was.
//
// A refund event naming a payment that is neither the intent's captured
// payment nor a recorded owed capture is refused (ErrRefundOfUnrecordedPayment)
// and rolls back, so the provider retries until the capture that explains it
// has been recorded. Booking it against the intent is the defect this closes:
// a refunded duplicate marked a still-paid order refunded.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrRefundOfUnrecordedPayment means a refund event names a provider payment
// that is not the intent's captured payment and is not recorded as owed back.
// Nothing is written; the provider retries.
var ErrRefundOfUnrecordedPayment = errors.New("payments: refund is for a payment this intent never settled and no owed capture records")

// ResolutionRefundedByProvider is refund_required.resolution once signed
// refund events cover the owed payment.
const ResolutionRefundedByProvider = "refunded"

// refundTarget is what a refund event's payment id says it refunds.
type refundTarget int

const (
	// refundOfIntent: the intent's own captured payment, or a refund that
	// cannot be told apart from one (no payment id on either side).
	refundOfIntent refundTarget = iota
	// refundOfOwedCapture: a payment recorded in refund_required.
	refundOfOwedCapture
	// refundOfUnknownPayment: some other payment. Refused.
	refundOfUnknownPayment
)

// classifyRefundTarget decides what a refund event refunds from the intent's
// captured payment id, the event's payment id and the reason of the
// owed-capture row recorded for the event's payment ("" when none is).
//
// A duplicate order capture (order_duplicate.go) is the one owed row whose
// payment IS the intent's settled payment: the whole intent is owed back
// because the order kept another. Its refund settles the row and never the
// intent, or payment.refunded would reach commerce's order ledger for money
// that was never the order's.
func classifyRefundTarget(intentPaymentID, eventPaymentID, owedReason string) refundTarget {
	settled, refunded := strings.TrimSpace(intentPaymentID), strings.TrimSpace(eventPaymentID)
	owedRecorded := owedReason != ""
	switch {
	case refunded == "":
		return refundOfIntent // nothing to compare (the stub's own refunds)
	case owedReason == reasonDuplicateOrderCapture:
		return refundOfOwedCapture
	case refunded == settled:
		return refundOfIntent
	case owedRecorded:
		return refundOfOwedCapture
	case settled == "":
		// An intent settled before provider_payment_id was written: it cannot
		// be told apart from its own capture, as before.
		return refundOfIntent
	default:
		return refundOfUnknownPayment
	}
}

// OwedCapture is one payments.refund_required row.
type OwedCapture struct {
	ID                int64
	IntentID          uuid.UUID
	Provider          string
	ProviderOrderID   string
	ProviderPaymentID string
	AmountMinor       int64
	Currency          string
	Reason            string
	RefundedMinor     int64
	ProviderRefundID  string
	Attempts          int
	Resolved          bool
}

// IdempotencyKey is the provider idempotency key for refunding this row. It
// is derived from the row id alone, so every attempt, on every replica, asks
// for the same refund.
func (o OwedCapture) IdempotencyKey() string {
	return "refund-required-" + strconv.FormatInt(o.ID, 10)
}

// RemainingMinor is what is still owed back on this payment.
func (o OwedCapture) RemainingMinor() int64 {
	if r := o.AmountMinor - o.RefundedMinor; r > 0 {
		return r
	}
	return 0
}

const owedCaptureColumns = `id, intent_id, provider, COALESCE(provider_order_id,''), provider_payment_id,
	amount_minor, COALESCE(currency,''), reason, refunded_minor, COALESCE(provider_refund_id,''),
	refund_attempts, resolved_at IS NOT NULL`

func scanOwedCapture(row pgx.Row, o *OwedCapture) error {
	return row.Scan(&o.ID, &o.IntentID, &o.Provider, &o.ProviderOrderID, &o.ProviderPaymentID,
		&o.AmountMinor, &o.Currency, &o.Reason, &o.RefundedMinor, &o.ProviderRefundID,
		&o.Attempts, &o.Resolved)
}

// owedCaptureForRefundTx locks the owed-capture row for a refunded payment,
// or returns nil when the payment id is blank or none is recorded.
// owedReason is the reason of an owed-capture row, "" for none.
func owedReason(o *OwedCapture) string {
	if o == nil {
		return ""
	}
	if r := strings.TrimSpace(o.Reason); r != "" {
		return r
	}
	return "unspecified" // recorded all the same
}

func owedCaptureForRefundTx(ctx context.Context, tx pgx.Tx, provider, paymentID string) (*OwedCapture, error) {
	if strings.TrimSpace(paymentID) == "" {
		return nil, nil
	}
	var o OwedCapture
	err := scanOwedCapture(tx.QueryRow(ctx,
		`SELECT `+owedCaptureColumns+` FROM payments.refund_required
		  WHERE provider = $1 AND provider_payment_id = $2
		  FOR UPDATE`, provider, paymentID), &o)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// settleOwedCapture adds a settled refund to what was already refunded on an
// owed payment. It never clamps: a refund past what the payment captured is a
// reconciliation break and is refused. resolved is true once the payment is
// refunded in full. An owed row with no recorded amount is never resolved by
// arithmetic it cannot do.
func settleOwedCapture(owed, refundedBefore, refund int64) (refunded int64, resolved bool, err error) {
	if refund <= 0 {
		return 0, false, fmt.Errorf("payments: owed-capture refund must be positive, got %d", refund)
	}
	refunded = refundedBefore + refund
	if owed > 0 && refunded > owed {
		return 0, false, fmt.Errorf("payments: refunds of %d on an owed payment of %d", refunded, owed)
	}
	return refunded, owed > 0 && refunded == owed, nil
}

// applyOwedCaptureRefundTx records a signed refund of an owed payment and
// resolves the row once the payment is refunded in full, in the caller's
// transaction. It reports whether this refund was new (a refund already
// recorded under another event id is a no-op).
func applyOwedCaptureRefundTx(ctx context.Context, tx pgx.Tx, o *OwedCapture, intentCurrency string,
	e WebhookEffect, providerRefundID string) (bool, error) {
	want := strings.TrimSpace(o.Currency)
	if want == "" {
		want = strings.TrimSpace(intentCurrency)
	}
	got := strings.TrimSpace(e.Currency)
	if got == "" || want == "" || !strings.EqualFold(got, want) {
		return false, fmt.Errorf("%w: refund %s of owed payment %s is in %q, the payment is in %q",
			ErrWebhookAmountMismatch, providerRefundID, o.ProviderPaymentID, e.Currency, want)
	}
	refunded, resolved, err := settleOwedCapture(o.AmountMinor, o.RefundedMinor, e.AmountMinor)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrWebhookAmountMismatch, err)
	}

	tag, err := tx.Exec(ctx,
		`INSERT INTO payments.refund_required_refunds
		     (provider, provider_refund_id, refund_required_id, amount_minor, currency, event_id)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (provider, provider_refund_id) DO NOTHING`,
		e.Provider, providerRefundID, o.ID, e.AmountMinor, got, e.EventID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil // this refund is already counted
	}

	if _, err := tx.Exec(ctx,
		`UPDATE payments.refund_required
		    SET refunded_minor     = $2,
		        provider_refund_id = COALESCE(provider_refund_id, $3),
		        resolved_at        = CASE WHEN $4 THEN NOW() ELSE resolved_at END,
		        resolution         = CASE WHEN $4 THEN $5 ELSE resolution END,
		        last_error         = NULL
		  WHERE id = $1`,
		o.ID, refunded, providerRefundID, resolved, ResolutionRefundedByProvider); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO payments.payment_audit_log (intent_id, event, metadata)
		 VALUES ($1,'refund_required_refunded',$2)`,
		o.IntentID,
		[]byte(fmt.Sprintf(`{"provider":%q,"provider_payment_id":%q,"provider_refund_id":%q,"amount_minor":%d,"refunded_minor":%d,"resolved":%t}`,
			e.Provider, o.ProviderPaymentID, providerRefundID, e.AmountMinor, refunded, resolved))); err != nil {
		return false, err
	}
	o.RefundedMinor, o.Resolved = refunded, resolved
	return true, nil
}

// ─── The worker's queue ──────────────────────────────────────────────

// claimRefundRequiredSQL is the worker's claim: unresolved, not parked and
// due rows, oldest due first, locked with SKIP LOCKED so two replicas never
// take the same row, and leased by pushing next_attempt_at out (30 s per
// attempt, capped at 4 min) in the same statement.
const claimRefundRequiredSQL = `UPDATE payments.refund_required
    SET refund_attempts = refund_attempts + 1,
        next_attempt_at = NOW() + (LEAST(refund_attempts + 1, 8) * INTERVAL '30 seconds')
  WHERE id IN (
        SELECT id FROM payments.refund_required
         WHERE resolved_at IS NULL AND parked_at IS NULL
           AND next_attempt_at <= NOW()
         ORDER BY next_attempt_at, id
         FOR UPDATE SKIP LOCKED
         LIMIT $1)
RETURNING ` + owedCaptureColumns

// ClaimDueRefundRequired takes owed captures for a refund attempt. SKIP
// LOCKED lets replicas share the queue, and the claim pushes next_attempt_at
// out, which is the lease: a claimed row is not claimable again until it
// lapses, whether or not the claimant finishes. A submitted row stays
// claimable, so an attempt is repeated (same idempotency key, one refund at
// the provider) until the signed refund webhook resolves it.
func (s *Store) ClaimDueRefundRequired(ctx context.Context, limit int) ([]OwedCapture, error) {
	rows, err := s.db.Query(ctx, claimRefundRequiredSQL, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OwedCapture
	for rows.Next() {
		var o OwedCapture
		if err := scanOwedCapture(rows, &o); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// MarkRefundRequiredSubmitted records the refund the provider accepted. The
// row stays unresolved: only the signed refund webhook resolves it.
func (s *Store) MarkRefundRequiredSubmitted(ctx context.Context, id int64, providerRefundID string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE payments.refund_required
		    SET provider_refund_id  = COALESCE(provider_refund_id, NULLIF($2,'')),
		        refund_submitted_at = COALESCE(refund_submitted_at, NOW()),
		        last_error          = NULL
		  WHERE id = $1 AND resolved_at IS NULL`, id, providerRefundID)
	return err
}

// MarkRefundRequiredAttemptFailed records a failure the provider may yet
// recover from. The row stays claimable after its lease.
func (s *Store) MarkRefundRequiredAttemptFailed(ctx context.Context, id int64, reason string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE payments.refund_required SET last_error = $2 WHERE id = $1 AND resolved_at IS NULL`,
		id, boundRunes(reason, maxParkReasonRunes))
	return err
}

// ParkRefundRequired stops the worker on a row it can never refund as the
// data stands. It reports whether this call parked it.
func (s *Store) ParkRefundRequired(ctx context.Context, id int64, code, reason string) (bool, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE payments.refund_required
		    SET parked_at = NOW(), failure_code = $2, last_error = $3
		  WHERE id = $1 AND resolved_at IS NULL AND parked_at IS NULL`,
		id, code, boundRunes(reason, maxParkReasonRunes))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
