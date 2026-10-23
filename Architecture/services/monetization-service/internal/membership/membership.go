// Package membership is the pure core of paid creator memberships on
// payments-service (phase 1 contract, pinned 23 Oct 2026): the statuses, the
// periods, the deterministic keys, and the decision table that turns one
// signed payment event into the effect the store must write.
//
// Money is applied ONLY from a signed payments-service event (payment.succeeded
// through shared/paymentevents Dispatch, ForApplication("memberships"),
// ApplyOnce and CheckCapture). Nothing here does I/O: the Postgres store and
// the in-memory test store both lock the rows, call Decide, and persist what
// it returns, so the rules are tested once and written twice mechanically.
//
// One membership_payments row is one paid period: a first join, a renewal, or
// an upgrade difference. Its id is the payments reference id.
package membership

import (
	"time"

	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

// Payments coordinates.
const (
	// ApplicationID is the payments application every membership intent and
	// refund belongs to (payments migration 016).
	ApplicationID = "memberships"
	// RefType is the payments reference type monetization owns.
	RefType = servicetoken.RefCreatorMembership
	// Currency is the only currency memberships are priced in.
	Currency = "INR"
)

// Payment kinds.
const (
	KindJoin    = "join"
	KindRenewal = "renewal"
	KindUpgrade = "upgrade"
)

// Subscription statuses. pending_payment is NOT live: it never grants access
// and is not covered by the one-live-subscription index; a separate partial
// unique index allows one pending join per (subscriber, creator).
const (
	StatusPendingPayment       = "pending_payment"
	StatusActive               = "active"
	StatusCancelledAtPeriodEnd = "cancelled_at_period_end"
	StatusPaused               = "paused"
	StatusPastDue              = "past_due"
	StatusGrace                = "grace"
	StatusCancelled            = "cancelled"
	StatusExpired              = "expired"
)

// membership_payments.status.
const (
	PayCreated    = "created"
	PayConfirming = "confirming"
	PayPaid       = "paid"
	PayFailed     = "failed"
	PayRefunded   = "refunded"
)

// membership_payments.refund_status.
const (
	RefundRequested = "requested" // queued here, not yet accepted by payments
	RefundPending   = "pending"   // payments accepted the command; money not back yet
	RefundRefunded  = "refunded"
	RefundFailed    = "failed"
)

// Refund causes.
const (
	CauseLateCapture = "late_capture"
	CauseSupport     = "support"
)

// Timings.
const (
	// PendingTTL: a join not paid within this is replaced or swept to expired.
	PendingTTL = 30 * time.Minute
	// GracePeriod: after the paid period ends without a renewal payment.
	GracePeriod = 72 * time.Hour
	// RenewAfterExpiryWindow: an expired membership may still be renewed
	// within this of its period end.
	RenewAfterExpiryWindow = 30 * 24 * time.Hour
	// ReminderLead and ReminderDay: the renewal_due reminders, 3 days before
	// the period end and on the day.
	ReminderLead = 72 * time.Hour
	ReminderDay  = 24 * time.Hour
)

// FeeBps is the platform commission on a membership payment. Founder
// decision 23 Oct 2026: zero for now, recorded so a rate can switch on later.
const FeeBps int64 = 0

// Split is a payment's fee and the creator's share. fee + share == amount.
func Split(amountPaise int64) (feePaise, creatorSharePaise int64) {
	fee := amountPaise * FeeBps / 10_000
	return fee, amountPaise - fee
}

// Truncate is the precision every stored time has: PostgreSQL keeps
// microseconds, so a time computed here is cut to them before it is written
// or compared, and a period end read back equals the one written.
func Truncate(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

// PeriodEnd is one billing period after start: one calendar month (monthly is
// the only period offered).
func PeriodEnd(start time.Time) time.Time { return start.AddDate(0, 1, 0) }

// RenewalStart is where a renewal's period starts: the paid period's end, or
// now if that has already passed (an expired or grace membership does not pay
// for the gap).
func RenewalStart(periodEnd, now time.Time) time.Time {
	if now.After(periodEnd) {
		return now
	}
	return periodEnd
}

// IsLive reports whether status still belongs to its subscriber (the
// one-live index's set). pending_payment is not live.
func IsLive(status string) bool {
	switch status {
	case StatusActive, StatusCancelledAtPeriodEnd, StatusPaused, StatusPastDue, StatusGrace:
		return true
	}
	return false
}

// CanRenew reports whether a renewal may be bought: a membership that was
// once activated and is active, cancelled at period end (paying re-enables
// renewal), past due, in grace, or expired within RenewAfterExpiryWindow of
// its period end.
func CanRenew(status string, activated bool, periodEnd, now time.Time) bool {
	if !activated {
		return false
	}
	switch status {
	case StatusActive, StatusCancelledAtPeriodEnd, StatusPastDue, StatusGrace:
		return true
	case StatusExpired:
		return now.Before(periodEnd.Add(RenewAfterExpiryWindow))
	}
	return false
}

// IntentKey is the payments idempotency key of a membership payment: a retry
// for the same paid period collapses to one intent. It is also the row's
// idempotency_key.
func IntentKey(paymentID uuid.UUID) string { return RefType + ":" + paymentID.String() }

// LateRefundKey is the refund key of a capture that could not be applied.
func LateRefundKey(paymentID uuid.UUID) string { return "membership-late:" + paymentID.String() }

// SupportRefundKey is the refund key of support's refund decision.
func SupportRefundKey(paymentID uuid.UUID) string { return "membership-refund:" + paymentID.String() }

// AgeYears is the age in whole years on now's calendar date (UTC).
func AgeYears(dob, now time.Time) int {
	dob, now = dob.UTC(), now.UTC()
	years := now.Year() - dob.Year()
	if now.Month() < dob.Month() || (now.Month() == dob.Month() && now.Day() < dob.Day()) {
		years--
	}
	return years
}

// Eligibility thresholds (founder, 23 Oct 2026).
const (
	MinAgeYears  = 18
	MinFollowers = 500
)

// Missing eligibility items, in the order they are reported.
const (
	MissingAge       = "age"
	MissingPAN       = "pan"
	MissingFollowers = "followers"
	MissingStrikes   = "strikes"
)

// StandingAnswer is trust-safety's standing for a user: "ok", "restricted"
// or "suspended", and how many strikes are active.
type StandingAnswer struct {
	Standing      string
	ActiveStrikes int
}
