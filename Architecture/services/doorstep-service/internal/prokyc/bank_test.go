package prokyc

import "testing"

func TestSimpleApplicationRequiresReviewedSelfie(t *testing.T) {
	f := Facts{SimpleApplication: true, IdentityReviewed: true, VerifiedSkills: 1, HasPAN: true, HasPayoutAccount: true}
	if got := MissingSteps(f); len(got) != 1 || got[0] != StepSelfie {
		t.Fatalf("missing selfie bypassed: %v", got)
	}
	f.SelfieMatched = true
	if got := MissingSteps(f); len(got) != 0 {
		t.Fatalf("optional introduction became required: %v", got)
	}
}

func TestApplicationNeedsPANAndReviewedBank(t *testing.T) {
	f := Facts{SimpleApplication: true, SelfieMatched: true, IdentityReviewed: true, VerifiedSkills: 1, HasPAN: true, HasPayoutAccount: true}
	if len(MissingSteps(f)) != 0 {
		t.Fatal("complete application refused")
	}
	f.HasPayoutAccount = false
	f.BankAwaitingReview = true
	if len(MissingSteps(f)) != 1 || MissingSteps(f)[0] != StepBank {
		t.Fatal("pending bank treated as approved")
	}
	if !AwaitingReviewOnly(f) {
		t.Fatal("pending bank not queued for review")
	}
	f.HasPayoutAccount = true
	f.HasPAN = false
	if len(MissingSteps(f)) != 1 || MissingSteps(f)[0] != StepPAN {
		t.Fatal("missing PAN accepted")
	}
}
