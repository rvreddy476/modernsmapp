package service

import "testing"

// The state machine (plan Phase 4A) is enforced in one place: the
// transition table here, applied by store.TransitionPayoutRequest as
// UPDATE ... WHERE id=$1 AND status=$2. Money that has left the platform
// cannot come back into flight: paid → processing is not a transition.
func TestPayoutTransitionTableRejectsPaidToProcessing(t *testing.T) {
	if CanTransitionPayout(PayoutStatusPaid, PayoutStatusProcessing) {
		t.Fatal("paid → processing is allowed; a paid payout must never go back into flight")
	}

	forward := [][2]string{
		{PayoutStatusRequested, PayoutStatusReserved},
		{PayoutStatusRequested, PayoutStatusHeld},
		{PayoutStatusReserved, PayoutStatusSubmitted},
		{PayoutStatusReserved, PayoutStatusHeld},
		{PayoutStatusReserved, PayoutStatusFailed},
		{PayoutStatusSubmitted, PayoutStatusProcessing},
		{PayoutStatusSubmitted, PayoutStatusPaid},
		{PayoutStatusSubmitted, PayoutStatusFailed},
		{PayoutStatusSubmitted, PayoutStatusReversed},
		{PayoutStatusProcessing, PayoutStatusPaid},
		{PayoutStatusProcessing, PayoutStatusFailed},
		{PayoutStatusProcessing, PayoutStatusReversed},
		{PayoutStatusPaid, PayoutStatusReversed},
		{PayoutStatusHeld, PayoutStatusRequested},
		{PayoutStatusHeld, PayoutStatusReserved},
	}
	for _, step := range forward {
		if !CanTransitionPayout(step[0], step[1]) {
			t.Errorf("%s → %s must be allowed", step[0], step[1])
		}
	}

	never := [][2]string{
		{PayoutStatusPaid, PayoutStatusProcessing},
		{PayoutStatusPaid, PayoutStatusSubmitted},
		{PayoutStatusPaid, PayoutStatusRequested},
		{PayoutStatusPaid, PayoutStatusFailed},
		{PayoutStatusFailed, PayoutStatusPaid},
		{PayoutStatusFailed, PayoutStatusReserved},
		{PayoutStatusReversed, PayoutStatusPaid},
		{PayoutStatusReversed, PayoutStatusRequested},
		{PayoutStatusProcessing, PayoutStatusSubmitted},
		{PayoutStatusProcessing, PayoutStatusReserved},
		{PayoutStatusSubmitted, PayoutStatusReserved},
		{PayoutStatusRequested, PayoutStatusPaid},
		{PayoutStatusRequested, PayoutStatusSubmitted},
		{PayoutStatusRequested, PayoutStatusRequested},
		{"", PayoutStatusPaid},
		{PayoutStatusPaid, ""},
		{"settled", PayoutStatusPaid}, // the old vocabulary is not a state
	}
	for _, step := range never {
		if CanTransitionPayout(step[0], step[1]) {
			t.Errorf("%q → %q must be refused", step[0], step[1])
		}
	}
	for _, terminal := range []string{PayoutStatusFailed, PayoutStatusReversed} {
		for _, to := range AllPayoutStatuses() {
			if CanTransitionPayout(terminal, to) {
				t.Errorf("%s is terminal but → %s is allowed", terminal, to)
			}
		}
	}
}
