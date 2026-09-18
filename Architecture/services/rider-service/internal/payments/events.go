package payments

import (
	"github.com/atpost/shared/events"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
)

// Ride payment row statuses (rider_ride_payments.status).
const (
	StatusPendingCash       = "pending_cash_confirmation"
	StatusPending           = "pending"
	StatusConfirming        = "confirming"
	StatusSucceeded         = "succeeded"
	StatusFailed            = "failed"
	StatusRefunded          = "refunded"
	StatusPartiallyRefunded = "partially_refunded"
)

// Payment methods on a ride payment row.
const (
	MethodCash = "cash"
	MethodUPI  = "upi"
	MethodCard = "card"
)

// Targets a payments event can name: a ride's payment row, or a customer's
// outstanding fee paid directly (both reference type mopedu_ride; the
// consumer resolves the UUID against rides first, then outstanding rows),
// or a captain subscription checkout row (reference type mopedu_subscription).
const (
	TargetRide         = "ride"
	TargetOutstanding  = "outstanding"
	TargetSubscription = "subscription"
)

// Outstanding row statuses (rider_customer_outstanding.status).
const (
	OutstandingPending  = "pending"
	OutstandingSettled  = "settled"
	OutstandingWaived   = "waived"
	OutstandingRefunded = "refunded"
)

// Subscription checkout payment statuses
// (rider_partner_subscriptions.payment_status).
const (
	SubscriptionPaymentPending    = "pending"
	SubscriptionPaymentConfirming = "confirming"
	SubscriptionPaymentPaid       = "paid"
	SubscriptionPaymentFailed     = "failed"
)

// Outcome is recorded on the rider_payment_inbox row.
type Outcome string

const (
	OutcomePaid              Outcome = "paid"
	OutcomeSettled           Outcome = "outstanding_settled"
	OutcomeAlreadyPaid       Outcome = "already_paid"
	OutcomeMismatch          Outcome = "mismatch"
	OutcomeMarkedFailed      Outcome = "marked_failed"
	OutcomeFailedIgnored     Outcome = "failed_ignored"
	OutcomeRefunded          Outcome = "refunded"
	OutcomePartiallyRefunded Outcome = "partially_refunded"
	OutcomeRefundIgnored     Outcome = "refund_ignored"
	OutcomeIgnored           Outcome = "ignored"
	// OutcomeDuplicateCapture: a second capture, on a different intent, for a
	// ride already paid. Money taken twice: the second intent is refunded by
	// rule and a reconciliation row is written.
	OutcomeDuplicateCapture Outcome = "duplicate_capture"
	// OutcomeDuplicateRefunded: the payment.refunded event for a duplicate
	// capture's intent settled the rule refund; the payment row is untouched.
	OutcomeDuplicateRefunded Outcome = "duplicate_refunded"
	// OutcomeOutstandingRefunded: the rule refund of a directly paid
	// cancellation fee settled.
	OutcomeOutstandingRefunded Outcome = "outstanding_refunded"
	// Subscription checkout outcomes.
	OutcomeSubscriptionActivated Outcome = "subscription_activated"
	OutcomeSubscriptionFailed    Outcome = "subscription_payment_failed"
	// Decided by the store, not by Decide.
	OutcomeDuplicate      Outcome = "duplicate"
	OutcomeTargetNotFound Outcome = "target_not_found"
)

// Effect is what the store must write in the same transaction.
type Effect int

const (
	EffectNone Effect = iota
	// EffectMarkPaid: the online ride payment was captured -> succeeded.
	EffectMarkPaid
	// EffectMarkFailed: the online payment failed before capture -> failed
	// with the event's reason; the customer may retry or switch to cash.
	EffectMarkFailed
	// EffectRefund: the payment is now fully refunded -> refunded.
	EffectRefund
	// EffectPartialRefund: part of the payment was refunded ->
	// partially_refunded, refunded_paise += amount.
	EffectPartialRefund
	// EffectSettleOutstanding: the outstanding fee was paid -> settled.
	EffectSettleOutstanding
	// EffectRefundDuplicate: file a rule refund of the second intent's full
	// amount and a reconciliation row; the paid row is untouched. The
	// payments Refund call runs after commit (the store reports RefundID).
	EffectRefundDuplicate
	// EffectSettleDuplicateRefund: the duplicate intent's refund settled ->
	// the rule refund row is refunded; the payment row is untouched.
	EffectSettleDuplicateRefund
	// EffectRefundOutstanding: the rule refund of a directly paid outstanding
	// fee settled -> outstanding refunded, refund row refunded.
	EffectRefundOutstanding
	// EffectActivateSubscription: the subscription checkout was captured ->
	// active (starts now, or extends the renewed row's expiry), paid.
	EffectActivateSubscription
	// EffectMarkSubscriptionFailed: the checkout payment failed -> failed;
	// the captain checks out again.
	EffectMarkSubscriptionFailed
)

