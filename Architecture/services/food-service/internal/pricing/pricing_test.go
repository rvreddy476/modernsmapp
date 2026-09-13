package pricing

import (
	"errors"
	"math/rand"
	"strconv"
	"testing"
	"time"

	"github.com/atpost/shared/gst"
	"github.com/atpost/shared/kyc"
)

// Synthetic identifiers only: these PANs are not issued, and the GSTIN check
// digits are computed.
const (
	testRestaurantPAN = "ZZZPZ0000Z"
	testPlatformPAN   = "ZZZCZ9999Z"
)

func synthGSTIN(t *testing.T, state, pan string) string {
	t.Helper()
	d, err := kyc.GSTINCheckDigit(state + pan + "1Z")
	if err != nil {
		t.Fatal(err)
	}
	return state + pan + "1Z" + string(d)
}

var testAt = time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)

func testConfig(t *testing.T, platformState string) Config {
	cfg := DefaultConfig()
	cfg.PlatformGSTIN = synthGSTIN(t, platformState, testPlatformPAN)
	return cfg
}

func workedCart() Cart {
	return Cart{
		Items: []ItemLine{{
			Ref: "item:1", Name: "Paneer Tikka", Quantity: 2, UnitPaise: 25000,
			Addons: []AddonLine{{Ref: "addon:1", Name: "Extra cheese", Quantity: 2, UnitPaise: 3000}},
		}},
		PackagingPaise: 2000,
	}
}

// The worked example in the report: RESTAURANT_STANDALONE (5%, s.9(5)) with
// the default fees, intra-state.
func TestPriceWorkedExampleSection95(t *testing.T) {
	q, err := Price(testConfig(t, "29"), Restaurant{TaxCategory: "RESTAURANT_STANDALONE", State: "Karnataka"}, workedCart(), testAt)
	if err != nil {
		t.Fatal(err)
	}
	want := Totals{ItemSubtotalPaise: 50000, AddonTotalPaise: 6000, PackagingFeePaise: 2000, DeliveryFeePaise: 2900,
		PlatformFeePaise: 500, TaxTotalPaise: 2900 + 90 + 522, FinalAmountPaise: 64912}
	if q.Totals != want {
		t.Fatalf("totals = %+v\nwant %+v", q.Totals, want)
	}
	if !q.Breakdown.NeedsAdviserConfirmation || !q.Breakdown.MenuPricesTreatedAsExclusive || q.Breakdown.Mode != "EXCLUSIVE" {
		t.Fatalf("breakdown flags = %+v", q.Breakdown)
	}
	for _, l := range q.Breakdown.Lines {
		if l.LiableParty != "PLATFORM" {
			t.Fatalf("line %s liable = %s; every line of a s.9(5) order is the platform's", l.Ref, l.LiableParty)
		}
		if l.ECOCollectsTCS {
			t.Fatalf("line %s marked TCS; s.9(5) supplies carry no TCS", l.Ref)
		}
		if l.Interstate {
			t.Fatalf("line %s interstate for an intra-state order", l.Ref)
		}
	}
}

func TestPriceSupplierLiableCategory(t *testing.T) {
	r := Restaurant{TaxCategory: "RESTAURANT_SPECIFIED_PREMISES", GSTIN: synthGSTIN(t, "29", testRestaurantPAN)}
	q, err := Price(testConfig(t, "29"), r, workedCart(), testAt)
	if err != nil {
		t.Fatal(err)
	}
	var restaurantTax, tcsLines int64
	for _, l := range q.Breakdown.Lines {
		switch l.Kind {
		case KindItem, KindAddon, KindPackaging:
			if l.LiableParty != "RESTAURANT" || l.Liability != "SUPPLIER" || !l.ECOCollectsTCS {
				t.Fatalf("restaurant line %s = %+v", l.Ref, l)
			}
			restaurantTax += l.TaxPaise
			tcsLines++
		case KindPlatformFee, KindDeliveryFee:
			if l.LiableParty != "PLATFORM" || l.ECOCollectsTCS {
				t.Fatalf("platform line %s = %+v", l.Ref, l)
			}
		}
	}
	if restaurantTax != 10440 || tcsLines != 3 {
		t.Fatalf("restaurant GST = %d over %d lines, want 10440 over 3", restaurantTax, tcsLines)
	}
	if q.Totals.FinalAmountPaise != 58000+500+2900+10440+90+522 {
		t.Fatalf("final = %d", q.Totals.FinalAmountPaise)
	}
	view := TaxesAndChargesFrom(q.Breakdown, q.Totals)
	if len(view.Taxes) != 2 || view.Taxes[0].LiableParty != "RESTAURANT" || view.Taxes[1].LiableParty != "PLATFORM" {
		t.Fatalf("taxes groups = %+v", view.Taxes)
	}
	if view.AdviserNotice != AdviserNotice || !view.NeedsAdviserConfirmation {
		t.Fatalf("adviser notice missing: %+v", view)
	}
}

