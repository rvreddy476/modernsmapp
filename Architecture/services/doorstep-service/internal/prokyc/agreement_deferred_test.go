package prokyc

import "testing"

func TestDeferredAgreementDoesNotBlockOrManufactureConsent(t *testing.T) {
	for _, version := range []string{"", "2025-01-01", AgreementVersion} {
		f := complete()
		f.AgreementVersion = version
		if missing := WorkMissingSteps(f); len(missing) != 0 {
			t.Fatalf("deferred agreement blocks setup: %v", missing)
		}
		for _, step := range CompletedSteps(f) {
			if step == StepAgreement {
				t.Fatal("deferred agreement must not appear as newly completed")
			}
		}
		if f.AgreementVersion != version {
			t.Fatal("readiness changed historical acceptance")
		}
		f.HasPayoutAccount = false
		if missing := WorkMissingSteps(f); len(missing) != 1 || missing[0] != StepBank {
			t.Fatalf("bank review must remain required: %v", missing)
		}
	}
}