// Event is a payments-service event about a Mopedu reference, reduced to
// the fields a decision reads. AmountMinor is integer paise; for
// payment.refunded it is this refund's amount.
type Event struct {
	EventID   string
	EventType string
	IntentID  string
	// ReferenceType is mopedu_ride (rides, outstanding fees) or
	// mopedu_subscription; empty is treated as mopedu_ride.
	ReferenceType string
	ReferenceID   uuid.UUID
	PayerID       uuid.UUID
	AmountMinor   int64
	Currency      string
	Status        string
	ProviderRef   string
	// Reason is payment.failed's failure reason when the payload states one.
	Reason string
}

// Snapshot is the target as read FOR UPDATE inside the applying
// transaction: a ride payment row (Kind ride) or an outstanding row (Kind
// outstanding).
type Snapshot struct {
	Kind string
	// ID is the payment row id or the outstanding row id.
	ID     uuid.UUID
	RideID uuid.UUID
	// Status is the row status (payments: StatusX; outstanding: OutstandingX).
	Status string
	// Method is the payment method (rides only).
	Method      string
	AmountMinor int64
	Currency    string
	// PayerID is the ride's customer / the outstanding row's customer.
	PayerID uuid.UUID
	// IntentID is the intent bound to the row; empty until an intent was
	// created for it.
	IntentID      string
	RefundedMinor int64
	// RuleRefundPending is set by the store when a rule refund row in
	// requested/accepted exists for the EVENT's intent (a duplicate capture's
	// intent on a ride, or the settled intent of an outstanding fee), so a
	// payment.refunded for it settles that row and nothing else.
	RuleRefundPending bool
}

// Decision is the pure result of Decide.
type Decision struct {
	Outcome Outcome
	Effect  Effect
	Detail  string
}

var (
	onlineAwaiting = set(StatusPending, StatusConfirming, StatusFailed)
	onlineFailable = set(StatusPending, StatusConfirming)
	moneyTaken     = set(StatusSucceeded, StatusPartiallyRefunded, StatusRefunded)
	refundable     = set(StatusSucceeded, StatusPartiallyRefunded)
	onlineMethods  = set(MethodUPI, MethodCard)
)

func set(v ...string) map[string]bool {
	m := make(map[string]bool, len(v))
	for _, s := range v {
		m[s] = true
	}
	return m
}

// Decide is the payment-event decision table. It has no I/O; the store
// calls it with the target locked and applies the Effect in the same
// transaction as the inbox row. A mismatch (amount, currency, payer or
// intent) is never an effect: the store records it on
// rider_payment_reconciliation as reconciliation_required.
func Decide(s Snapshot, ev Event) Decision {
	switch s.Kind {
	case TargetOutstanding:
		return decideOutstanding(s, ev)
	case TargetSubscription:
		return decideSubscription(s, ev)
	}
	switch ev.EventType {
	case events.EventPaymentSucceeded:
		return decideSucceeded(s, ev)
	case events.EventPaymentFailed:
		return decideFailed(s)
	case events.EventPaymentRefunded:
		return decideRefunded(s, ev)
	}
	return Decision{Outcome: OutcomeIgnored, Detail: "unhandled event type " + ev.EventType}
}

