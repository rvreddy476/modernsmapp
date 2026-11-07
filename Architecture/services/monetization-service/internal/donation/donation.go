// Package donation decides what a payments-service event does to a
// fundraiser donation. It mirrors internal/membership: the store reads the
// rows FOR UPDATE inside the applying transaction, Decide says what they
// must become, and the store writes exactly that with the inbox row.
//
// The rule it exists for (review 2026-11-07): a donation counts towards a
// fundraiser ONLY on payments-service's signed payment.succeeded event for
// application `fundraisers`, reference type `fundraiser_donation`,
// reference id = donations.id, and only when the event's amount, currency
// and payer are the donation's own. The donate call records a pending row
// that counts for nothing. Money is integer paise throughout.
//
// Not yet wired end to end: payments-service has no `fundraisers`
// application and shared/servicetoken no `fundraiser_donation` reference
// type, so no intent can be opened for a donation and no such event can
// arrive. Until both exist (outside monetization's lane) donations stay
// pending, which is the safe failure: nothing is counted that was not paid.
package donation

import (
	"fmt"

	"github.com/atpost/shared/events"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
)

// Payments coordinates. Both must be registered in payments-service (and
// the reference type in shared/servicetoken) before any donation can be
// paid.
const (
	ApplicationID = "fundraisers"
	RefType       = "fundraiser_donation"
)

// Donation statuses (donations.status, migration 026).
const (
	StatusLegacy            = "legacy_unverified"
	StatusPending           = "pending"
	StatusSucceeded         = "succeeded"
	StatusFailed            = "failed"
	StatusPartiallyRefunded = "partially_refunded"
	StatusRefunded          = "refunded"
)

// Counted reports whether a donation with this status is part of the
// fundraiser's raised total and shown in its donor list.
func Counted(status string) bool {
	switch status {
	case StatusLegacy, StatusSucceeded, StatusPartiallyRefunded:
		return true
	}
	return false
}

// Donation is a donations row as read FOR UPDATE.
type Donation struct {
	ID            uuid.UUID
	FundraiserID  uuid.UUID
	DonorID       uuid.UUID
	AmountPaise   int64
	RefundedPaise int64
	Currency      string
	Status        string
	IntentID      string
}

// Fundraiser is the fundraiser row the donation belongs to, read FOR UPDATE.
type Fundraiser struct {
	ID          uuid.UUID
	GoalPaise   int64
	RaisedPaise int64
	Status      string
}

// Event is a payments-service event about a donation. For payment.refunded
// AmountMinor is the refund's.
type Event struct {
	EventID     string
	EventType   string
	IntentID    string
	DonationID  uuid.UUID
	PayerID     uuid.UUID
	AmountMinor int64
	Currency    string
}

// Outcome is recorded on the inbox row.
type Outcome string

const (
	OutcomeCounted           Outcome = "counted"
	OutcomeAlreadyCounted    Outcome = "already_counted"
	OutcomeMismatch          Outcome = "mismatch"
	OutcomeMarkedFailed      Outcome = "marked_failed"
	OutcomeFailedIgnored     Outcome = "failed_ignored"
	OutcomeRefunded          Outcome = "refunded"
	OutcomePartiallyRefunded Outcome = "partially_refunded"
	OutcomeRefundIgnored     Outcome = "refund_ignored"
	OutcomeIgnored           Outcome = "ignored"
	// Decided by the store, not by Decide.
	OutcomeDuplicate        Outcome = "duplicate"
	OutcomeDonationNotFound Outcome = "donation_not_found"
)

// Decision is the effect of one event. Donation is the row as it must be
// after it; nil means leave it alone.
type Decision struct {
	Outcome  Outcome
	Detail   string
	Donation *Donation
	// RaisedDeltaPaise moves fundraisers.raised_paise; DonorDelta its
	// donor_count.
	RaisedDeltaPaise int64
	DonorDelta       int
	// Complete marks the fundraiser completed: this donation reached its goal.
	Complete bool
}

