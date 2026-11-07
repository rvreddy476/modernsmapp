package postgres

// A second captured intent on one commerce order (commerce.order.duplicate_capture).
//
// Commerce binds one intent to an order, and the first capture to land is the
// order's payment. When a capture on ANOTHER intent of the same order lands
// after it, commerce changes nothing and emits commerce.order.duplicate_capture.
// The buyer has paid twice; this records that intent's payment as owed back:
// a payments.refund_required row, reason duplicate_order_capture, which the
// refund-required worker refunds and the provider's signed refund webhook
// resolves, as for a second payment on one intent (M1).
//
// Commerce decides, payments refunds. Which intent is the order's is
// commerce's record (orders.payment_intent_id); payments sees two succeeded
// intents with one reference and cannot tell which the order kept, because the
// one that captured first here need not be the one commerce applied first.
// What was captured, and what has been refunded, is payments' record. So the
// event names the intent and every money figure comes from this ledger, never
// from the event.
//
// Every guard is a refusal (ErrOrderDuplicateRefused): dead-lettered and
// alarmed, never retried, nothing written.
//
//   - The duplicate is a commerce order intent of exactly this order.
//   - It is succeeded with nothing refunded or reserved for refund: a payment
//     already being returned through refund_commands is not refunded twice.
//   - The event's amount and currency are the intent's.
//   - The bound intent is a DIFFERENT intent of the same order and is itself
//     paid. Without this an event naming the order's only payment refunds it.
//
// One refund per payment: the row's key is (provider, provider_payment_id),
// so a redelivery is a no-op. The intent is not touched and payment.refunded
// is never published for it (ApplyRefundWebhookAtomically books the refund to
// this row), because commerce's order ledger must not count it.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrOrderDuplicateRefused means a duplicate-capture report does not match
// this ledger. Nothing is written.
var ErrOrderDuplicateRefused = errors.New("payments: duplicate order capture refused")

// ErrIntentOwedBack refuses a refund command on an intent whose captured
// payment is already being returned as a duplicate order capture.
var ErrIntentOwedBack = errors.New("payments: the intent's payment is owed back as a duplicate order capture")

// reasonDuplicateOrderCapture is the refund_required reason for a whole
// intent owed back: its payment is the intent's own settled payment, which is
// why a refund of it is booked to the row (classifyRefundTarget), not the
// intent.
const reasonDuplicateOrderCapture = "duplicate_order_capture"

// commerceOwnerDomain and commerceReferenceType are how a commerce order's
// intent is stamped (the handler's domainForRef, servicetoken.RefOrder).
const (
	commerceOwnerDomain   = "commerce-service"
	commerceReferenceType = "order"
)

// OrderDuplicateCapture is commerce.order.duplicate_capture as received.
type OrderDuplicateCapture struct {
	// EventID is the envelope id, recorded on the row.
	EventID       string
	OrderID       uuid.UUID
	IntentID      uuid.UUID // the duplicate: refunded
	BoundIntentID uuid.UUID // the order's own payment: never refunded here
	AmountMinor   int64
	Currency      string
}

// orderIntent is what the guards read of one intent.
type orderIntent struct {
	ID              uuid.UUID
	OwnerDomain     string
	ReferenceType   string
	ReferenceID     uuid.UUID
	Status          string
	AmountMinor     int64
	CapturedMinor   *int64 // set on a bank-offer capture (migration 014)
	Currency        string
	RefundedMinor   int64
	ReservedMinor   int64
	Provider        string
	ProviderOrderID string
	ProviderPayment string
}

// isOrderIntent reports whether i is a commerce intent of order.
func (i orderIntent) isOrderIntent(order uuid.UUID) bool {
	return i.OwnerDomain == commerceOwnerDomain && i.ReferenceType == commerceReferenceType && i.ReferenceID == order
}

