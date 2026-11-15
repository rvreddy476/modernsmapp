package verification

import "testing"

// held reports whether candidate would be held against reserved under the
// store's rule: fuzzy for an organisation or government name, exact for a
// person's.
func held(candidate, reserved string, fuzzy bool) bool {
	cand := CandidateKeys(candidate)
	res := ReservedKeys(reserved)
	if fuzzy {
		stored := map[string]bool{}
		for _, v := range FuzzyVariantsOf(res) {
			stored[v] = true
		}
		for _, v := range FuzzyVariantsOf(cand) {
			if stored[v] {
				return true
			}
		}
		return false
	}
	stored := map[string]bool{}
	for _, k := range res {
		stored[k] = true
	}
	for _, k := range cand {
		if stored[k] {
			return true
		}
	}
	return false
}

func TestImpersonation_OrganisationNamesAreHeld(t *testing.T) {
	for _, c := range []string{
		"NASA",
		"nasa",
		"N A S A",
		"NASA Official",
		"nasa_official",
		"nasaofficial",
		"The Real NASA",
		"ΝΑЅΑ", // Greek Nu, Alpha, Cyrillic Dze, Alpha
		"ＮＡＳＡ", // fullwidth
		"N4SA",
		"NAASA", // one insertion
		"NASA HQ",
		"NASA India",
	} {
		if !held(c, "NASA", true) {
			t.Errorf("%q should be held against NASA", c)
		}
	}
	for _, c := range []string{
		"Infosys", "lnfosys", "Inf0sys", "Infosys Ltd Official", "Infossys", "Ιnfosys", "Infosy",
	} {
		if !held(c, "Infosys", true) {
			t.Errorf("%q should be held against Infosys", c)
		}
	}
	for _, c := range []string{"Café Coffee Day", "Cafe Coffee Day", "CAFÉ COFFEE DAY official"} {
		if !held(c, "Cafe Coffee Day", true) {
			t.Errorf("%q should be held against Cafe Coffee Day", c)
		}
	}
	if !held("Modern", "Modem", true) { // "rn" reads as "m"
		t.Error("rn/m look-alike should be held")
	}
}

func TestImpersonation_DifferentNamesAreNotHeld(t *testing.T) {
	for _, pair := range [][2]string{
		{"Air", "Air India"},
		{"Kevin", "Kev"},
		{"Ravi Kumar", "Infosys"},
		{"NBA", "NASA"}, // short: exact only
		{"BBS", "BBC"},  // short: exact only
		{"Indigo Paints", "IndiGo"},
		{"Google Fan Art", "Google"}, // more than one edit away
	} {
		if held(pair[0], pair[1], true) {
			t.Errorf("%q must not be held against %q", pair[0], pair[1])
		}
	}
}

func TestImpersonation_PersonNamesAreExactOnly(t *testing.T) {
	for _, c := range []string{"Rahul Sharma", "rahul.sharma", "Sharma Rahul", "RAHUL  SHARMA", "Rahul Sharma Official", "Rаhul Sharma"} {
		if !held(c, "Rahul Sharma", false) {
			t.Errorf("%q should be held against the verified Rahul Sharma", c)
		}
	}
	for _, c := range []string{"Rahul Sharman", "Rahul Verma", "Rahul"} {
		if held(c, "Rahul Sharma", false) {
			t.Errorf("%q must not be held against the verified Rahul Sharma (exact only)", c)
		}
	}
}

func TestImpersonation_ShortNamesReserveNothing(t *testing.T) {
	if k := ReservedKeys("AB"); len(k) != 0 {
		t.Fatalf("a two-letter name reserved %v", k)
	}
	if k := CandidateKeys("!!"); len(k) != 0 {
		t.Fatalf("punctuation produced keys %v", k)
	}
}

func TestImpersonation_DevanagariIsKept(t *testing.T) {
	// Vowel signs are marks on a non-Latin base: kept, so two different
	// Hindi names do not collapse into one skeleton.
	if held("राम", "रोम", false) {
		t.Fatal("different Devanagari names collapsed")
	}
	if !held("राम", "राम", false) {
		t.Fatal("the same Devanagari name should match")
	}
}

func TestFuzzyVariants(t *testing.T) {
	if v := FuzzyVariants("abcd"); len(v) != 1 {
		t.Fatalf("short key fuzzed: %v", v)
	}
	if v := FuzzyVariants("abcde"); len(v) != 6 {
		t.Fatalf("FuzzyVariants(abcde) = %v", v)
	}
}

// An Official name is stored with and without its legal form, so the
// organisation's everyday name, and that name decorated, are held.
func TestReservedOrgKeys_DropOnlyTheLegalForm(t *testing.T) {
	heldOrg := func(candidate, reserved string) bool {
		stored := map[string]bool{}
		for _, v := range FuzzyVariantsOf(ReservedOrgKeys(reserved)) {
			stored[v] = true
		}
		for _, v := range FuzzyVariantsOf(CandidateKeys(candidate)) {
			if stored[v] {
				return true
			}
		}
		return false
	}
	for _, c := range []string{"Acme Private Limited Official", "Acme", "Acme Official", "ACME Pvt. Ltd.", "acme_official"} {
		if !heldOrg(c, "ACME PRIVATE LIMITED") {
			t.Errorf("%q is not held against ACME PRIVATE LIMITED", c)
		}
	}
	if heldOrg("Air", "Air India Limited") {
		t.Error(`"Air" is held against "Air India Limited": only the legal form may be dropped`)
	}
	if !heldOrg("Air India Official", "Air India Limited") {
		t.Error(`"Air India Official" is not held against "Air India Limited"`)
	}
	if got, want := ReservedOrgKeys("Co Ltd"), ReservedKeys("Co Ltd"); len(got) != len(want) {
		t.Errorf("a name that is only a legal form reserved %v, want only %v", got, want)
	}
}
