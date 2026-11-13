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