// duplicateOwedMinor checks a duplicate-capture report against the two
// intents and returns what the duplicate's payment is owed back: the money it
// captured, which is less than the order value on a bank-offer capture. It has
// no I/O.
func duplicateOwedMinor(ev OrderDuplicateCapture, dup, bound orderIntent) (int64, error) {
	refuse := func(format string, args ...any) (int64, error) {
		return 0, fmt.Errorf("%w: order %s intent %s: %s", ErrOrderDuplicateRefused, ev.OrderID, ev.IntentID,
			fmt.Sprintf(format, args...))
	}
	switch {
	case ev.IntentID == uuid.Nil || ev.BoundIntentID == uuid.Nil:
		return refuse("the duplicate and the bound intent must both be named")
	case ev.IntentID == ev.BoundIntentID:
		return refuse("the duplicate is the order's own intent")
	case !dup.isOrderIntent(ev.OrderID):
		return refuse("not a commerce intent of this order (%s %s/%s)", dup.OwnerDomain, dup.ReferenceType, dup.ReferenceID)
	case !bound.isOrderIntent(ev.OrderID):
		return refuse("the bound intent %s is not a commerce intent of this order", bound.ID)
	case !paidStatuses[bound.Status]:
		return refuse("the bound intent %s is %s; the order's only payment is never refunded as a duplicate",
			bound.ID, bound.Status)
	case dup.Status != "succeeded" || dup.RefundedMinor != 0 || dup.ReservedMinor != 0:
		return refuse("the duplicate is %s with %d refunded and %d reserved; it is already being refunded",
			dup.Status, dup.RefundedMinor, dup.ReservedMinor)
	case ev.AmountMinor != dup.AmountMinor:
		return refuse("amount %d, the intent is %d", ev.AmountMinor, dup.AmountMinor)
	case strings.TrimSpace(ev.Currency) == "" || !strings.EqualFold(strings.TrimSpace(ev.Currency), strings.TrimSpace(dup.Currency)):
		return refuse("currency %q, the intent is %q", ev.Currency, dup.Currency)
	}
	owed := dup.AmountMinor
	if dup.CapturedMinor != nil {
		owed = *dup.CapturedMinor
	}
	if owed <= 0 || owed > dup.AmountMinor {
		return refuse("captured %d on an intent of %d", owed, dup.AmountMinor)
	}
	return owed, nil
}

// duplicateOrderPaymentKey is the refund_required key for the duplicate's
// payment: its provider payment id, or the intent when it holds none (the
// column refuses a blank key; the worker parks such a row, see
// service.routeOwedRefund).
func duplicateOrderPaymentKey(dup orderIntent) string {
	if p := strings.TrimSpace(dup.ProviderPayment); p != "" {
		return p
	}
	return OwedKeyIntentPrefix + dup.ID.String()
}

// alreadyOwedBack decides a duplicate whose payment key is already in
// refund_required, recorded on intent owner. On the duplicate's own intent it
// is a redelivery (nil). On another intent it is refused: one provider payment
// owed back on two intents is a reconciliation break, and answering it as a
// redelivery would acknowledge a duplicate nobody refunds as this intent's. It
// has no I/O.
func alreadyOwedBack(key string, owner, dup uuid.UUID) error {
	if owner != dup {
		return fmt.Errorf("%w: payment %s is already owed back on intent %s", ErrOrderDuplicateRefused, key, owner)
	}
	return nil
}

// OwedKeyIntentPrefix marks a refund_required key that is an intent, not a
// provider payment: no provider can refund it.
const OwedKeyIntentPrefix = "intent:"

const orderIntentColumns = `id, COALESCE(owner_domain,''), COALESCE(reference_type,''), reference_id, status,
	COALESCE(amount_minor,0), captured_minor, currency, COALESCE(refunded_amount_minor,0),
	COALESCE(refund_reserved_minor,0), COALESCE(provider,'razorpay'),
	COALESCE(NULLIF(provider_order_id,''), COALESCE(provider_ref,'')), COALESCE(provider_payment_id,'')`

func readOrderIntentTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, lock string) (orderIntent, error) {
	var i orderIntent
	err := tx.QueryRow(ctx, `SELECT `+orderIntentColumns+` FROM payments.payment_intents WHERE id = $1 `+lock, id).
		Scan(&i.ID, &i.OwnerDomain, &i.ReferenceType, &i.ReferenceID, &i.Status, &i.AmountMinor, &i.CapturedMinor,
			&i.Currency, &i.RefundedMinor, &i.ReservedMinor, &i.Provider, &i.ProviderOrderID, &i.ProviderPayment)
	if errors.Is(err, pgx.ErrNoRows) {
		return i, fmt.Errorf("%w: intent %s does not exist", ErrOrderDuplicateRefused, id)
	}
	return i, err
}

// RecordOrderDuplicateCapture records the duplicate's payment as owed back. It
// reports whether a NEW row was written; a redelivery is (false, nil).
func (s *Store) RecordOrderDuplicateCapture(ctx context.Context, ev OrderDuplicateCapture) (bool, error) {
	if ev.IntentID == uuid.Nil || ev.BoundIntentID == uuid.Nil {
		return false, fmt.Errorf("%w: the duplicate and the bound intent must both be named", ErrOrderDuplicateRefused)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// The duplicate is locked: a refund command on it (CreateRefundCommand
	// takes the same lock) is either already reserved, and this refuses, or
	// comes after, and ErrIntentOwedBack refuses it. The bound intent is only
	// read: every paid status it can be in is terminal or moves to another.
	dup, err := readOrderIntentTx(ctx, tx, ev.IntentID, "FOR UPDATE")
	if err != nil {
		return false, err
	}
	bound, err := readOrderIntentTx(ctx, tx, ev.BoundIntentID, "")
	if err != nil {
		return false, err
	}
	owed, err := duplicateOwedMinor(ev, dup, bound)
	if err != nil {
		return false, err
	}

	key := duplicateOrderPaymentKey(dup)
	tag, err := tx.Exec(ctx,
		`INSERT INTO payments.refund_required
		     (intent_id, provider, provider_order_id, provider_payment_id, event_id,
		      amount_minor, currency, reason, application_id)
		 VALUES ($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,
		         (SELECT application_id FROM payments.payment_intents WHERE id = $1))
		 ON CONFLICT (provider, provider_payment_id) DO NOTHING`,
		dup.ID, dup.Provider, dup.ProviderOrderID, key, ev.EventID, owed, dup.Currency, reasonDuplicateOrderCapture)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		// Already recorded. It must be this intent's: one provider payment
		// owed back on two intents is a reconciliation break.
		var owner uuid.UUID
		if err := tx.QueryRow(ctx,
			`SELECT intent_id FROM payments.refund_required WHERE provider = $1 AND provider_payment_id = $2`,
			dup.Provider, key).Scan(&owner); err != nil {
			return false, err
		}
		if err := alreadyOwedBack(key, owner, dup.ID); err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO payments.payment_audit_log (intent_id, event, old_status, new_status, metadata)
		 VALUES ($1,'duplicate_order_capture_refund_required',$2,$2,$3)`,
		dup.ID, dup.Status,
		[]byte(fmt.Sprintf(`{"order_id":%q,"bound_intent_id":%q,"event_id":%q,"provider_payment_id":%q,"amount_minor":%d}`,
			ev.OrderID, bound.ID, ev.EventID, key, owed))); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// intentOwedBackTx reports whether an intent's own payment is recorded as a
// duplicate order capture, in the caller's transaction.
func intentOwedBackTx(ctx context.Context, tx pgx.Tx, intentID uuid.UUID) (bool, error) {
	var owed bool
	err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM payments.refund_required WHERE intent_id = $1 AND reason = $2)`,
		intentID, reasonDuplicateOrderCapture).Scan(&owed)
	return owed, err
}
