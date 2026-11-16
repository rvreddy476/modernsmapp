package postgres

// A signed capture whose money does not match its intent (B2) is refunded,
// not left on an alarm.
//
// ApplyWebhookAtomically refuses such a capture and rolls everything back, so
// the intent stays non-terminal and nothing is published: correct, because a
// capture that disagrees with us about how much money moved must never become
// payment.succeeded. But the buyer's money HAD moved, and nothing returned it:
// the provider retried, the alarm fired, and the reconciler, which only ever
// settles a capture that verifies, left it alone for ever. The buyer was
// charged, had no order, and had no refund unless a person noticed.
//
// RecordMismatchedCapture records that payment as owed back, exactly like a
// late or duplicate capture: a payments.refund_required row (reason
// capture_mismatch), which the refund-required worker refunds and the signed
// refund webhook resolves (classifyRefundTarget: the payment is not the
// intent's settled payment and is recorded, so the intent is never credited
// and payment.refunded is never published). The intent is not touched — it
// stays pending, so a correct payment can still settle it — and the provider
// inbox row is still not written, so the provider's retries keep alarming
// until the refund lands. A retry finds the row (one per provider payment) and
// writes nothing.
//
// The service calls this only for a FINAL refusal (shouldRefundMismatch): a
// lower capture while bank offers are on belongs to the offer rule, whose
// evidence can contradict the event, and stays an operator alarm.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrMismatchNotRecordable means the refused capture does not carry what a
// refund needs (a payment id, a positive amount, a currency). Nothing is
// written; the alarm stands.
var ErrMismatchNotRecordable = errors.New("payments: a mismatched capture cannot be recorded for a refund")

// mismatchRecordable checks a refused capture has what a refund of it needs.
// It has no I/O.
func mismatchRecordable(e WebhookEffect) error {
	switch {
	case e.NewStatus != "succeeded":
		return fmt.Errorf("%w: not a capture (%q)", ErrMismatchNotRecordable, e.NewStatus)
	case strings.TrimSpace(e.ProviderPaymentID) == "":
		return fmt.Errorf("%w: no provider payment id", ErrMismatchNotRecordable)
	case strings.TrimSpace(e.ProviderOrderID) == "":
		return fmt.Errorf("%w: no provider order id", ErrMismatchNotRecordable)
	case e.AmountMinor <= 0:
		return fmt.Errorf("%w: captured amount %d", ErrMismatchNotRecordable, e.AmountMinor)
	case strings.TrimSpace(e.Currency) == "":
		return fmt.Errorf("%w: no currency", ErrMismatchNotRecordable)
	}
	return nil
}

// RecordMismatchedCapture records a refused capture as owed back. It reports
// whether a NEW row was written; a retry of the same payment, or an intent
// that is no longer pending (its own paths record late and duplicate
// captures), is (false, nil).
func (s *Store) RecordMismatchedCapture(ctx context.Context, e WebhookEffect) (bool, error) {
	if e.Provider == "" {
		e.Provider = "razorpay"
	}
	if err := mismatchRecordable(e); err != nil {
		return false, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	ids, err := intentIDsByRef(ctx, tx,
		`SELECT id FROM payments.payment_intents
		  WHERE provider = $1 AND provider_order_id = $2
		  ORDER BY id`, e.Provider, e.ProviderOrderID)
	if err != nil {
		return false, err
	}
	if len(ids) != 1 {
		return false, fmt.Errorf("%w: provider order %q matches %d intents", ErrMismatchNotRecordable, e.ProviderOrderID, len(ids))
	}
	var (
		intentID        uuid.UUID
		status, settled string
		intentCurrency  string
	)
	if err := tx.QueryRow(ctx,
		`SELECT id, status, COALESCE(provider_payment_id,''), COALESCE(currency,'')
		   FROM payments.payment_intents WHERE id = $1 FOR UPDATE`, ids[0]).
		Scan(&intentID, &status, &settled, &intentCurrency); err != nil {
		return false, err
	}
	if status != "pending" && status != "processing" {
		return false, nil
	}
	if strings.TrimSpace(settled) == strings.TrimSpace(e.ProviderPaymentID) {
		// Never owe back the payment that settles the intent.
		return false, fmt.Errorf("%w: payment %s is the intent's own", ErrMismatchNotRecordable, e.ProviderPaymentID)
	}

	var rowID int64
	err = tx.QueryRow(ctx,
		`INSERT INTO payments.refund_required
		     (intent_id, provider, provider_order_id, provider_payment_id, event_id,
		      amount_minor, currency, reason, application_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,
		         (SELECT application_id FROM payments.payment_intents WHERE id = $1))
		 ON CONFLICT (provider, provider_payment_id) DO NOTHING
		 RETURNING id`,
		intentID, e.Provider, e.ProviderOrderID, strings.TrimSpace(e.ProviderPaymentID), e.EventID,
		e.AmountMinor, strings.TrimSpace(e.Currency), reasonCaptureMismatch).Scan(&rowID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // already owed back (this capture's retry, or another reason)
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO payments.payment_audit_log (intent_id, event, old_status, new_status, metadata)
		 VALUES ($1,'capture_mismatch_refund_required',$2,$2,$3)`,
		intentID, status,
		[]byte(fmt.Sprintf(`{"provider":%q,"event_id":%q,"provider_payment_id":%q,"amount_minor":%d,"currency":%q,"intent_currency":%q}`,
			e.Provider, e.EventID, e.ProviderPaymentID, e.AmountMinor, e.Currency, intentCurrency))); err != nil {
		return false, err
	}
	if err := enqueueOwedCaptureTx(ctx, tx, rowID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
