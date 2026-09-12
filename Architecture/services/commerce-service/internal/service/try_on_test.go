package service

// The try-on descriptor's two pure guards: the shape a seller may publish,
// and the category fence over which kinds a product may claim.
//
// Both are pure, so they are proven here without a database. The fence is the
// guard that matters: without it a lipstick can be published as eyewear, and
// the eyewear effect tracks a nose bridge — the viewer would see a pair of
// frames drawn across their mouth and conclude the platform is broken.

import (
	"errors"
	"strings"
	"testing"

	"github.com/atpost/commerce-service/internal/store/postgres"
)

func lookHex(id, label, hex string) postgres.TryOnVariant {
	return postgres.TryOnVariant{ID: id, Label: label, Hex: hex}
}

func TestTryOnKindsForCategoryRoot(t *testing.T) {
	cases := []struct {
		root string
		want []string
	}{
		{"beauty-and-personal-care", []string{TryOnMakeup}},
		{"jewellery-and-watches", []string{TryOnJewellery, TryOnWatch}},
		{"fashion", []string{TryOnEyewear}},
		// Roots that admit nothing. Every one of these is a seeded root from
		// migration 023, and nothing in any of them goes on a face.
		{"electronics", nil},
		{"grocery-and-gourmet", nil},
		{"books-and-stationery", nil},
		{"automotive", nil},
		{"home-and-kitchen", nil},
		{"toys-and-baby", nil},
		{"sports-and-fitness", nil},
		{"handicrafts-and-decor", nil},
		{"health-and-wellness", nil},
		// A root added after this map was written must fail closed.
		{"a-category-invented-tomorrow", nil},
		{"", nil},
	}
	for _, tc := range cases {
		got := TryOnKindsForCategoryRoot(tc.root)
		if len(got) != len(tc.want) {
			t.Errorf("root %q admits %v; want %v", tc.root, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("root %q admits %v; want %v", tc.root, got, tc.want)
				break
			}
		}
	}
}

// The returned slice must be a copy. A caller that writes into it would
// otherwise rewrite the fence for the life of the process.
//
// The write is an in-place element assignment, not an append: appending to a
// slice whose backing array is already full allocates a fresh one and so
// cannot detect aliasing at all. An earlier version of this test appended,
// passed with the copy deliberately removed, and proved nothing.
func TestTryOnKindsSliceIsNotTheFenceItself(t *testing.T) {
	got := TryOnKindsForCategoryRoot("fashion")
	if len(got) == 0 {
		t.Fatal("fashion must admit at least one kind for this test to mean anything")
	}
	got[0] = TryOnMakeup
	if TryOnKindAllowed("fashion", TryOnMakeup) {
		t.Fatal("writing into the returned slice widened the fence; it must be a copy")
	}
	if !TryOnKindAllowed("fashion", TryOnEyewear) {
		t.Fatal("writing into the returned slice narrowed the fence; it must be a copy")
	}
}

func TestTryOnKindAllowed(t *testing.T) {
	cases := []struct {
		root, kind string
		want       bool
	}{
		{"beauty-and-personal-care", TryOnMakeup, true},
		{"jewellery-and-watches", TryOnWatch, true},
		{"jewellery-and-watches", TryOnJewellery, true},
		{"fashion", TryOnEyewear, true},
		// The defect the fence exists for.
		{"beauty-and-personal-care", TryOnEyewear, false},
		{"fashion", TryOnMakeup, false},
		{"jewellery-and-watches", TryOnMakeup, false},
		{"electronics", TryOnMakeup, false},
		// Case and padding must not open it.
		{"  Fashion  ", TryOnEyewear, true},
		{"fashion", "EYEWEAR", false},
	}
	for _, tc := range cases {
		if got := TryOnKindAllowed(tc.root, tc.kind); got != tc.want {
			t.Errorf("TryOnKindAllowed(%q, %q) = %v; want %v", tc.root, tc.kind, got, tc.want)
		}
	}
}

