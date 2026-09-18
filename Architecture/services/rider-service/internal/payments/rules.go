package payments

// Automatic refunds by rule (online payments only). Each rule is a pure
// function over facts the store reads; the service files the refund through
// the same rider_ride_refunds path an admin uses, with requested_by = the
// fixed system actor and the rule as the reason code. Discretionary refunds
// stay admin-only and never come through here.
//
//	(a) RuleCaptainCancel     — the captain (or admin / system) cancelled a
//	                            ride the customer had paid online: full
//	                            remaining amount back.
//	(b) RuleDuplicateCapture  — a second payment.succeeded on a DIFFERENT
//	                            intent for a ride already paid: the second
//	                            capture back in full, plus reconciliation.
//	(c) RuleCancellationFee   — a cancellation fee paid online whose
//	                            cancellation is inside the free window or
//	                            partner-caused: the fee back.
//
// Cash never reaches a rule: a cash dispute is an outstanding waiver.

import (
	"time"

	"github.com/google/uuid"
)

// Refund rule codes (rider_ride_refunds.rule_code).
const (
	RuleDiscretionary    = "discretionary"
	RuleCaptainCancel    = "captain_cancel"
	RuleDuplicateCapture = "duplicate_capture"
	RuleCancellationFee  = "cancellation_fee"
)

// SystemActorID is the fixed actor automatic refunds and automatic
// verifications are recorded under (requested_by, audit admin_user_id). A
// name-based UUID so every replica names the same actor; it is not an
// account and can never sign in.
var SystemActorID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://momentum.app/actor/mopedu-system"))

// CaptainCancelFacts is what rule (a) reads: the ride's cancellation actor
// and its latest payment row.
type CaptainCancelFacts struct {
	// CancelledByKind is rider_rides.cancelled_by_kind: customer | partner |
	// admin | system (an expiry is system).
	CancelledByKind string
	PaymentMethod   string
	PaymentStatus   string
	AmountPaise     int64
	RefundedPaise   int64
	// InFlightPaise is the sum of refunds already requested/accepted.
	InFlightPaise int64
}

// CaptainCancelRefund returns the amount rule (a) refunds and whether it
// fires: only when the cancellation was NOT the customer's, the payment is
// online and captured (succeeded / partially_refunded), and something is
// left to refund.
func CaptainCancelRefund(f CaptainCancelFacts) (int64, bool) {
	switch f.CancelledByKind {
	case "partner", "admin", "system":
	default:
		return 0, false
	}
	if !onlineMethods[f.PaymentMethod] || !refundable[f.PaymentStatus] {
		return 0, false
	}
	remaining := f.AmountPaise - f.RefundedPaise - f.InFlightPaise
	if remaining <= 0 {
		return 0, false
	}
	return remaining, true
}

// DuplicateCaptureFacts is what rule (b) reads: the paid row and the
// capture event.
type DuplicateCaptureFacts struct {
	RowStatus        string
	RowMethod        string
	BoundIntentID    string
	EventIntentID    string
	RowAmountMinor   int64
	EventAmountMinor int64
	RowCurrency      string
	EventCurrency    string
	RowPayerID       uuid.UUID
	EventPayerID     uuid.UUID
}

// DuplicateCapture reports whether the event is a second capture for a ride
// already paid: the row's money is taken on a bound intent, the event names
// a DIFFERENT intent, and its amount, currency and payer are the ride's (a
// capture with another amount or payer is a mismatch for reconciliation, not
// a duplicate to refund).
func DuplicateCapture(f DuplicateCaptureFacts) bool {
	if !moneyTaken[f.RowStatus] || !onlineMethods[f.RowMethod] {
		return false
	}
	if f.BoundIntentID == "" || f.EventIntentID == "" || f.BoundIntentID == f.EventIntentID {
		return false
	}
	if f.EventAmountMinor <= 0 || f.EventAmountMinor != f.RowAmountMinor {
		return false
	}
	if f.EventCurrency == "" || !equalFold(f.EventCurrency, f.RowCurrency) {
		return false
	}
	if f.EventPayerID == uuid.Nil || f.EventPayerID != f.RowPayerID {
		return false
	}
	return true
}

// CancellationFeeFacts is what rule (c) reads: the outstanding fee row and
// the cancelled ride it belongs to.
type CancellationFeeFacts struct {
	// Reason is the outstanding row's reason; only cancellation_fee counts.
	Reason string
	// OutstandingStatus is settled when the fee was paid.
	OutstandingStatus string
	// SettledOnline is true when a payments intent settled the row directly
	// (settled_intent_id); a fee absorbed into the next ride's fare is not
	// refunded by rule.
	SettledOnline bool
	AmountPaise   int64
	// CancelledByKind is the ride's cancellation actor; no_show is the
	// captain reporting the customer absent (the customer's fault).
	CancelledByKind string
	AssignedAt      *time.Time
	CancelledAt     *time.Time
	// CancelFreeSeconds is the fare rule's free window after assignment.
	CancelFreeSeconds int
}

// CancellationFeeRefund returns the amount rule (c) refunds and whether it
// fires: the fee was paid online and the cancellation is now partner- or
// platform-caused (not the customer's), or falls inside the free window.
func CancellationFeeRefund(f CancellationFeeFacts) (int64, bool) {
	if f.Reason != "cancellation_fee" || f.OutstandingStatus != OutstandingSettled || !f.SettledOnline || f.AmountPaise <= 0 {
		return 0, false
	}
	switch f.CancelledByKind {
	case "customer", "no_show":
		// The customer's own cancellation: refundable only inside the free
		// window (a fee charged there was never owed).
		if f.AssignedAt == nil || f.CancelledAt == nil || f.CancelledAt.Before(*f.AssignedAt) {
			// Inconsistent facts (cancelled before assigned) are never a
			// reason to move money.
			return 0, false
		}
		free := time.Duration(f.CancelFreeSeconds) * time.Second
		if f.CancelledAt.Sub(*f.AssignedAt) <= free {
			return f.AmountPaise, true
		}
		return 0, false
	case "partner", "admin", "system":
		return f.AmountPaise, true
	}
	return 0, false
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'a' && ca <= 'z' {
			ca -= 'a' - 'A'
		}
		if cb >= 'a' && cb <= 'z' {
			cb -= 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
