package postgres

// Where `try_on` sits in the JSON, pinned.
//
// This exists because getting it wrong produced the worst kind of failure.
// The descriptor was first returned as a SIBLING of `product`, alongside
// `media` and `attributes`. The Android client reads `product.try_on`, so it
// found nothing, concluded the product was not try-on capable, and hid the
// Try-on control — on a product that was fully set up, with no error in any
// log, on either side. "No descriptor" and "descriptor in the wrong place"
// are indistinguishable to a client, so nothing could report it.
//
// Every assertion below is about the shape a shipped client decodes. A field
// rename or a move back out to the wrapper breaks these rather than breaking
// a device.

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestTryOnIsNestedInsideTheProductObject(t *testing.T) {
	p := &Product{
		ID:    uuid.New(),
		Title: "Velvet Matte Lipstick",
		TryOn: &ProductTryOn{
			Capable: true, Kind: "makeup", EffectSlug: "momentum_lipstick",
			Variants: []TryOnVariant{{ID: "v1", Label: "Ruby Rush", Hex: "#B11226"}},
		},
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// The client reads product.try_on. If this key is not on the product
	// object, the Try-on control is hidden and nothing says why.
	inner, ok := decoded["try_on"]
	if !ok {
		t.Fatalf("`try_on` is not a key of the product object. The client reads "+
			"product.try_on; as a sibling of `product` it is invisible. Keys present: %v",
			keysOf(decoded))
	}

	var d ProductTryOn
	if err := json.Unmarshal(inner, &d); err != nil {
		t.Fatalf("the nested descriptor does not decode: %v", err)
	}
	if !d.Capable {
		t.Error("`capable` must survive the round trip; it is what the client switches on")
	}
	if d.Kind != "makeup" || d.EffectSlug != "momentum_lipstick" {
		t.Errorf("kind/effect_slug did not survive: %+v", d)
	}
	if len(d.Variants) != 1 || d.Variants[0].Hex != "#B11226" {
		t.Errorf("variants did not survive: %+v", d.Variants)
	}
}

// A product with no descriptor must OMIT the key entirely. The client treats
// absence as "not capable", so emitting `"try_on": null` would be decoded by
// a stricter client as a present-but-broken field.
func TestTryOnIsOmittedWhenThereIsNoDescriptor(t *testing.T) {
	raw, err := json.Marshal(&Product{ID: uuid.New(), Title: "Heritage Linen Kurta"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := decoded["try_on"]; present {
		t.Error("`try_on` must be absent, not null, on a product with no descriptor")
	}
}

// The wire names the client decodes, spelled out once so a rename is loud.
func TestTryOnWireFieldNames(t *testing.T) {
	raw, err := json.Marshal(&ProductTryOn{
		Capable: true, Kind: "makeup", EffectSlug: "s",
		Variants: []TryOnVariant{{ID: "v1", Label: "L", Hex: "#AABBCC", JS: "f()"}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"capable", "kind", "effect_slug", "variants"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("the descriptor is missing the wire key %q; keys: %v", key, keysOfAny(decoded))
		}
	}
	variants, _ := decoded["variants"].([]any)
	if len(variants) != 1 {
		t.Fatalf("variants did not serialise as a one-element array: %v", decoded["variants"])
	}
	v, _ := variants[0].(map[string]any)
	for _, key := range []string{"id", "label", "hex", "js"} {
		if _, ok := v[key]; !ok {
			t.Errorf("a variant is missing the wire key %q; keys: %v", key, keysOfAny(v))
		}
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func keysOfAny(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