func TestPriceInterStateUsesIGST(t *testing.T) {
	// Platform registered in Maharashtra, restaurant in Karnataka: the s.9(5)
	// lines are taxed from the platform's state, so IGST.
	q, err := Price(testConfig(t, "27"), Restaurant{TaxCategory: "CLOUD_KITCHEN_TAKEAWAY", State: "29"}, workedCart(), testAt)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range q.Breakdown.Lines {
		if !l.Interstate || l.IGSTPaise != l.TaxPaise || l.CGSTPaise != 0 || l.SGSTPaise != 0 {
			t.Fatalf("line %s not IGST: %+v", l.Ref, l)
		}
	}
	intra, err := Price(testConfig(t, "29"), Restaurant{TaxCategory: "CLOUD_KITCHEN_TAKEAWAY", State: "29"}, workedCart(), testAt)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range intra.Breakdown.Lines {
		if l.Interstate || l.IGSTPaise != 0 || l.CGSTPaise+l.SGSTPaise != l.TaxPaise {
			t.Fatalf("intra line %s: %+v", l.Ref, l)
		}
	}
}

func TestPriceRefusals(t *testing.T) {
	cfg := testConfig(t, "29")
	cases := []struct {
		name string
		cfg  Config
		r    Restaurant
		want error
		code string
	}{
		{"no tax category", cfg, Restaurant{State: "Karnataka"}, ErrRestaurantTaxCategoryMissing, "FOOD_RESTAURANT_TAX_CATEGORY_MISSING"},
		{"no state", cfg, Restaurant{TaxCategory: "RESTAURANT_STANDALONE"}, ErrRestaurantStateUnknown, "FOOD_RESTAURANT_STATE_UNKNOWN"},
		{"no platform gstin", DefaultConfig(), Restaurant{TaxCategory: "RESTAURANT_STANDALONE", State: "Karnataka"}, ErrPlatformGSTINNotConfigured, "FOOD_PLATFORM_GSTIN_NOT_CONFIGURED"},
		{"supplier liable without gstin", cfg, Restaurant{TaxCategory: "OUTDOOR_CATERING", State: "Karnataka"}, ErrRestaurantGSTINMissing, "FOOD_RESTAURANT_GSTIN_MISSING"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Price(tc.cfg, tc.r, workedCart(), testAt)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if got := Code(err); got != tc.code {
				t.Fatalf("code = %s, want %s", got, tc.code)
			}
		})
	}
}

func TestRestaurantStateResolution(t *testing.T) {
	cases := map[string]Restaurant{
		"29": {State: "karnataka "},
		"27": {GSTINStateCode: "27", State: "Karnataka"},
		"33": {State: "33"},
	}
	for want, r := range cases {
		got, err := r.PlaceOfSupplyState()
		if err != nil || got != want {
			t.Fatalf("%+v -> %s, %v; want %s", r, got, err, want)
		}
	}
	if _, err := (Restaurant{State: "Atlantis"}).PlaceOfSupplyState(); !errors.Is(err, ErrRestaurantStateUnknown) {
		t.Fatalf("unknown state: %v", err)
	}
}

