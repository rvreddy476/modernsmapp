package payments

import (
	"time"

	"github.com/atpost/shared/events"
	"github.com/atpost/shared/paymentevents"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

// RefTypeDatingPremium is the payments reference type dating owns.
const RefTypeDatingPremium = servicetoken.RefDatingPremium

// Purchase statuses (dating_premium_purchases.status).
const (
	StatusCreated           = "created"
	StatusConfirming        = "confirming"
	StatusPaid              = "paid"
	StatusFailed            = "failed"
	StatusRefunded          = "refunded"
	StatusPartiallyRefunded = "partially_refunded"
)

// Outcome is recorded on the dating_payment_inbox row.
type Outcome string

const (
	OutcomeGranted           Outcome = "granted"
	OutcomeAlreadyPaid       Outcome = "already_paid"
	OutcomeAmountMismatch    Outcome = "amount_mismatch"
	OutcomeMarkedFailed      Outcome = "marked_failed"
	OutcomeFailedIgnored     Outcome = "failed_ignored"
	OutcomeRefunded          Outcome = "refunded"
	OutcomePartiallyRefunded Outcome = "partially_refunded"
	OutcomeRefundIgnored     Outcome = "refund_ignored"
	OutcomeIgnored           Outcome = "ignored"
	// Decided by the store, not by Decide.
	OutcomeDuplicate        Outcome = "duplicate"
	OutcomePurchaseNotFound Outcome = "purchase_not_found"
)

// Effect is what the store must write in the same transaction.
type Effect int

const (
	EffectNone Effect = iota
	// EffectGrant: payment captured. A pass extends expires_at to
	// GREATEST(expires_at, now()) + duration; a Boost adds one token.
	EffectGrant
	// EffectMarkFailed: payment failed before it was captured.
	EffectMarkFailed
	// EffectRevoke: the payment is now fully refunded. A pass loses whatever
	// of its granted time is not already revoked; a Boost loses its token
	// when the token is still unused.
	EffectRevoke
	// EffectPartialRevoke: part of the payment was refunded. A pass is
	// shortened pro rata (ProRataRevokeSeconds); a Boost is left untouched.
	EffectPartialRevoke
)

// Event is a payments-service event about a premium purchase, reduced to the
// fields a decision reads. AmountMinor is integer paise; for
// payment.refunded it is this refund's amount.
type Event struct {
	EventID     string
	EventType   string
	IntentID    string
	PurchaseID  uuid.UUID
	PayerID     uuid.UUID
	AmountMinor int64
	Currency    string
	Status      string
}

// PurchaseSnapshot is the purchase as read FOR UPDATE inside the applying
// transaction.
type PurchaseSnapshot struct {
	PurchaseID    uuid.UUID
	UserID        uuid.UUID
	Product       string
	Status        string
	AmountMinor   int64
	Currency      string
	RefundedMinor int64
	// IntentID is the payments intent bound to the purchase; empty until the
	// create call returned.
	IntentID string
}

// Decision is the pure result of Decide.
type Decision struct {
	Outcome Outcome
	Effect  Effect
	Detail  string
}

var (
	awaitingPayment = set(StatusCreated, StatusConfirming, StatusFailed)
	failable        = set(StatusCreated, StatusConfirming)
	moneyTaken      = set(StatusPaid, StatusPartiallyRefunded, StatusRefunded)
	refundable      = set(StatusPaid, StatusPartiallyRefunded)
)

func set(v ...string) map[string]bool {
	m := make(map[string]bool, len(v))
	for _, s := range v {
		m[s] = true
	}
	return m
}

// Decide is the payment-event decision table. It has no I/O; the store calls
// it with the purchase locked and applies the Effect in the same transaction
// as the inbox row.
func Decide(p PurchaseSnapshot, ev Event) Decision {
	switch ev.EventType {
	case events.EventPaymentSucceeded:
		return decideSucceeded(p, ev)
	case events.EventPaymentFailed:
		return decideFailed(p)
	case events.EventPaymentRefunded:
		return decideRefunded(p, ev)
	}
	return Decision{Outcome: OutcomeIgnored, Detail: "unhandled event type " + ev.EventType}
}

func decideSucceeded(p PurchaseSnapshot, ev Event) Decision {
	// Amount, currency, payer and (once bound) intent are checked before
	// state, with food's RequireStated rule: an event that does not STATE its
	// currency or payer grants nothing.
	currency := p.Currency
	if currency == "" {
		currency = CurrencyINR
	}
	if err := paymentevents.CheckCapture(
		paymentevents.Expected{AmountMinor: p.AmountMinor, Currency: currency, PayerID: p.UserID, IntentID: p.IntentID},
		paymentevents.Observed{AmountMinor: ev.AmountMinor, Currency: ev.Currency, PayerID: ev.PayerID, IntentID: ev.IntentID},
		paymentevents.RequireStated,
	); err != nil {
		return mismatch(err)
	}
	if moneyTaken[p.Status] {
		return Decision{Outcome: OutcomeAlreadyPaid, Detail: "purchase already " + p.Status}
	}
	if awaitingPayment[p.Status] {
		// A capture after a failure (the user retried the same intent) still
		// grants: the money was taken for exactly this purchase.
		return Decision{Outcome: OutcomeGranted, Effect: EffectGrant}
	}
	return Decision{Outcome: OutcomeIgnored, Detail: "purchase is " + p.Status}
}

func decideFailed(p PurchaseSnapshot) Decision {
	if failable[p.Status] {
		return Decision{Outcome: OutcomeMarkedFailed, Effect: EffectMarkFailed}
	}
	return Decision{Outcome: OutcomeFailedIgnored, Detail: "purchase is " + p.Status}
}

func decideRefunded(p PurchaseSnapshot, ev Event) Decision {
	if !refundable[p.Status] {
		return Decision{Outcome: OutcomeRefundIgnored, Detail: "purchase is " + p.Status}
	}
	// The refund may not exceed what is left of the purchase, so repeated
	// partial refunds can never revoke more than was paid for.
	remaining := p.AmountMinor - p.RefundedMinor
	if err := paymentevents.CheckRefund(
		paymentevents.Expected{AmountMinor: remaining, IntentID: p.IntentID},
		paymentevents.Observed{AmountMinor: ev.AmountMinor, IntentID: ev.IntentID},
	); err != nil {
		return mismatch(err)
	}
	full := p.RefundedMinor+ev.AmountMinor >= p.AmountMinor
	switch {
	case ev.Status == StatusRefunded || (full && ev.Status == StatusPartiallyRefunded):
		return Decision{Outcome: OutcomeRefunded, Effect: EffectRevoke}
	case ev.Status == StatusPartiallyRefunded:
		return Decision{Outcome: OutcomePartiallyRefunded, Effect: EffectPartialRevoke}
	}
	return Decision{Outcome: OutcomeRefundIgnored, Detail: "refund status " + ev.Status}
}

// mismatch records a refused money check; the detail is the shared check's.
func mismatch(err error) Decision {
	return Decision{Outcome: OutcomeAmountMismatch, Detail: err.Error()}
}

// ProRataRevokeSeconds is how much of a pass's granted time a cumulative
// refund takes back: granted * refunded / paid, floored, never more than
// granted. The store revokes the difference between this and what earlier
// refunds already revoked.
func ProRataRevokeSeconds(grantedSeconds, amountMinor, refundedMinor int64) int64 {
	if grantedSeconds <= 0 || amountMinor <= 0 || refundedMinor <= 0 {
		return 0
	}
	if refundedMinor >= amountMinor {
		return grantedSeconds
	}
	return grantedSeconds * refundedMinor / amountMinor
}

// Applied is what the store reports back after committing.
type Applied struct {
	Decision   Decision
	PurchaseID uuid.UUID
	UserID     uuid.UUID
	Product    string
	// PassExpiresAt is the user's pass expiry after the effect (passes only).
	PassExpiresAt *time.Time
}
