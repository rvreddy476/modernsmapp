package postgres

// The discount the client is NOT allowed to compute, and the fields the
// product summary must actually put on the wire.
//
// `discount_pct` is derived in MarshalJSON rather than stored on the struct,
// so the only honest way to test it is through the JSON a client receives —
// which is also the layer where the previous generation of image bugs lived
// (the field existed on the model and never reached the wire).

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func minor(v int64) *int64 { return &v }

func TestDiscountMaths(t *testing.T) {
	cases := []struct {
		name       string
		price, mrp *int64
		want       *int
	}{
		{"a quarter off", minor(74900), minor(99900), intp(25)},
		{"half off exactly", minor(50000), minor(100000), intp(50)},
		// 24.5% must round to 25, not truncate to 24 — a badge that says 24
		// against a price the shopper can see is 25 reads as a lie.
		{"rounds to nearest, up", minor(75500), minor(100000), intp(25)},
		// 24.4% rounds down.
		{"rounds to nearest, down", minor(75600), minor(100000), intp(24)},
		// A one-paise cut on a ₹1,000 item is 0%. "0% off" is not a deal and
		// a client that draws a badge for it is lying.
		{"a rounding-to-zero cut is not a discount", minor(99999), minor(100000), nil},
		{"no discount when equal", minor(99900), minor(99900), nil},
		{"no discount when price exceeds mrp", minor(120000), minor(99900), nil},
		// The three ways the data can be missing. None of them is 100% off.
		{"no mrp", minor(74900), nil, nil},
		{"no price", nil, minor(99900), nil},
		{"zero mrp", minor(74900), minor(0), nil},
		{"zero price", minor(0), minor(99900), nil},
	}
	for _, tc := range cases {
		got := DiscountPct(tc.price, tc.mrp)
		switch {
		case tc.want == nil && got != nil:
			t.Errorf("%s: got %d%%, want no discount", tc.name, *got)
		case tc.want != nil && got == nil:
			t.Errorf("%s: got no discount, want %d%%", tc.name, *tc.want)
		case tc.want != nil && got != nil && *got != *tc.want:
			t.Errorf("%s: got %d%%, want %d%%", tc.name, *got, *tc.want)
		}
	}
}

func intp(v int) *int { return &v }

// The summary a shopper's grid actually receives.
func TestProductSummaryJSONCarriesWhatTheGridDraws(t *testing.T) {
	name := "Electronics"
	store := "E2E Merged Store"
	fav := true
	p := Product{
		ID:            uuid.New(),
		Title:         "Widget",
		CategoryID:    &[]uuid.UUID{uuid.New()}[0],
		CategoryName:  &name,
		RetailerName:  &store,
		IsFavourite:   &fav,
		ImageURL:      "https://cdn/m.jpg",
		ThumbnailURL:  "https://cdn/t.jpg",
		MinPriceMinor: minor(74900),
		MRPMinor:      minor(99900),
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{
		"image_url", "thumbnail_url", "category_id", "category_name",
		"discount_pct", "is_favourite", "seller_name", "min_price_minor", "mrp_minor",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("the product summary has no %q — the client cannot draw it", key)
		}
	}
	if got["discount_pct"] != float64(25) {
		t.Errorf("discount_pct = %v, want 25", got["discount_pct"])
	}
	// Both spellings of the shop's name, as MarshalJSON has always promised.
	if got["seller_name"] != store || got["retailer_name"] != store {
		t.Errorf("the store name did not reach both wire names: %v / %v",
			got["seller_name"], got["retailer_name"])
	}
}

// A product with no media sends NEITHER url field, rather than an empty
// string: `"image_url": ""` is a value a client will try to load.
func TestAProductWithNoImageSendsNoImageFields(t *testing.T) {
	raw, err := json.Marshal(Product{ID: uuid.New(), Title: "Widget"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"image_url", "thumbnail_url", "discount_pct", "is_favourite", "category_name"} {
		if v, ok := got[key]; ok {
			t.Errorf("%q is present as %v on a product that has none", key, v)
		}
	}
}

// An anonymous browse must not claim the shopper deliberately did not like
// something: `is_favourite` is absent, not false.
func TestAnonymousBrowseOmitsTheHeartRatherThanEmptyingIt(t *testing.T) {
	raw, _ := json.Marshal(Product{ID: uuid.New()})
	var got map[string]any
	_ = json.Unmarshal(raw, &got)
	if _, ok := got["is_favourite"]; ok {
		t.Fatal("is_favourite was sent to a caller with no user; the client would draw an empty heart")
	}

	no := false
	raw, _ = json.Marshal(Product{ID: uuid.New(), IsFavourite: &no})
	_ = json.Unmarshal(raw, &got)
	if v, ok := got["is_favourite"]; !ok || v != false {
		t.Fatalf("a signed-in caller's explicit false did not survive: %v (present=%v)", v, ok)
	}
}