// Over many random carts every total is the sum of its lines to the paise and
// the order total is exactly items + add-ons + packaging + fees + tax - discount.
func TestPriceExactnessOverRandomCarts(t *testing.T) {
	rng := rand.New(rand.NewSource(20260913))
	categories := []string{"RESTAURANT_STANDALONE", "CLOUD_KITCHEN_TAKEAWAY", "RESTAURANT_SPECIFIED_PREMISES", "OUTDOOR_CATERING", "OUTDOOR_CATERING_SPECIFIED_PREMISES"}
	restaurantGSTIN := synthGSTIN(t, "29", testRestaurantPAN)
	for i := 0; i < 2000; i++ {
		cfg := testConfig(t, []string{"29", "27"}[rng.Intn(2)])
		cfg.PlatformFeePaise = int64(rng.Intn(1500))
		cfg.DeliveryFeePaise = int64(rng.Intn(6000))
		cart := Cart{PackagingPaise: int64(rng.Intn(3000))}
		for n := 1 + rng.Intn(6); n > 0; n-- {
			item := ItemLine{Ref: "item:" + strconv.Itoa(n), Quantity: int64(1 + rng.Intn(5)), UnitPaise: int64(1 + rng.Intn(90000))}
			for a := rng.Intn(3); a > 0; a-- {
				item.Addons = append(item.Addons, AddonLine{Ref: "addon:" + strconv.Itoa(n) + ":" + strconv.Itoa(a), Quantity: int64(1 + rng.Intn(4)), UnitPaise: int64(rng.Intn(9000))})
			}
			cart.Items = append(cart.Items, item)
		}
		r := Restaurant{TaxCategory: categories[rng.Intn(len(categories))], GSTIN: restaurantGSTIN}
		supply := int64(0)
		for _, it := range cart.Items {
			supply += it.Quantity * it.UnitPaise
			for _, a := range it.Addons {
				supply += a.Quantity * a.UnitPaise
			}
		}
		supply += cart.PackagingPaise
		if rng.Intn(3) == 0 {
			cart.DiscountPaise = rng.Int63n(supply + 1)
		}
		q, err := Price(cfg, r, cart, testAt)
		if err != nil {
			t.Fatalf("cart %d: %v", i, err)
		}
		tot := q.Totals
		if tot.Sum() != tot.FinalAmountPaise {
			t.Fatalf("cart %d: components sum %d != final %d (%+v)", i, tot.Sum(), tot.FinalAmountPaise, tot)
		}
		var gross, tax, lineCGST, lineSGST, lineIGST int64
		for _, l := range q.Breakdown.Lines {
			if l.TaxablePaise+l.TaxPaise != l.GrossPaise || l.CGSTPaise+l.SGSTPaise+l.IGSTPaise != l.TaxPaise {
				t.Fatalf("cart %d line %s does not add up: %+v", i, l.Ref, l)
			}
			gross += l.GrossPaise
			tax += l.TaxPaise
			lineCGST += l.CGSTPaise
			lineSGST += l.SGSTPaise
			lineIGST += l.IGSTPaise
		}
		if gross != tot.FinalAmountPaise || tax != tot.TaxTotalPaise || q.Breakdown.TotalPaise != tot.FinalAmountPaise {
			t.Fatalf("cart %d: lines gross %d tax %d vs totals %+v", i, gross, tax, tot)
		}
		if lineCGST != q.Breakdown.TotalCGSTPaise || lineSGST != q.Breakdown.TotalSGSTPaise || lineIGST != q.Breakdown.TotalIGSTPaise {
			t.Fatalf("cart %d: component totals drift", i)
		}
		var partyGross int64
		for _, p := range q.Breakdown.ByLiableParty {
			partyGross += p.GrossPaise
		}
		if partyGross != tot.FinalAmountPaise {
			t.Fatalf("cart %d: party gross %d != final %d", i, partyGross, tot.FinalAmountPaise)
		}
		view := TaxesAndChargesFrom(q.Breakdown, tot)
		var viewTax int64
		for _, g := range view.Taxes {
			var rateTax int64
			for _, rl := range g.Rates {
				rateTax += rl.TaxPaise
			}
			if rateTax != g.TaxPaise {
				t.Fatalf("cart %d: group %s rates %d != %d", i, g.LiableParty, rateTax, g.TaxPaise)
			}
			viewTax += g.TaxPaise
		}
		if viewTax != tot.TaxTotalPaise || view.TotalTaxPaise != tot.TaxTotalPaise {
			t.Fatalf("cart %d: taxes_and_charges tax %d != %d", i, viewTax, tot.TaxTotalPaise)
		}
		itemTax := q.TaxByItem()
		var perItem int64
		for _, v := range itemTax {
			perItem += v
		}
		for _, l := range q.Breakdown.Lines {
			if l.Kind == KindPackaging || l.Kind == KindPlatformFee || l.Kind == KindDeliveryFee {
				perItem += l.TaxPaise
			}
		}
		if perItem != tot.TaxTotalPaise {
			t.Fatalf("cart %d: per-item tax + fee tax %d != %d", i, perItem, tot.TaxTotalPaise)
		}
	}
}

