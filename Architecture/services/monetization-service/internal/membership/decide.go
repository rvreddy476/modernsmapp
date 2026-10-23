package membership

import (
	"time"

	"github.com/atpost/shared/events"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
)

// Payment is a membership_payments row as read FOR UPDATE inside the applying
// transaction.
type Payment struct {
	ID             uuid.UUID
	SubscriptionID uuid.UUID
	PayerID        uuid.UUID
	CreatorID      uuid.UUID
	Kind           string
	// TierID/TierName/TierPricePaise: the tier this payment puts the
	// subscription on, and that tier's price as locked when it was asked for.
	TierID         uuid.UUID
	TierName       string
	TierPricePaise int64
	// FromTierID/FromTierName/FromPricePaise: an upgrade's starting tier, and
	// the price the subscription had locked on it.
	FromTierID     *uuid.UUID
	FromTierName   string
	FromPricePaise int64

	AmountPaise       int64
	FeePaise          int64
	CreatorSharePaise int64
	Currency          string
	// PeriodStart is the paid period's key: a join's creation time, the
	// period end a renewal renews FROM, an upgrade's request time.
	PeriodStart time.Time
	// PeriodEnd is the nominal end: an upgrade's is the subscription's period
	// end it was priced against.
	PeriodEnd time.Time
	// GrantedStart/GrantedEnd: the access this payment actually bought,
	// recorded when it was applied. A refund removes exactly this.
	GrantedStart *time.Time
	GrantedEnd   *time.Time

	IntentID string
	Status   string
	// LateCapture: the money arrived for a subscription that could no longer
	// use it, and is refunded.
	LateCapture   bool
	RefundStatus  string
	RefundKey     string
	RefundCause   string
	RefundPaise   int64
	RefundedPaise int64
	RefundError   string
	CreatedAt     time.Time
}

// Sub is the subscription a payment belongs to, read FOR UPDATE in the same
// transaction.
type Sub struct {
	ID                 uuid.UUID
	SubscriberID       uuid.UUID
	CreatorID          uuid.UUID
	TierID             uuid.UUID
	TierName           string
	PricePaise         int64
	Currency           string
	Status             string
	CurrentPeriodStart time.Time
	CurrentPeriodEnd   time.Time
	GracePeriodEnd     *time.Time
	PendingTierID      *uuid.UUID
	PendingPricePaise  *int64
	// Counted: this subscription is counted in its tier's subscriber_count.
	Counted     bool
	ActivatedAt *time.Time
	CreatedAt   time.Time
}

// Event is a payments-service event about a membership payment, reduced to
// what a decision reads. For payment.refunded AmountMinor is the refund's.
type Event struct {
	EventID     string
	EventType   string
	IntentID    string
	PaymentID   uuid.UUID
	PayerID     uuid.UUID
	AmountMinor int64
	Currency    string
	Status      string
	// ReasonCode is payment.refund_failed's closed-vocabulary reason.
	ReasonCode string
}

// Outcome is recorded on the inbox row.
type Outcome string

const (
	OutcomeActivated         Outcome = "activated"
	OutcomeRenewed           Outcome = "renewed"
	OutcomeUpgraded          Outcome = "upgraded"
	OutcomeAlreadyApplied    Outcome = "already_applied"
	OutcomeLateCapture       Outcome = "late_capture"
	OutcomeMismatch          Outcome = "mismatch"
	OutcomeMarkedFailed      Outcome = "marked_failed"
	OutcomeFailedIgnored     Outcome = "failed_ignored"
	OutcomeRefunded          Outcome = "refunded"
	OutcomePartiallyRefunded Outcome = "partially_refunded"
	OutcomeRefundIgnored     Outcome = "refund_ignored"
	OutcomeRefundFailed      Outcome = "refund_failed"
	OutcomeIgnored           Outcome = "ignored"
	// Decided by the store, not by Decide.
	OutcomeDuplicate       Outcome = "duplicate"
	OutcomePaymentNotFound Outcome = "payment_not_found"
)

// Membership notifications (topic monetization.events).
const (
	EventStarted       = "membership.started"
	EventRenewed       = "membership.renewed"
	EventRenewalDue    = "membership.renewal_due"
	EventPaymentFailed = "membership.payment_failed"
	EventEnded         = "membership.ended"
)

