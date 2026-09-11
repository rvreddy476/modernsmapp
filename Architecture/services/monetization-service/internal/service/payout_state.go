package service

import (
	"context"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// The withdrawal state machine (plan Phase 4A, M-03)
// ---------------------------------------------------------------------------
//
//	requested -> reserved -> submitted -> processing -> paid | failed | reversed
//	                                                    paid -> reversed
//	requested | reserved -> held -> requested | reserved
//	reserved -> failed   (the provider refused the create outright)
//
// The table below is the ONLY statement of what may follow what. It is
// applied by store.TransitionPayoutRequest, which is
// UPDATE ... WHERE id=$1 AND status=$2 and treats zero rows as
// ErrIllegalTransition, so a row that moved under us cannot be moved
// again by a stale reader. Nothing else in the service writes
// payout_requests.status.

// The eight states. requested and held are also what Phase 3A writes.
const (
	PayoutStatusRequested  = "requested"  // passed the gates; money reserved in pending_payout
	PayoutStatusReserved   = "reserved"   // past the review window and under the auto-approve limit
	PayoutStatusSubmitted  = "submitted"  // CreatePayout returned (or an existing payout was adopted)
	PayoutStatusProcessing = "processing" // the provider has it in flight
	PayoutStatusPaid       = "paid"       // processed; UTR captured; money has left
	PayoutStatusFailed     = "failed"     // rejected, cancelled or failed; funds returned
	PayoutStatusReversed   = "reversed"   // came back from the bank; funds returned
	PayoutStatusHeld       = "held"       // waiting on a human
)

// ErrIllegalTransition is the store's: the row was not in the state the
// caller believed, or the pair is not in the table.
var ErrIllegalTransition = postgres.ErrIllegalTransition

var payoutTransitions = map[string]map[string]bool{
	PayoutStatusRequested:  set(PayoutStatusReserved, PayoutStatusHeld),
	PayoutStatusReserved:   set(PayoutStatusSubmitted, PayoutStatusHeld, PayoutStatusFailed),
	PayoutStatusSubmitted:  set(PayoutStatusProcessing, PayoutStatusPaid, PayoutStatusFailed, PayoutStatusReversed),
	PayoutStatusProcessing: set(PayoutStatusPaid, PayoutStatusFailed, PayoutStatusReversed),
	PayoutStatusPaid:       set(PayoutStatusReversed),
	PayoutStatusFailed:     set(),
	PayoutStatusReversed:   set(),
	PayoutStatusHeld:       set(PayoutStatusRequested, PayoutStatusReserved),
}

func set(states ...string) map[string]bool {
	m := make(map[string]bool, len(states))
	for _, s := range states {
		m[s] = true
	}
	return m
}

// CanTransitionPayout reports whether from -> to is in the table. A state
// that is not one of the eight is never a valid endpoint.
func CanTransitionPayout(from, to string) bool {
	next, ok := payoutTransitions[from]
	if !ok {
		return false
	}
	return next[to]
}

// AllPayoutStatuses lists the eight states, for tests and for the CHECK
// constraint's twin in code.
func AllPayoutStatuses() []string {
	return []string{
		PayoutStatusRequested, PayoutStatusReserved, PayoutStatusSubmitted, PayoutStatusProcessing,
		PayoutStatusPaid, PayoutStatusFailed, PayoutStatusReversed, PayoutStatusHeld,
	}
}

// IsTerminalPayoutStatus: nothing follows failed or reversed.
func IsTerminalPayoutStatus(s string) bool {
	return len(payoutTransitions[s]) == 0 && (s == PayoutStatusFailed || s == PayoutStatusReversed)
}

// transitionPayout applies one step of the table on db. It checks the
// table first so an unlisted pair never reaches SQL, then relies on the
// store's WHERE status=$2 for the race.
func (s *Service) transitionPayout(ctx context.Context, db postgres.DBTX, id uuid.UUID, from, to string, patch postgres.PayoutRequestPatch) error {
	if !CanTransitionPayout(from, to) {
		return ErrIllegalTransition
	}
	return s.store.TransitionPayoutRequestTx(ctx, db, id, from, to, patch)
}