func decideSucceeded(s Snapshot, ev Event) Decision {
	// Amount, currency, payer and (once bound) intent are checked before
	// state, with the RequireStated rule: an event that does not STATE its
	// currency or payer marks nothing.
	currency := s.Currency
	if currency == "" {
		currency = CurrencyINR
	}
	// Rule (b): a second capture on ANOTHER intent for a ride already paid
	// is money taken twice. The rule refund of the second intent is filed
	// in the same transaction; nothing about the paid row changes.
	if DuplicateCapture(DuplicateCaptureFacts{
		RowStatus: s.Status, RowMethod: s.Method, BoundIntentID: s.IntentID, EventIntentID: ev.IntentID,
		RowAmountMinor: s.AmountMinor, EventAmountMinor: ev.AmountMinor, RowCurrency: currency, EventCurrency: ev.Currency,
		RowPayerID: s.PayerID, EventPayerID: ev.PayerID,
	}) {
		return Decision{Outcome: OutcomeDuplicateCapture, Effect: EffectRefundDuplicate,
			Detail: "duplicate capture on intent " + ev.IntentID + " for a ride already paid on " + s.IntentID}
	}
	if err := paymentevents.CheckCapture(
		paymentevents.Expected{AmountMinor: s.AmountMinor, Currency: currency, PayerID: s.PayerID, IntentID: s.IntentID},
		paymentevents.Observed{AmountMinor: ev.AmountMinor, Currency: ev.Currency, PayerID: ev.PayerID, IntentID: ev.IntentID},
		paymentevents.RequireStated,
	); err != nil {
		return mismatch(err.Error())
	}
	if moneyTaken[s.Status] {
		return Decision{Outcome: OutcomeAlreadyPaid, Detail: "payment already " + s.Status}
	}
	// A capture for a ride whose payment is (now) cash is money taken that
	// the row cannot absorb: reconciliation, never paid.
	if !onlineMethods[s.Method] {
		return mismatch("captured online but the ride payment is " + s.Method + " (" + s.Status + ")")
	}
	if onlineAwaiting[s.Status] {
		// A capture after a failure (the customer retried the same intent)
		// still pays: the money was taken for exactly this ride.
		return Decision{Outcome: OutcomePaid, Effect: EffectMarkPaid}
	}
	return Decision{Outcome: OutcomeIgnored, Detail: "payment is " + s.Status}
}

func decideFailed(s Snapshot) Decision {
	if onlineMethods[s.Method] && onlineFailable[s.Status] {
		return Decision{Outcome: OutcomeMarkedFailed, Effect: EffectMarkFailed}
	}
	return Decision{Outcome: OutcomeFailedIgnored, Detail: "payment is " + s.Method + "/" + s.Status}
}

func decideRefunded(s Snapshot, ev Event) Decision {
	// A refund of a duplicate capture's intent settles the rule refund row
	// and never touches the paid row's amounts (that money was never the
	// ride's).
	if s.RuleRefundPending && ev.IntentID != "" && ev.IntentID != s.IntentID {
		return Decision{Outcome: OutcomeDuplicateRefunded, Effect: EffectSettleDuplicateRefund}
	}
	if !refundable[s.Status] {
		return Decision{Outcome: OutcomeRefundIgnored, Detail: "payment is " + s.Status}
	}
	// The refund may not exceed what is left of the payment, so repeated
	// partial refunds can never take back more than was paid.
	remaining := s.AmountMinor - s.RefundedMinor
	if err := paymentevents.CheckRefund(
		paymentevents.Expected{AmountMinor: remaining, IntentID: s.IntentID},
		paymentevents.Observed{AmountMinor: ev.AmountMinor, IntentID: ev.IntentID},
	); err != nil {
		return mismatch(err.Error())
	}
	full := s.RefundedMinor+ev.AmountMinor >= s.AmountMinor
	switch {
	case ev.Status == StatusRefunded || (full && ev.Status == StatusPartiallyRefunded):
		return Decision{Outcome: OutcomeRefunded, Effect: EffectRefund}
	case ev.Status == StatusPartiallyRefunded:
		return Decision{Outcome: OutcomePartiallyRefunded, Effect: EffectPartialRefund}
	}
	return Decision{Outcome: OutcomeRefundIgnored, Detail: "refund status " + ev.Status}
}