// Entitlement actions published on entitlement.changed.
const (
	EntitlementGranted    = "granted"
	EntitlementRenewed    = "renewed"
	EntitlementUpgraded   = "upgraded"
	EntitlementRevoked    = "revoked"
	EntitlementShortened  = "refunded"
	EntitlementDowngraded = "downgraded"
)

// TierDelta moves one tier's subscriber_count.
type TierDelta struct {
	TierID uuid.UUID
	Delta  int
}

// Refund is a refund the store must queue (and the service then files with
// payments under Key).
type Refund struct {
	Cause       string
	Key         string
	AmountPaise int64
}

// Decision is the effect of one event. Payment and Sub are the rows as they
// must be after it; nil means "leave the row alone".
type Decision struct {
	Outcome Outcome
	Detail  string

	Payment *Payment
	Sub     *Sub
	Tiers   []TierDelta
	// CreditCreatorPaise is the creator's share recorded as earnings now.
	CreditCreatorPaise int64
	// ReverseCreatorPaise is earnings taken back by a refund.
	ReverseCreatorPaise int64
	// ReverseEarningRow marks the payment's earning row reversed (a full
	// refund of an applied payment).
	ReverseEarningRow bool
	Refund            *Refund
	// Notify are the membership.* events to publish after commit, in order.
	Notify []string
	// Entitlement is the entitlement.changed action to publish, "" for none.
	Entitlement string
}

// Decide is the payment-event decision table. now is the applying
// transaction's clock. otherLive reports that ANOTHER live subscription for
// the same (subscriber, creator) exists, which makes activating this one
// impossible (the one-live index).
func Decide(p Payment, s Sub, otherLive bool, ev Event, now time.Time) Decision {
	now = Truncate(now)
	switch ev.EventType {
	case events.EventPaymentSucceeded:
		return decideSucceeded(p, s, otherLive, ev, now)
	case events.EventPaymentFailed:
		return decideFailed(p, ev)
	case events.EventPaymentRefunded:
		return decideRefunded(p, s, ev, now)
	case events.EventPaymentRefundFailed:
		return decideRefundFailed(p, ev)
	}
	return Decision{Outcome: OutcomeIgnored, Detail: "unhandled event type " + ev.EventType}
}

func mismatch(err error) Decision {
	return Decision{Outcome: OutcomeMismatch, Detail: err.Error()}
}

func ptrTime(t time.Time) *time.Time { return &t }

