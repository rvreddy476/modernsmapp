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