func TestValidateTryOnDescriptorAcceptsAndNormalises(t *testing.T) {
	d, err := ValidateTryOnDescriptor("  MAKEUP  ", "  Ruby_Woo-01  ", []postgres.TryOnVariant{
		{ID: " v1 ", Label: "  Ruby  ", Hex: " #AA1133 "},
		{ID: "v2", Label: "Coral", JS: " setShade('coral') "},
	})
	if err != nil {
		t.Fatalf("a well-formed descriptor was refused: %v", err)
	}
	if !d.Capable {
		t.Error("a stored descriptor must read capable on the wire")
	}
	if d.Kind != TryOnMakeup {
		t.Errorf("kind = %q; want %q", d.Kind, TryOnMakeup)
	}
	if d.EffectSlug != "ruby_woo-01" {
		t.Errorf("effect_slug = %q; want it lowercased and trimmed", d.EffectSlug)
	}
	if len(d.Variants) != 2 {
		t.Fatalf("kept %d variants; want 2", len(d.Variants))
	}
	if d.Variants[0].ID != "v1" || d.Variants[0].Label != "Ruby" || d.Variants[0].Hex != "#AA1133" {
		t.Errorf("variant 1 was not trimmed: %+v", d.Variants[0])
	}
	if d.Variants[1].JS != "setShade('coral')" {
		t.Errorf("variant 2 js was not trimmed: %q", d.Variants[1].JS)
	}
}

// A descriptor with no variants is legitimate: an eyewear frame is one model
// with no colourway to choose.
func TestValidateTryOnDescriptorAllowsNoVariants(t *testing.T) {
	d, err := ValidateTryOnDescriptor(TryOnEyewear, "aviator-classic", nil)
	if err != nil {
		t.Fatalf("a single-look descriptor was refused: %v", err)
	}
	if d.Variants == nil {
		t.Error("variants must serialise as [] rather than null")
	}
}

func TestValidateTryOnDescriptorRejects(t *testing.T) {
	many := make([]postgres.TryOnVariant, 0, MaxTryOnVariants+1)
	for i := 0; i <= MaxTryOnVariants; i++ {
		many = append(many, lookHex(string(rune('a'+i)), "Look", "#112233"))
	}
	cases := []struct {
		name       string
		kind, slug string
		variants   []postgres.TryOnVariant
		wantIn     string
	}{
		{"an unknown kind", "hairstyle", "x", nil, "kind must be one of"},
		{"an empty kind", "", "x", nil, "kind must be one of"},
		{"an empty slug", TryOnMakeup, "  ", nil, "effect_slug"},
		{"a slug with a space", TryOnMakeup, "ruby woo", nil, "effect_slug"},
		{"a slug with punctuation", TryOnMakeup, "ruby.woo", nil, "effect_slug"},
		{"a slug starting with a hyphen", TryOnMakeup, "-ruby", nil, "effect_slug"},
		{"an over-long slug", TryOnMakeup, strings.Repeat("a", 65), nil, "effect_slug"},
		{"too many variants", TryOnMakeup, "x", many, "at most"},
		{"a variant with no id", TryOnMakeup, "x",
			[]postgres.TryOnVariant{{Label: "Ruby", Hex: "#112233"}}, "no id"},
		{"a variant with no label", TryOnMakeup, "x",
			[]postgres.TryOnVariant{{ID: "v1", Hex: "#112233"}}, "no label"},
		{"a duplicate variant id", TryOnMakeup, "x",
			[]postgres.TryOnVariant{lookHex("v1", "Ruby", "#112233"), lookHex("v1", "Coral", "#332211")},
			"appears twice"},
		{"a malformed hex", TryOnMakeup, "x",
			[]postgres.TryOnVariant{lookHex("v1", "Ruby", "AA1133")}, "hex must be"},
		{"a three-digit hex", TryOnMakeup, "x",
			[]postgres.TryOnVariant{lookHex("v1", "Ruby", "#A13")}, "hex must be"},
		{"a variant that drives nothing", TryOnMakeup, "x",
			[]postgres.TryOnVariant{{ID: "v1", Label: "Ruby"}}, "needs a hex or a js call"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateTryOnDescriptor(tc.kind, tc.slug, tc.variants)
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !errors.Is(err, ErrTryOnInvalid) {
				t.Errorf("error does not wrap ErrTryOnInvalid: %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("message %q does not say why (wanted %q)", err.Error(), tc.wantIn)
			}
		})
	}
}