func decideSucceeded(p Payment, s Sub, otherLive bool, ev Event, now time.Time) Decision {
	// Amount, currency, payer and (once bound) intent are checked before any
	// state, with RequireStated: an event that does not STATE its currency
	// or payer applies nothing.
	currency := p.Currency
	if currency == "" {
		currency = Currency
	}
	if err := paymentevents.CheckCapture(
		paymentevents.Expected{AmountMinor: p.AmountPaise, Currency: currency, PayerID: p.PayerID, IntentID: p.IntentID},
		paymentevents.Observed{AmountMinor: ev.AmountMinor, Currency: ev.Currency, PayerID: ev.PayerID, IntentID: ev.IntentID},
		paymentevents.RequireStated,
	); err != nil {
		return mismatch(err)
	}
	if p.Status == PayPaid || p.Status == PayRefunded {
		// This row's money was already taken (applied, or refunded as late).
		// A second event for it moves nothing.
		return Decision{Outcome: OutcomeAlreadyApplied, Detail: "payment already " + p.Status}
	}
	// created, confirming, or failed (the member retried the same intent):
	// the money for exactly this row has arrived.
	np := p
	np.Status = PayPaid
	ns := s

	switch p.Kind {
	case KindJoin:
		if s.Status != StatusPendingPayment {
			return late(np, ev, "join captured for a subscription that is "+s.Status)
		}
		if otherLive {
			return late(np, ev, "join captured while another live subscription exists")
		}
		start, end := now, Truncate(PeriodEnd(now))
		ns.Status = StatusActive
		ns.CurrentPeriodStart, ns.CurrentPeriodEnd = start, end
		ns.GracePeriodEnd = nil
		ns.TierID, ns.TierName, ns.PricePaise = p.TierID, p.TierName, p.TierPricePaise
		if ns.ActivatedAt == nil {
			ns.ActivatedAt = ptrTime(now)
		}
		d := Decision{Outcome: OutcomeActivated, Notify: []string{EventStarted}, Entitlement: EntitlementGranted}
		if !s.Counted {
			ns.Counted = true
			d.Tiers = []TierDelta{{p.TierID, +1}}
		}
		np.GrantedStart, np.GrantedEnd = ptrTime(start), ptrTime(end)
		d.Payment, d.Sub, d.CreditCreatorPaise = &np, &ns, p.CreatorSharePaise
		return d

	case KindRenewal:
		if !CanRenew(s.Status, s.ActivatedAt != nil, s.CurrentPeriodEnd, now) {
			return late(np, ev, "renewal captured for a subscription that is "+s.Status)
		}
		if !s.CurrentPeriodEnd.Equal(p.PeriodStart) {
			// The period this payment renews was already renewed (or moved):
			// a second payment for the same period is refunded, never stacked.
			return late(np, ev, "renewal captured for a period that is no longer the current one")
		}
		if s.Status == StatusExpired && otherLive {
			return late(np, ev, "renewal of an expired subscription while another live one exists")
		}
		start := Truncate(RenewalStart(s.CurrentPeriodEnd, now))
		end := Truncate(PeriodEnd(start))
		ns.Status = StatusActive
		ns.CurrentPeriodStart, ns.CurrentPeriodEnd = start, end
		ns.GracePeriodEnd = nil
		ns.PendingTierID, ns.PendingPricePaise = nil, nil
		ns.TierID, ns.TierName, ns.PricePaise = p.TierID, p.TierName, p.TierPricePaise
		d := Decision{Outcome: OutcomeRenewed, Notify: []string{EventRenewed}, Entitlement: EntitlementRenewed}
		switch {
		case !s.Counted:
			ns.Counted = true
			d.Tiers = []TierDelta{{p.TierID, +1}}
		case s.TierID != p.TierID:
			d.Tiers = []TierDelta{{s.TierID, -1}, {p.TierID, +1}}
		}
		np.GrantedStart, np.GrantedEnd = ptrTime(start), ptrTime(end)
		d.Payment, d.Sub, d.CreditCreatorPaise = &np, &ns, p.CreatorSharePaise
		return d

	case KindUpgrade:
		if s.Status != StatusActive || p.FromTierID == nil || s.TierID != *p.FromTierID || !s.CurrentPeriodEnd.Equal(p.PeriodEnd) {
			return late(np, ev, "upgrade captured after the subscription changed")
		}
		ns.TierID, ns.TierName, ns.PricePaise = p.TierID, p.TierName, p.TierPricePaise
		ns.PendingTierID, ns.PendingPricePaise = nil, nil
		d := Decision{Outcome: OutcomeUpgraded, Entitlement: EntitlementUpgraded}
		if s.Counted {
			d.Tiers = []TierDelta{{s.TierID, -1}, {p.TierID, +1}}
		}
		np.GrantedStart, np.GrantedEnd = ptrTime(now), ptrTime(s.CurrentPeriodEnd)
		d.Payment, d.Sub, d.CreditCreatorPaise = &np, &ns, p.CreatorSharePaise
		return d
	}
	return Decision{Outcome: OutcomeIgnored, Detail: "unknown payment kind " + p.Kind}
}

// late records a capture that cannot be applied and queues its full refund
// under membership-late:{paymentId}. No access, no earnings.
func late(np Payment, ev Event, detail string) Decision {
	np.LateCapture = true
	np.RefundStatus = RefundRequested
	np.RefundCause = CauseLateCapture
	np.RefundKey = LateRefundKey(np.ID)
	np.RefundPaise = ev.AmountMinor
	return Decision{
		Outcome: OutcomeLateCapture,
		Detail:  detail,
		Payment: &np,
		Refund:  &Refund{Cause: CauseLateCapture, Key: np.RefundKey, AmountPaise: ev.AmountMinor},
	}
}

