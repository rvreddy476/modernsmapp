package prokyc

import (
	"reflect"
	"testing"
)

func complete() Facts {
	return Facts{DisplayName: "Ravi", HasPhoto: true, AadhaarVerified: true, SelfieMatched: true, VerifiedSkills: 1,
		HasServiceArea: true, HasWeeklyHours: true, HasPayoutAccount: true, BackgroundClear: true,
		AgreementVersion: AgreementVersion}
}

func TestMissingStepsOrderAndEmpty(t *testing.T) {
	if got := MissingSteps(complete()); got == nil || len(got) != 0 {
		t.Fatalf("complete professional: missing %v, want empty non-nil", got)
	}
	got := MissingSteps(Facts{})
	if !reflect.DeepEqual(got, RequiredSteps) {
		t.Fatalf("empty facts: %v, want every required step in order %v", got, RequiredSteps)
	}
	// PAN is recommended, never required.
	for _, s := range got {
		if s == StepPAN {
			t.Fatal("pan listed as required")
		}
	}
	if r := RecommendedSteps(complete()); len(r) != 1 || r[0] != StepPAN {
		t.Fatalf("recommended %v", r)
	}
	f := complete()
	f.HasPAN = true
	if r := RecommendedSteps(f); len(r) != 0 {
		t.Fatalf("recommended with PAN %v", r)
	}
	if c := CompletedSteps(f); len(c) != len(RequiredSteps)+1 {
		t.Fatalf("completed %v", c)
	}
}

// Each fact gates exactly its step.
func TestEachStepGate(t *testing.T) {
	cases := map[Step]func(*Facts){
		StepProfile:           func(f *Facts) { f.HasPhoto = false },
		StepAadhaar:           func(f *Facts) { f.AadhaarVerified = false },
		StepSelfie:            func(f *Facts) { f.SelfieMatched = false },
		StepSkills:            func(f *Facts) { f.VerifiedSkills = 0; f.SkillsAwaitingReview = 3 },
		StepServiceArea:       func(f *Facts) { f.HasServiceArea = false },
		StepWeeklyHours:       func(f *Facts) { f.HasWeeklyHours = false },
		StepBank:              func(f *Facts) { f.HasPayoutAccount = false },
		StepPoliceCertificate: func(f *Facts) { f.BackgroundClear = false; f.PoliceCertificatePending = true },
		StepAgreement:         func(f *Facts) { f.AgreementVersion = "2025-01-01" },
	}
	for step, mutate := range cases {
		f := complete()
		mutate(&f)
		if got := MissingSteps(f); len(got) != 1 || got[0] != step {
			t.Errorf("%s: missing %v", step, got)
		}
	}
	f := complete()
	f.DisplayName = "   "
	if got := MissingSteps(f); len(got) != 1 || got[0] != StepProfile {
		t.Errorf("blank name: %v", got)
	}
}

func TestAwaitingReviewOnly(t *testing.T) {
	f := complete()
	f.BackgroundClear, f.PoliceCertificatePending = false, true
	f.VerifiedSkills, f.SkillsAwaitingReview = 0, 1
	if !AwaitingReviewOnly(f) {
		t.Fatal("only reviews left: want true")
	}
	f.PoliceCertificatePending = false
	if AwaitingReviewOnly(f) {
		t.Fatal("no police certificate uploaded: want false")
	}
	f = complete()
	f.SelfieMatched = false
	if AwaitingReviewOnly(f) {
		t.Fatal("selfie missing: want false")
	}
	if !AwaitingReviewOnly(complete()) {
		t.Fatal("nothing missing counts as ready for review")
	}
}

func TestGenderAndSalonRules(t *testing.T) {
	for _, c := range []struct {
		rule, gender string
		ok           bool
	}{
		{GenderAny, "", true}, {GenderAny, "male", true},
		{GenderFemaleOnly, "female", true}, {GenderFemaleOnly, "male", false}, {GenderFemaleOnly, "other", false}, {GenderFemaleOnly, "", false},
		{GenderMaleOnly, "male", true}, {GenderMaleOnly, "female", false}, {GenderMaleOnly, "", false},
		{"unknown_rule", "female", false},
	} {
		if got := GenderAllowed(c.rule, c.gender); got != c.ok {
			t.Errorf("GenderAllowed(%s,%q)=%v", c.rule, c.gender, got)
		}
	}
	women := SkillCategory{Skill: "salon_women", Family: FamilySalon, GenderRule: GenderFemaleOnly}
	men := SkillCategory{Skill: "salon_men", Family: FamilySalon, GenderRule: GenderMaleOnly}
	clean := SkillCategory{Skill: "deep_cleaning", Family: "HOME_CLEANING", GenderRule: GenderAny}
	if b := ApprovalBlocks([]SkillCategory{women, clean}, "female", true); len(b) != 0 {
		t.Fatalf("woman, salon_women, clear: %v", b)
	}
	if b := ApprovalBlocks([]SkillCategory{women}, "male", true); len(b) != 1 || b[0] != (Block{"salon_women", BlockGender}) {
		t.Fatalf("man with women's salon: %v", b)
	}
	if b := ApprovalBlocks([]SkillCategory{men}, "female", true); len(b) != 1 || b[0].Reason != BlockGender {
		t.Fatalf("woman with men's salon: %v", b)
	}
	if b := ApprovalBlocks([]SkillCategory{women}, "female", false); len(b) != 1 || b[0] != (Block{"salon_women", BlockBackgroundCheck}) {
		t.Fatalf("salon without background check: %v", b)
	}
	if b := ApprovalBlocks([]SkillCategory{clean}, "", false); len(b) != 0 {
		t.Fatalf("non-salon without check is not a salon block: %v", b)
	}
	if !GenderBlocked([]SkillCategory{women}, "male") || GenderBlocked([]SkillCategory{women}, "") || GenderBlocked([]SkillCategory{women}, "female") {
		t.Fatal("GenderBlocked")
	}
}

func TestValidateHours(t *testing.T) {
	ok, err := ValidateHours([]Window{
		{1, "14:00", "18:00"}, {1, "09:00", "13:00"}, {0, "10:00", "12:00"}, {1, "13:00", "14:00"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Window{{0, "10:00", "12:00"}, {1, "09:00", "13:00"}, {1, "13:00", "14:00"}, {1, "14:00", "18:00"}}
	if !reflect.DeepEqual(ok, want) {
		t.Fatalf("sorted %v", ok)
	}
	for name, ws := range map[string][]Window{
		"weekday 7":        {{7, "09:00", "10:00"}},
		"bad time":         {{1, "9:00", "10:00"}},
		"24:00":            {{1, "09:00", "24:00"}},
		"end before start": {{1, "18:00", "09:00"}},
		"too short":        {{1, "09:00", "09:15"}},
		"overlap":          {{2, "09:00", "12:00"}, {2, "11:00", "13:00"}},
		"too many":         {{3, "06:00", "07:00"}, {3, "07:00", "08:00"}, {3, "08:00", "09:00"}, {3, "09:00", "10:00"}, {3, "10:00", "11:00"}, {3, "11:00", "12:00"}, {3, "12:00", "13:00"}},
	} {
		if _, err := ValidateHours(ws); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if out, err := ValidateHours(nil); err != nil || len(out) != 0 {
		t.Fatalf("empty: %v %v", out, err)
	}
}
