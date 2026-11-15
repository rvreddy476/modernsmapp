package store

import (
	"sort"
	"testing"

	"github.com/atpost/identity-shared/verification"
)

func TestReservedNameRows_OnlyMarkedProfilesReserve(t *testing.T) {
	if v, _ := ReservedNameRows(verification.LevelOrganisation, false, "Infosys"); len(v) != 0 {
		t.Fatalf("an unverified profile reserved %v", v)
	}
	if v, _ := ReservedNameRows("", true, "Infosys"); len(v) != 0 {
		t.Fatalf("a profile without a level reserved %v", v)
	}
}

func TestReservedNameRows_PersonExactOrganisationFuzzy(t *testing.T) {
	person, kind := ReservedNameRows(verification.LevelPerson, true, "Asha Rao", "asharao")
	if kind != verification.LevelPerson {
		t.Fatalf("kind = %q", kind)
	}
	sort.Strings(person)
	if len(person) != 1 || person[0] != "asharao" { // both names, one skeleton, no variants
		t.Fatalf("person rows = %v", person)
	}
	org, kind := ReservedNameRows(verification.LevelOrganisation, true, "Infosys")
	if kind != verification.LevelOrganisation || len(org) <= 1 {
		t.Fatalf("organisation rows = %v (%s): want the one-deletion variants too", org, kind)
	}
	gov, kind := ReservedNameRows(verification.LevelGovernment, true, "Ministry of Example Affairs")
	if kind != verification.LevelGovernment || len(gov) <= 2 {
		t.Fatalf("government rows = %v (%s)", gov, kind)
	}
}

// rowsHold is FindReservedNameHolder's SQL on the pure functions: a
// non-person row holds a candidate when one of its variants is among the
// candidate keys' fuzzy variants.
func rowsHold(rows []string, candidate string) bool {
	stored := map[string]bool{}
	for _, v := range rows {
		stored[v] = true
	}
	for _, v := range verification.FuzzyVariantsOf(verification.CandidateKeys(candidate)) {
		if stored[v] {
			return true
		}
	}
	return false
}

// The registered name of an Official mark carries its legal form ("Private
// Limited"); an impersonator drops it, adds "Official", or both. Every one of
// these must be held, as the approve integration test expects.
func TestReservedNameRows_OrganisationHeldWithoutItsLegalForm(t *testing.T) {
	rows, _ := ReservedNameRows(verification.LevelOrganisation, true, "ACME PRIVATE LIMITED")
	for _, c := range []string{
		"Acme Private Limited Official",
		"ACME PRIVATE LIMITED",
		"Acme",
		"Acme Official",
		"Acme Pvt Ltd",
		"The Real Acme",
		"acmeofficial",
	} {
		if !rowsHold(rows, c) {
			t.Errorf("%q is not held against ACME PRIVATE LIMITED", c)
		}
	}
	for _, c := range []string{"Acne Studios", "Some One"} {
		if rowsHold(rows, c) {
			t.Errorf("%q is held against ACME PRIVATE LIMITED", c)
		}
	}
}