func decideFailed(p Payment, ev Event) Decision {
	if p.IntentID != "" && ev.IntentID != "" && ev.IntentID != p.IntentID {
		return Decision{Outcome: OutcomeMismatch, Detail: "failed event for intent " + ev.IntentID + " is not the payment's intent"}
	}
	if p.Status != PayCreated && p.Status != PayConfirming {
		return Decision{Outcome: OutcomeFailedIgnored, Detail: "payment is " + p.Status}
	}
	np := p
	np.Status = PayFailed
	// A join's subscription stays pending_payment until it is replaced or
	// swept; nothing else changes.
	return Decision{Outcome: OutcomeMarkedFailed, Payment: &np, Notify: []string{EventPaymentFailed}}
}

func decideRefunded(p Payment, s Sub, ev Event, now time.Time) Decision {
	if p.Status != PayPaid {
		return Decision{Outcome: OutcomeRefundIgnored, Detail: "payment is " + p.Status}
	}
	remaining := p.AmountPaise - p.RefundedPaise
	if err := paymentevents.CheckRefund(
		paymentevents.Expected{AmountMinor: remaining, IntentID: p.IntentID},
		paymentevents.Observed{AmountMinor: ev.AmountMinor, IntentID: ev.IntentID},
	); err != nil {
		return mismatch(err)
	}
	np := p
	np.RefundedPaise += ev.AmountMinor
	d := Decision{Payment: &np}
	if !p.LateCapture && p.AmountPaise > 0 {
		// The creator's share of this refund comes back out of earnings.
		d.ReverseCreatorPaise = ev.AmountMinor * p.CreatorSharePaise / p.AmountPaise
	}
	full := ev.Status == "refunded" || np.RefundedPaise >= p.AmountPaise
	if !full {
		d.Outcome = OutcomePartiallyRefunded
		return d
	}
	np.Status = PayRefunded
	np.RefundStatus = RefundRefunded
	d.Outcome = OutcomeRefunded
	if p.LateCapture {
		return d
	}
	d.ReverseEarningRow = true

	// The period it bought is removed.
	ns := s
	switch p.Kind {
	case KindJoin, KindRenewal:
		if p.GrantedStart == nil || p.GrantedEnd == nil || !IsLive(s.Status) {
			return d
		}
		bought := p.GrantedEnd.Sub(*p.GrantedStart)
		newEnd := Truncate(s.CurrentPeriodEnd.Add(-bought))
		ns.CurrentPeriodEnd = newEnd
		if ns.CurrentPeriodStart.After(newEnd) {
			ns.CurrentPeriodStart = Truncate(newEnd.AddDate(0, -1, 0))
		}
		if !now.Before(newEnd) {
			// It was the current period: access ends now.
			ns.Status = StatusExpired
			ns.GracePeriodEnd = nil
			ns.PendingTierID, ns.PendingPricePaise = nil, nil
			if s.Counted {
				ns.Counted = false
				d.Tiers = []TierDelta{{s.TierID, -1}}
			}
			d.Notify = []string{EventEnded}
			d.Entitlement = EntitlementRevoked
		} else {
			d.Entitlement = EntitlementShortened
		}
		d.Sub = &ns
	case KindUpgrade:
		if p.FromTierID == nil || s.TierID != p.TierID || !IsLive(s.Status) {
			return d
		}
		ns.TierID, ns.TierName, ns.PricePaise = *p.FromTierID, p.FromTierName, p.FromPricePaise
		if s.Counted {
			d.Tiers = []TierDelta{{p.TierID, -1}, {*p.FromTierID, +1}}
		}
		d.Entitlement = EntitlementDowngraded
		d.Sub = &ns
	}
	return d
}

func decideRefundFailed(p Payment, ev Event) Decision {
	if p.RefundStatus != RefundRequested && p.RefundStatus != RefundPending {
		return Decision{Outcome: OutcomeRefundIgnored, Detail: "no refund in flight"}
	}
	np := p
	np.RefundStatus = RefundFailed
	np.RefundError = ev.ReasonCode
	return Decision{Outcome: OutcomeRefundFailed, Detail: ev.ReasonCode, Payment: &np}
}

// Applied is what a store reports back after committing one event.
type Applied struct {
	Decision Decision
	// Payment and Sub are the rows after the effect (zero when not found).
	Payment Payment
	Sub     Sub
}
