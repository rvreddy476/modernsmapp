package pages

import "testing"

func TestPageTypes_ExactlyThirteen(t *testing.T) {
	if len(PageTypes) != 13 {
		t.Fatalf("expected exactly 13 page types, got %d", len(PageTypes))
	}
	for _, pt := range PageTypes {
		if _, ok := PageTypeConfigs[pt]; !ok {
			t.Errorf("PageTypes entry %q missing from PageTypeConfigs", pt)
		}
	}
	if len(PageTypeConfigs) != 13 {
		t.Errorf("expected 13 config entries, got %d", len(PageTypeConfigs))
	}
}

func TestIsValidPageType(t *testing.T) {
	if !IsValidPageType(PageTypeFoodPartner) {
		t.Error("food_partner should be valid")
	}
	if IsValidPageType("dragon_tamer") {
		t.Error("unknown type must be rejected")
	}
	if IsValidPageType("") {
		t.Error("empty type must be rejected")
	}
}

func TestIsValidDocumentType(t *testing.T) {
	for _, ok := range []string{"identity_proof", "fssai_license", "other"} {
		if !IsValidDocumentType(ok) {
			t.Errorf("%s should be a valid document type", ok)
		}
	}
	if IsValidDocumentType("blood_sample") {
		t.Error("unknown document type must be rejected")
	}
}

func TestDisplayType(t *testing.T) {
	cases := map[string]string{
		PageTypeBusiness:      "Business Page",
		PageTypeFoundationNGO: "Foundation / NGO Page",
		"unmapped_value":      "Unmapped Value", // fallback title-case
	}
	for in, want := range cases {
		if got := DisplayType(in); got != want {
			t.Errorf("DisplayType(%q)=%q want %q", in, got, want)
		}
	}
}

func TestRequiredDocs(t *testing.T) {
	// food_partner requires identity_proof (spec §8).
	got := RequiredDocs(PageTypeFoodPartner)
	if len(got) != 1 || got[0] != "identity_proof" {
		t.Errorf("food_partner required docs = %v, want [identity_proof]", got)
	}
	// community_organization requires none.
	if len(RequiredDocs(PageTypeCommunityOrganization)) != 0 {
		t.Error("community_organization should require no documents")
	}
	// unknown type → nil.
	if RequiredDocs("nope") != nil {
		t.Error("unknown type should return nil required docs")
	}
}

func TestGatedButtons(t *testing.T) {
	for _, g := range []string{"follow", "unfollow", "message"} {
		if !IsGatedButton(g) {
			t.Errorf("%s should be gated", g)
		}
	}
	for _, hint := range []string{"donate", "shop", "call", "order_food", "menu"} {
		if IsGatedButton(hint) {
			t.Errorf("%s should be a hint, not gated", hint)
		}
	}
}
