package prokyc

import "testing"

func TestPoliceClearanceNotRequiredForWork(t *testing.T) {
	f := complete()
	f.BackgroundClear = false
	f.PoliceCertificatePending = false
	if missing := WorkMissingSteps(f); len(missing) != 0 {
		t.Fatalf("historical police clearance blocked work: %v", missing)
	}
	f.HasServiceArea = false
	if missing := WorkMissingSteps(f); len(missing) != 1 || missing[0] != StepServiceArea {
		t.Fatalf("coverage guard must remain: %v", missing)
	}
	f.IdentityReviewed = false
	if len(WorkMissingSteps(f)) != 2 {
		t.Fatal("identity guard must remain")
	}
}