// Applied is what the store did with one event.
type Applied struct {
	Decision Decision
	Donation Donation
}

// Decide is the decision table.
func Decide(d Donation, f Fundraiser, ev Event) Decision {
	switch ev.EventType {
	case events.EventPaymentSucceeded:
		return decideSucceeded(d, f, ev)
	case events.EventPaymentFailed:
		if d.Status != StatusPending {
			return Decision{Outcome: OutcomeFailedIgnored, Detail: "donation is " + d.Status}
		}
		next := d
		next.Status = StatusFailed
		return Decision{Outcome: OutcomeMarkedFailed, Donation: &next}
	case events.EventPaymentRefunded:
		return decideRefunded(d, ev)
	}
	return Decision{Outcome: OutcomeIgnored, Detail: "unhandled event type " + ev.EventType}
}

func decideSucceeded(d Donation, f Fundraiser, ev Event) Decision {
	switch d.Status {
	case StatusPending, StatusFailed:
		// A capture after a failure is still the donor's money.
	case StatusSucceeded, StatusPartiallyRefunded, StatusRefunded:
		if ev.IntentID != "" && d.IntentID != "" && ev.IntentID != d.IntentID {
			return Decision{Outcome: OutcomeMismatch,
				Detail: fmt.Sprintf("second capture on intent %s for a donation counted on %s", ev.IntentID, d.IntentID)}
		}
		return Decision{Outcome: OutcomeAlreadyCounted}
	default:
		return Decision{Outcome: OutcomeIgnored, Detail: "donation is " + d.Status}
	}
	// Amount, currency, payer, and a bound intent: all must be this
	// donation's, stated on the event. A payment of the right amount by
	// someone else, or in another currency, counts for nothing.
	if err := paymentevents.CheckCapture(
		paymentevents.Expected{AmountMinor: d.AmountPaise, Currency: d.Currency, PayerID: d.DonorID, IntentID: d.IntentID},
		paymentevents.Observed{AmountMinor: ev.AmountMinor, Currency: ev.Currency, PayerID: ev.PayerID, IntentID: ev.IntentID},
		paymentevents.RequireStated,
	); err != nil {
		return Decision{Outcome: OutcomeMismatch, Detail: err.Error()}
	}
	if d.AmountPaise <= 0 {
		return Decision{Outcome: OutcomeMismatch, Detail: "donation has no positive amount"}
	}
	next := d
	next.Status = StatusSucceeded
	if next.IntentID == "" {
		next.IntentID = ev.IntentID
	}
	return Decision{
		Outcome:          OutcomeCounted,
		Donation:         &next,
		RaisedDeltaPaise: d.AmountPaise,
		DonorDelta:       1,
		Complete:         f.Status == "active" && f.GoalPaise > 0 && f.RaisedPaise+d.AmountPaise >= f.GoalPaise,
	}
}

func decideRefunded(d Donation, ev Event) Decision {
	if d.Status != StatusSucceeded && d.Status != StatusPartiallyRefunded {
		return Decision{Outcome: OutcomeRefundIgnored, Detail: "donation is " + d.Status}
	}
	if err := paymentevents.CheckRefund(
		paymentevents.Expected{AmountMinor: d.AmountPaise - d.RefundedPaise, IntentID: d.IntentID},
		paymentevents.Observed{AmountMinor: ev.AmountMinor, IntentID: ev.IntentID},
	); err != nil {
		return Decision{Outcome: OutcomeMismatch, Detail: err.Error()}
	}
	next := d
	next.RefundedPaise += ev.AmountMinor
	dec := Decision{Donation: &next, RaisedDeltaPaise: -ev.AmountMinor}
	if next.RefundedPaise >= next.AmountPaise {
		next.Status = StatusRefunded
		dec.Outcome = OutcomeRefunded
		dec.DonorDelta = -1
	} else {
		next.Status = StatusPartiallyRefunded
		dec.Outcome = OutcomePartiallyRefunded
	}
	return dec
}
