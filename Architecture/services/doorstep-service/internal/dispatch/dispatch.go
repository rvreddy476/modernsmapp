// Package dispatch holds the pure rules of offers and reassignment (A4): how
// long an offer stays open, the worker deadlines around a slot, the penalty
// a professional pays for giving a job back or not turning up, the earning
// shown on an offer, and how an assignment row reads as an offer. No I/O;
// the service applies these to the rows the store locks.
//
// Every number here is a placeholder the founder confirms (plan: "Founder
// and adviser items" 3); they live in one place so changing one is one line.
package dispatch

import (
	"time"
)

// Worker deadlines, measured from the booking's slot start.
const (
	// AlertBefore: still unassigned two hours out -> ops alert.
	AlertBefore = 2 * time.Hour
	// CancelBefore: still unassigned 45 minutes out -> cancelled by the
	// system with a full refund.
	CancelBefore = 45 * time.Minute
	// DutyBefore: the assigned professional is not on duty 90 minutes out
	// -> the job is reassigned. DutyGrace gives a professional who accepted
	// late that long to go on duty first.
	DutyBefore = 90 * time.Minute
	DutyGrace  = 15 * time.Minute
	// LateAfter: not arrived 15 minutes after the slot -> the customer is
	// told and may cancel free of charge.
	LateAfter = 15 * time.Minute
	// NoShowAfter: not arrived 30 minutes after the slot -> professional
	// no-show; reassigned when someone can start within RescueWithin,
	// else the booking ends with a full refund.
	NoShowAfter  = 30 * time.Minute
	RescueWithin = 60 * time.Minute
	// RescueWindow is how long a last-minute reassignment may look for a
	// professional who accepts before the booking ends with a full refund.
	RescueWindow = 30 * time.Minute
	// StaleFix: an on-duty professional with no location fix for this long
	// is taken off duty.
	StaleFix = 5 * time.Minute
	// RetryEvery: a confirmed booking with no live offer (dispatch found
	// nobody, or a crash between the confirm and the offer) is dispatched
	// again at most this often.
	RetryEvery = 2 * time.Minute
)

// Windows is a city's offer configuration (doorstep.cities).
type Windows struct {
	FarMinutes          int // offer_window_far_minutes (120)
	NearMinutes         int // offer_window_near_minutes (10)
	FarThresholdMinutes int // offer_far_threshold_minutes (720)
}

// DefaultWindows are the schema defaults.
var DefaultWindows = Windows{FarMinutes: 120, NearMinutes: 10, FarThresholdMinutes: 720}

// OfferExpiry is when an offer made at now lapses: the far window (2 h)
// when the slot starts more than the threshold (12 h) after now, else the
// near window (10 min); never after a rescue deadline.
func OfferExpiry(now, slotStart time.Time, w Windows, rescueUntil *time.Time) time.Time {
	if w.FarMinutes <= 0 || w.NearMinutes <= 0 || w.FarThresholdMinutes <= 0 {
		w = DefaultWindows
	}
	ttl := time.Duration(w.NearMinutes) * time.Minute
	if slotStart.Sub(now) > time.Duration(w.FarThresholdMinutes)*time.Minute {
		ttl = time.Duration(w.FarMinutes) * time.Minute
	}
	exp := now.Add(ttl)
	if rescueUntil != nil && exp.After(*rescueUntil) && rescueUntil.After(now) {
		exp = *rescueUntil
	}
	return exp
}

// RescueStart is the earliest start a replacement professional could make
// for a job that needs someone now: now plus the zone's travel buffer,
// rounded up to five minutes. ok is false when that is later than
// RescueWithin from now.
func RescueStart(now time.Time, bufferMinutes int) (time.Time, bool) {
	start := now.Add(time.Duration(bufferMinutes) * time.Minute)
	if r := start.Truncate(5 * time.Minute); r.Before(start) {
		start = r.Add(5 * time.Minute)
	}
	return start, !start.After(now.Add(RescueWithin))
}

// NeedsRescue reports whether a booking going back to confirmed at now must
// be rescued (its slot is inside the unassigned-cancel deadline).
func NeedsRescue(now, slotStart time.Time) bool {
	return slotStart.Sub(now) < CancelBefore
}

// Penalty tiers for a professional who gives back a job they accepted
// (placeholders, paise). A job given back more than a day ahead costs
// nothing but still counts against acceptance.
const (
	PenaltyFreeBefore   = 24 * time.Hour
	PenaltyLowBefore    = 3 * time.Hour
	PenaltyLowPaise     = 10000 // 3-24 h before the slot
	PenaltyHighPaise    = 20000 // under 3 h before the slot
	PenaltyEnRoutePaise = 30000 // already travelling
	PenaltyNoShowPaise  = 30000 // never arrived
)

// Penalty tiers (earning line reasons).
const (
	TierFree    = "free"
	TierLow     = "under_24h"
	TierHigh    = "under_3h"
	TierEnRoute = "en_route"
	TierNoShow  = "no_show"
)

// CancelPenalty is what a professional pays for giving back an accepted
// job at now: by how far away the slot is, or more once travelling.
func CancelPenalty(now, slotStart time.Time, status string) (paise int64, tier string) {
	if status == "en_route" || status == "arrived" {
		return PenaltyEnRoutePaise, TierEnRoute
	}
	left := slotStart.Sub(now)
	switch {
	case left >= PenaltyFreeBefore:
		return 0, TierFree
	case left >= PenaltyLowBefore:
		return PenaltyLowPaise, TierLow
	default:
		return PenaltyHighPaise, TierHigh
	}
}

// EarningEstimate is the professional's share of a job shown on an offer:
// the taxable value less the platform commission (basis points). Settlement
// (A5) recomputes it at completion.
func EarningEstimate(taxablePaise int64, commissionBPS int) int64 {
	if commissionBPS < 0 {
		commissionBPS = 0
	}
	if commissionBPS > 10000 {
		commissionBPS = 10000
	}
	return taxablePaise - taxablePaise*int64(commissionBPS)/10000
}

// Offer statuses as a professional reads them (Offer.status).
const (
	OfferOpen      = "open"
	OfferAccepted  = "accepted"
	OfferDeclined  = "declined"
	OfferExpired   = "expired"
	OfferWithdrawn = "withdrawn"
)

// OfferStatus maps an assignment row to the offer status: an offer past its
// expiry reads expired before the worker gets to it.
func OfferStatus(assignmentStatus string, expiresAt, now time.Time) string {
	switch assignmentStatus {
	case "offered":
		if !now.Before(expiresAt) {
			return OfferExpired
		}
		return OfferOpen
	case "accepted", "completed":
		return OfferAccepted
	case "declined":
		return OfferDeclined
	case "expired":
		return OfferExpired
	default: // released, cancelled, no_show
		return OfferWithdrawn
	}
}

// AddressWindow is how long after completion the professional still sees
// the address and the chat.
const AddressWindow = 2 * time.Hour

// AddressVisible reports whether a professional with an accepted (or
// completed) assignment sees the booking's full address: from acceptance
// until completion + 2 h, never on a cancelled or failed booking.
func AddressVisible(assignmentStatus, bookingStatus string, completedAt *time.Time, now time.Time) bool {
	if assignmentStatus != "accepted" && assignmentStatus != "completed" {
		return false
	}
	switch bookingStatus {
	case "assigned", "en_route", "arrived", "in_progress", "awaiting_extras_payment":
		return true
	case "completed":
		return completedAt != nil && now.Before(completedAt.Add(AddressWindow))
	}
	return false
}