func TestZeroFeesAreOmitted(t *testing.T) {
	cfg := testConfig(t, "29")
	cfg.PlatformFeePaise, cfg.DeliveryFeePaise = 0, 0
	cart := workedCart()
	cart.PackagingPaise = 0
	q, err := Price(cfg, Restaurant{TaxCategory: "RESTAURANT_STANDALONE", State: "29"}, cart, testAt)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range q.Breakdown.Lines {
		if l.AmountPaise == 0 {
			t.Fatalf("zero line %s kept", l.Ref)
		}
	}
}

func TestConfigFromEnv(t *testing.T) {
	gstin := synthGSTIN(t, "29", testPlatformPAN)
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	cfg, err := ConfigFromEnv(env(map[string]string{"ENV": "dev"}))
	if err != nil || cfg.PlatformGSTIN != "" || cfg.PlatformFeePaise != 500 || cfg.DeliveryFeePaise != 2900 || cfg.CouponsEnabled {
		t.Fatalf("dev defaults = %+v, %v", cfg, err)
	}
	if _, err := ConfigFromEnv(env(map[string]string{"ENV": "production"})); err == nil {
		t.Fatal("production without FOOD_PLATFORM_GSTIN must refuse to start")
	}
	if _, err := ConfigFromEnv(env(map[string]string{})); err == nil {
		t.Fatal("a blank ENV is production")
	}
	if _, err := ConfigFromEnv(env(map[string]string{"ENV": "dev", EnvPlatformGSTIN: "29ZZZCZ9999Z1ZX"})); err == nil {
		t.Fatal("a malformed GSTIN must be refused even in dev")
	}
	cfg, err = ConfigFromEnv(env(map[string]string{"ENV": "production", EnvPlatformGSTIN: " " + gstin + " ", EnvPlatformFeePaise: "0",
		EnvDeliveryFeePaise: "3500", EnvCouponsEnabled: "true"}))
	if err != nil || cfg.PlatformGSTIN != gstin || cfg.PlatformFeePaise != 0 || cfg.DeliveryFeePaise != 3500 || !cfg.CouponsEnabled {
		t.Fatalf("configured = %+v, %v", cfg, err)
	}
	for _, bad := range []map[string]string{
		{"ENV": "dev", EnvPlatformFeePaise: "-1"},
		{"ENV": "dev", EnvDeliveryFeePaise: "29.00"},
		{"ENV": "dev", EnvCouponsEnabled: "maybe"},
	} {
		if _, err := ConfigFromEnv(env(bad)); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
}

func TestCouponGate(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.CheckCoupon(""); err != nil {
		t.Fatalf("no coupon: %v", err)
	}
	if err := cfg.CheckCoupon("FIGO50"); !errors.Is(err, ErrCouponsDisabled) || Code(err) != "FOOD_COUPONS_DISABLED" {
		t.Fatalf("disabled flag: %v", err)
	}
	cfg.CouponsEnabled = true
	if err := cfg.CheckCoupon("FIGO50"); err != nil {
		t.Fatalf("enabled flag: %v", err)
	}
}

func TestBreakdownRoundTripsThroughGST(t *testing.T) {
	q, err := Price(testConfig(t, "29"), Restaurant{TaxCategory: "RESTAURANT_STANDALONE", State: "29"}, workedCart(), testAt)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range q.Breakdown.Lines {
		if l.Category == "" || l.SAC == "" || l.RateBP == 0 || l.RateEffectiveFrom == "" {
			t.Fatalf("line %s lost its rate row: %+v", l.Ref, l)
		}
		if gst.Category(l.Category) == gst.CategoryPlatformFee && l.Kind != KindPlatformFee {
			t.Fatalf("platform fee kind = %s", l.Kind)
		}
	}
}
