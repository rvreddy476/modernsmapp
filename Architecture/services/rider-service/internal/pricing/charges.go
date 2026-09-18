package pricing

import "time"

// WaitingCharge returns the chargeable waiting minutes and the charge: whole
// minutes waited (rounded down) beyond the rule's free window, at the rule's
// per-minute rate.
func WaitingCharge(r FareRule, waitingSeconds int) (minutes int, paise int64) {
	if waitingSeconds <= 0 || r.WaitingPerMinutePaise <= 0 {
		return 0, 0
	}
	total := waitingSeconds / 60
	chargeable := total - r.WaitingFreeMinutes
	if chargeable <= 0 {
		return 0, 0
	}
	return chargeable, int64(chargeable) * r.WaitingPerMinutePaise
}

// Cancellation actors.
const (
	CancelByCustomer = "customer"
	CancelByPartner  = "partner"
	CancelByAdmin    = "admin"
	CancelBySystem   = "system"
	// CancelNoShow is a partner reporting the customer never appeared; the
	// customer owes the same fee as a late cancellation.
	CancelNoShow = "no_show"
)

// CancellationFee is what the customer owes when a ride is cancelled:
//   - the customer cancels (or is a no-show) after a partner was assigned and
//     more than the rule's free window has passed since assignment -> the
//     rule's cancellation fee;
//   - anything else (partner, admin or system cancels; no partner yet; inside
//     the free window) -> 0.
func CancellationFee(r FareRule, by string, assignedAt *time.Time, now time.Time) int64 {
	if by != CancelByCustomer && by != CancelNoShow {
		return 0
	}
	if assignedAt == nil || r.CancellationFeePaise <= 0 {
		return 0
	}
	free := time.Duration(r.CancelFreeSeconds) * time.Second
	if now.Sub(*assignedAt) <= free {
		return 0
	}
	return r.CancellationFeePaise
}