func decideOutstanding(s Snapshot, ev Event) Decision {
	switch ev.EventType {
	case events.EventPaymentSucceeded:
		if err := paymentevents.CheckCapture(
			paymentevents.Expected{AmountMinor: s.AmountMinor, Currency: CurrencyINR, PayerID: s.PayerID, IntentID: s.IntentID},
			paymentevents.Observed{AmountMinor: ev.AmountMinor, Currency: ev.Currency, PayerID: ev.PayerID, IntentID: ev.IntentID},
			paymentevents.RequireStated,
		); err != nil {
			return mismatch(err.Error())
		}
		switch s.Status {
		case OutstandingPending:
			return Decision{Outcome: OutcomeSettled, Effect: EffectSettleOutstanding}
		case OutstandingSettled:
			return Decision{Outcome: OutcomeAlreadyPaid, Detail: "outstanding already settled"}
		}
		// Captured for a waived fee: money taken for nothing owed.
		return mismatch("captured for an outstanding fee that is " + s.Status)
	case events.EventPaymentFailed:
		return Decision{Outcome: OutcomeFailedIgnored, Detail: "outstanding fee stays " + s.Status}
	case events.EventPaymentRefunded:
		// Only the rule refund of a directly paid fee (rule (c)) is expected
		// here, and only for the intent that settled the row, in full.
		if s.Status == OutstandingSettled && s.RuleRefundPending && s.IntentID != "" && ev.IntentID == s.IntentID &&
			ev.AmountMinor == s.AmountMinor && (ev.Status == StatusRefunded || ev.Status == StatusPartiallyRefunded) {
			return Decision{Outcome: OutcomeOutstandingRefunded, Effect: EffectRefundOutstanding}
		}
		return Decision{Outcome: OutcomeRefundIgnored, Detail: "no rule refund is pending for outstanding fee (" + s.Status + ")"}
	}
	return Decision{Outcome: OutcomeIgnored, Detail: "unhandled event type " + ev.EventType}
}

// decideSubscription is the captain subscription checkout table. Status is
// the row's payment_status. A capture must match the plan price, the
// currency and the captain (RequireStated), and the bound intent.
func decideSubscription(s Snapshot, ev Event) Decision {
	switch ev.EventType {
	case events.EventPaymentSucceeded:
		currency := s.Currency
		if currency == "" {
			currency = CurrencyINR
		}
		if err := paymentevents.CheckCapture(
			paymentevents.Expected{AmountMinor: s.AmountMinor, Currency: currency, PayerID: s.PayerID, IntentID: s.IntentID},
			paymentevents.Observed{AmountMinor: ev.AmountMinor, Currency: ev.Currency, PayerID: ev.PayerID, IntentID: ev.IntentID},
			paymentevents.RequireStated,
		); err != nil {
			return mismatch(err.Error())
		}
		if s.AmountMinor <= 0 {
			return mismatch("captured for a free (trial) subscription")
		}
		switch s.Status {
		case SubscriptionPaymentPending, SubscriptionPaymentConfirming, SubscriptionPaymentFailed:
			// A capture after a failure (the captain retried the same intent)
			// still activates: the money was taken for exactly this period.
			return Decision{Outcome: OutcomeSubscriptionActivated, Effect: EffectActivateSubscription}
		case SubscriptionPaymentPaid:
			return Decision{Outcome: OutcomeAlreadyPaid, Detail: "subscription already paid"}
		}
		return mismatch("captured for a subscription whose payment is " + s.Status)
	case events.EventPaymentFailed:
		switch s.Status {
		case SubscriptionPaymentPending, SubscriptionPaymentConfirming:
			return Decision{Outcome: OutcomeSubscriptionFailed, Effect: EffectMarkSubscriptionFailed}
		}
		return Decision{Outcome: OutcomeFailedIgnored, Detail: "subscription payment stays " + s.Status}
	case events.EventPaymentRefunded:
		return Decision{Outcome: OutcomeRefundIgnored, Detail: "subscription refunds are not applied by rule"}
	}
	return Decision{Outcome: OutcomeIgnored, Detail: "unhandled event type " + ev.EventType}
}

// mismatch records a refused money check; the detail is the shared check's.
func mismatch(detail string) Decision {
	return Decision{Outcome: OutcomeMismatch, Detail: detail}
}

// Applied is what the store reports back after committing.
type Applied struct {
	Decision Decision
	Target   string
	// RideID is set for a ride payment; CustomerID for both ride targets
	// (for a subscription it is the captain's user id).
	RideID     uuid.UUID
	TargetID   uuid.UUID
	CustomerID uuid.UUID
	// PartnerID is the subscription's partner.
	PartnerID uuid.UUID
	// RefundID is the rule refund row EffectRefundDuplicate filed; the
	// service sends the payments Refund command for it after commit.
	RefundID uuid.UUID
	// Status is the row status after the effect.
	Status string
}
