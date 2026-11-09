package postgres

import (
	"errors"
	"testing"

	"github.com/atpost/shared/partnerwork"
)

// Food server lane (9 Nov 2026): going offline is refused only for a rider
// holding a job, and only while partnerwork is off (partnerwork refuses
// itself when on). Going online is never refused by this rule.
func TestOfflineRefusedOnJob(t *testing.T) {
	for _, tc := range []struct {
		online, partnerworkOn, onJob, want bool
	}{
		{online: false, partnerworkOn: false, onJob: true, want: true},
		{online: false, partnerworkOn: false, onJob: false, want: false},
		{online: false, partnerworkOn: true, onJob: true, want: false},
		{online: true, partnerworkOn: false, onJob: true, want: false},
		{online: true, partnerworkOn: true, onJob: false, want: false},
	} {
		if got := offlineRefusedOnJob(tc.online, tc.partnerworkOn, tc.onJob); got != tc.want {
			t.Fatalf("online=%v partnerwork=%v onJob=%v: refused=%v, want %v", tc.online, tc.partnerworkOn, tc.onJob, got, tc.want)
		}
	}
}

// The refusal is partnerwork's BUSY, so both paths answer 409 WORK_BUSY.
func TestErrRiderOnActiveJobIsWorkBusy(t *testing.T) {
	if !errors.Is(ErrRiderOnActiveJob, partnerwork.ErrBusy) || partnerwork.ErrorCode(ErrRiderOnActiveJob) != "BUSY" {
		t.Fatalf("ErrRiderOnActiveJob does not read as BUSY: %v", ErrRiderOnActiveJob)
	}
}
