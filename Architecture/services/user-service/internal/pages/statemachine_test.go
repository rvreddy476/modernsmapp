package pages

import "testing"

func TestCanTransition_Legal(t *testing.T) {
	legal := []struct{ from, to string }{
		{StatusDraft, StatusPendingReview},
		{StatusDraft, StatusDisabled},
		{StatusPendingReview, StatusApproved},
		{StatusPendingReview, StatusRejected},
		{StatusPendingReview, StatusDisabled},
		{StatusRejected, StatusDraft},
		{StatusRejected, StatusDisabled},
		{StatusApproved, StatusSuspended},
		{StatusApproved, StatusDisabled},
		{StatusSuspended, StatusApproved},
		{StatusSuspended, StatusDisabled},
	}
	for _, tc := range legal {
		if !CanTransition(tc.from, tc.to) {
			t.Errorf("expected %s→%s to be legal", tc.from, tc.to)
		}
	}
}

func TestCanTransition_IllegalRejected(t *testing.T) {
	all := []string{
		StatusDraft, StatusPendingReview, StatusApproved,
		StatusRejected, StatusSuspended, StatusDisabled,
	}
	legalSet := map[string]map[string]bool{}
	for from, tos := range legalTransitions {
		legalSet[from] = map[string]bool{}
		for _, to := range tos {
			legalSet[from][to] = true
		}
	}
	for _, from := range all {
		for _, to := range all {
			want := legalSet[from][to]
			if got := CanTransition(from, to); got != want {
				t.Errorf("CanTransition(%s,%s)=%v want %v", from, to, got, want)
			}
		}
	}
}

func TestDisabledIsTerminal(t *testing.T) {
	for _, to := range []string{StatusDraft, StatusPendingReview, StatusApproved, StatusRejected, StatusSuspended} {
		if CanTransition(StatusDisabled, to) {
			t.Errorf("disabled must be terminal; got legal transition to %s", to)
		}
	}
	if !IsTerminal(StatusDisabled) {
		t.Error("IsTerminal(disabled) should be true")
	}
	if IsTerminal(StatusApproved) {
		t.Error("IsTerminal(approved) should be false")
	}
}

func TestIsValidStatus(t *testing.T) {
	if !IsValidStatus(StatusApproved) {
		t.Error("approved should be a valid status")
	}
	if IsValidStatus("bogus") {
		t.Error("bogus must not be a valid status")
	}
}
