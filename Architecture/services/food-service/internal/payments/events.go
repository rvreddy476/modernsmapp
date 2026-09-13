package payments

import (
	"fmt"
	"strings"

	"github.com/atpost/food-service/internal/orderstate"
	"github.com/atpost/shared/events"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

// RefTypeFoodOrder is the payments reference type food owns.
const RefTypeFoodOrder = servicetoken.RefFoodOrder

// Outcome is recorded on the food.payment_event_inbox row.
type Outcome string

const (
	OutcomeConfirmed           Outcome = "confirmed"
	OutcomeAlreadyPaid         Outcome = "already_paid"
	OutcomeAmountMismatch      Outcome = "amount_mismatch"
	OutcomeLateCapture         Outcome = "late_capture_refund_required"
	OutcomeMarkedFailed        Outcome = "marked_failed"
	OutcomeIgnoredAfterCapture Outcome = "ignored_after_capture"
	OutcomeFailedIgnored       Outcome = "failed_ignored"
	OutcomeRefunded            Outcome = "refunded"
	OutcomePartiallyRefunded   Outcome = "partially_refunded"
	OutcomeRefundIgnored       Outcome = "refund_ignored"
	OutcomeIgnored             Outcome = "ignored"
	// Decided by the store, not by Decide.
	OutcomeDuplicate     Outcome = "duplicate"
	OutcomeOrderNotFound Outcome = "order_not_found"
)

// Effect is what the store must write in the same transaction.
type Effect int

const (
	EffectNone Effect = iota
	// EffectConfirm: payment CAPTURED; order -> CONFIRMED through the guard.
	EffectConfirm
	// EffectMarkFailed: payment FAILED; order -> PAYMENT_FAILED through the guard.
	EffectMarkFailed
	// EffectRefund: payment REFUNDED; order REFUND_PENDING -> REFUNDED through
	// the guard when it is REFUND_PENDING.
	EffectRefund
	// EffectPartialRefund: payment PARTIALLY_REFUNDED; order status unchanged
	// (food.order_status has no partial state).
	EffectPartialRefund
)

// Event is a payments-service event about a food order, reduced to the fields
// a decision reads. AmountMinor is integer paise; the deprecated float
// `amount` is never parsed. For payment.refunded, AmountMinor is the refund.
type Event struct {
	EventID     string
	EventType   string
	IntentID    string
	OrderID     uuid.UUID
	PayerID     uuid.UUID
	AmountMinor int64
	Currency    string
	Status      string
}

// OrderSnapshot is the order as read FOR UPDATE inside the applying
// transaction. AmountMinor is computed in SQL from NUMERIC(12,2).
type OrderSnapshot struct {
	OrderID       uuid.UUID
	UserID        uuid.UUID
	Status        string
	PaymentStatus string
	AmountMinor   int64
	Currency      string
	// IntentID is food.payments.provider_payment_id; empty when never set.
	IntentID string
}

// Decision is the pure result of Decide.
type Decision struct {
	Outcome Outcome
	Effect  Effect
	Detail  string
}

var (
	awaitingPayment = set(orderstate.Placed, orderstate.PaymentPending, orderstate.PaymentFailed)
	failable        = set(orderstate.Placed, orderstate.PaymentPending)
	fulfilment      = set(orderstate.Confirmed, orderstate.Preparing, orderstate.ReadyForPickup,
		orderstate.DeliveryAssigning, orderstate.DeliveryAssigned, orderstate.PickedUp,
		orderstate.OutForDelivery, orderstate.Delivered)
	moneyTaken = set("CAPTURED", "REFUND_PENDING", "PARTIALLY_REFUNDED", "REFUNDED")
	refundable = set("CAPTURED", "REFUND_PENDING", "PARTIALLY_REFUNDED")
)

func set(v ...string) map[string]bool {
	m := make(map[string]bool, len(v))
	for _, s := range v {
		m[s] = true
	}
	return m
}

// Decide is the payment-event decision table. It has no I/O; the store calls
// it with the order locked and applies the Effect in the same transaction as
// the inbox row.
func Decide(o OrderSnapshot, ev Event) Decision {
	switch ev.EventType {
	case events.EventPaymentSucceeded:
		return decideSucceeded(o, ev)
	case events.EventPaymentFailed:
		return decideFailed(o)
	case events.EventPaymentRefunded:
		return decideRefunded(o, ev)
	}
	return Decision{Outcome: OutcomeIgnored, Detail: "unhandled event type " + ev.EventType}
}

func decideSucceeded(o OrderSnapshot, ev Event) Decision {
	// Every party and the amount are checked before state: a capture that does
	// not match the order must never be recorded as "already paid" either.
	if ev.AmountMinor != o.AmountMinor {
		return mismatch("amount_minor %d != order %d", ev.AmountMinor, o.AmountMinor)
	}
	orderCurrency := o.Currency
	if orderCurrency == "" {
		orderCurrency = "INR"
	}
	if ev.Currency == "" || !strings.EqualFold(ev.Currency, orderCurrency) {
		return mismatch("currency %q != order %q", ev.Currency, orderCurrency)
	}
	if ev.PayerID == uuid.Nil || ev.PayerID != o.UserID {
		return mismatch("payer %s is not the order's customer", ev.PayerID)
	}
	if o.IntentID != "" && ev.IntentID != o.IntentID {
		return mismatch("intent %s is not the order's intent", ev.IntentID)
	}
	if moneyTaken[o.PaymentStatus] {
		return Decision{Outcome: OutcomeAlreadyPaid, Detail: "payment already " + o.PaymentStatus}
	}
	if awaitingPayment[o.Status] {
		return Decision{Outcome: OutcomeConfirmed, Effect: EffectConfirm}
	}
	if fulfilment[o.Status] {
		return Decision{Outcome: OutcomeAlreadyPaid, Detail: "order already " + o.Status}
	}
	// Cancelled, rejected, refunded, failed, draft: the customer has been
	// charged for an order nobody will cook. Never revive it.
	return Decision{Outcome: OutcomeLateCapture, Detail: "captured on a " + o.Status + " order"}
}

func decideFailed(o OrderSnapshot) Decision {
	if moneyTaken[o.PaymentStatus] {
		return Decision{Outcome: OutcomeIgnoredAfterCapture, Detail: "payment already " + o.PaymentStatus}
	}
	if failable[o.Status] {
		return Decision{Outcome: OutcomeMarkedFailed, Effect: EffectMarkFailed}
	}
	return Decision{Outcome: OutcomeFailedIgnored, Detail: "order is " + o.Status}
}

func decideRefunded(o OrderSnapshot, ev Event) Decision {
	if ev.AmountMinor <= 0 || ev.AmountMinor > o.AmountMinor {
		return mismatch("refund amount_minor %d outside order %d", ev.AmountMinor, o.AmountMinor)
	}
	if o.IntentID != "" && ev.IntentID != o.IntentID {
		return mismatch("refund intent %s is not the order's intent", ev.IntentID)
	}
	if !refundable[o.PaymentStatus] {
		return Decision{Outcome: OutcomeRefundIgnored, Detail: "payment is " + o.PaymentStatus}
	}
	switch ev.Status {
	case "refunded":
		return Decision{Outcome: OutcomeRefunded, Effect: EffectRefund}
	case "partially_refunded":
		return Decision{Outcome: OutcomePartiallyRefunded, Effect: EffectPartialRefund}
	}
	return Decision{Outcome: OutcomeRefundIgnored, Detail: "refund status " + ev.Status}
}

func mismatch(format string, args ...any) Decision {
	return Decision{Outcome: OutcomeAmountMismatch, Detail: fmt.Sprintf(format, args...)}
}
