package settlement

import (
	"reflect"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/pricing"
	"github.com/atpost/shared/kyc"
)

func synthGSTIN(t *testing.T, state, pan string) string {
	t.Helper()
	d, err := kyc.GSTINCheckDigit(state + pan + "1Z")
	if err != nil {
		t.Fatal(err)
	}
	return state + pan + "1Z" + string(d)
}

func pricedOrder(t *testing.T, category string, commissionBP, refund int64) Order {
	t.Helper()
	cfg := pricing.DefaultConfig()
	cfg.PlatformGSTIN = synthGSTIN(t, "29", "ZZZCZ9999Z")
	r := pricing.Restaurant{TaxCategory: category, State: "29", GSTIN: synthGSTIN(t, "29", "ZZZPZ0000Z")}
	cart := pricing.Cart{
		Items: []pricing.ItemLine{{Ref: "item:1", Quantity: 2, UnitPaise: 25000,
			Addons: []pricing.AddonLine{{Ref: "addon:1", Quantity: 2, UnitPaise: 3000}}}},
		PackagingPaise: 2000,
	}
	q, err := pricing.Price(cfg, r, cart, time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	b := q.Breakdown
	return Order{
		OrderID: "o-" + category, ItemSubtotalPaise: q.Totals.ItemSubtotalPaise, AddonTotalPaise: q.Totals.AddonTotalPaise,
		PackagingFeePaise: q.Totals.PackagingFeePaise, PlatformFeePaise: q.Totals.PlatformFeePaise, DeliveryFeePaise: q.Totals.DeliveryFeePaise,
		FinalAmountPaise: q.Totals.FinalAmountPaise, CommissionBP: commissionBP, ProcessedRefundPaise: refund, Breakdown: &b,
	}
}

// The formula table from the report, in paise.
func TestComputeRestaurantFormulaTable(t *testing.T) {
	rules := DefaultRules()
	cases := []struct {
		name  string
		order Order
		want  Line
	}{
		{
			// net 58000; commission 15% 8700; GST on commission 18% 1566;
			// s.9(5) GST never reaches the restaurant; no TCS.
			"s.9(5) restaurant", pricedOrder(t, "RESTAURANT_STANDALONE", 1500, 0),
			Line{OrderCount: 1, NetSupplyPaise: 58000, CommissionPaise: 8700, CommissionGSTPaise: 1566,
				GSTPassthroughPaise: 0, TCSBasePaise: 0, TCSPaise: 0, RefundSharePaise: 0, PayoutPaise: 47734,
				ExcludedPlatformFeePaise: 500, ExcludedDeliveryFeePaise: 2900, ExcludedSection95GSTPaise: 2900, ExcludedPlatformOwnGSTPaise: 612},
		},
		{
			// supplier-liable 18%: passthrough 10440; TCS 0.5% of 58000 = 290.
			"specified premises", pricedOrder(t, "RESTAURANT_SPECIFIED_PREMISES", 1500, 0),
			Line{OrderCount: 1, NetSupplyPaise: 58000, CommissionPaise: 8700, CommissionGSTPaise: 1566,
				GSTPassthroughPaise: 10440, TCSBasePaise: 58000, TCSPaise: 290, PayoutPaise: 57884,
				ExcludedPlatformFeePaise: 500, ExcludedDeliveryFeePaise: 2900, ExcludedPlatformOwnGSTPaise: 612},
		},
		{
			// A full refund of a delivered specified-premises order takes back
			// the whole restaurant share (net + passthrough = 68440).
			"fully refunded", pricedOrder(t, "RESTAURANT_SPECIFIED_PREMISES", 1500, 72452),
			Line{OrderCount: 1, NetSupplyPaise: 58000, CommissionPaise: 8700, CommissionGSTPaise: 1566,
				GSTPassthroughPaise: 10440, TCSBasePaise: 58000, TCSPaise: 290, RefundSharePaise: 68440, PayoutPaise: -10556,
				ExcludedPlatformFeePaise: 500, ExcludedDeliveryFeePaise: 2900, ExcludedPlatformOwnGSTPaise: 612},
		},
		{
			// A partial refund of 10000 of a 64912 s.9(5) order: the restaurant
			// share is 58000/64912 of it, largest remainder = 8935.
			"partially refunded", pricedOrder(t, "RESTAURANT_STANDALONE", 1000, 10000),
			Line{OrderCount: 1, NetSupplyPaise: 58000, CommissionPaise: 5800, CommissionGSTPaise: 1044,
				RefundSharePaise: 8935, PayoutPaise: 58000 - 5800 - 1044 - 8935,
				ExcludedPlatformFeePaise: 500, ExcludedDeliveryFeePaise: 2900, ExcludedSection95GSTPaise: 2900, ExcludedPlatformOwnGSTPaise: 612},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeRestaurant([]Order{tc.order}, rules)
			tc.want.Rules = rules
			tc.want.NeedsAdviserConfirmation = true
			got.Orders = nil
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("line = %+v\nwant  %+v", got, tc.want)
			}
		})
	}
}

// The payout never moves with the platform fee, the delivery fee or s.9(5)
// GST: change all three and the restaurant's money is identical.
func TestPlatformChargesNeverReachThePayout(t *testing.T) {
	base := pricedOrder(t, "RESTAURANT_STANDALONE", 1500, 0)
	bumped := base
	b := *base.Breakdown
	b.Lines = append([]pricing.BreakdownLine(nil), base.Breakdown.Lines...)
	for i := range b.Lines {
		switch b.Lines[i].Kind {
		case pricing.KindPlatformFee, pricing.KindDeliveryFee:
			b.Lines[i].AmountPaise *= 10
			b.Lines[i].TaxablePaise *= 10
			b.Lines[i].TaxPaise *= 10
			b.Lines[i].GrossPaise = b.Lines[i].TaxablePaise + b.Lines[i].TaxPaise
		case pricing.KindItem, pricing.KindAddon, pricing.KindPackaging:
			// s.9(5) GST on the restaurant's supply
			b.Lines[i].TaxPaise *= 3
			b.Lines[i].GrossPaise = b.Lines[i].TaxablePaise + b.Lines[i].TaxPaise
		}
	}
	bumped.Breakdown = &b
	bumped.PlatformFeePaise *= 10
	bumped.DeliveryFeePaise *= 10
	x, y := ComputeRestaurant([]Order{base}, DefaultRules()), ComputeRestaurant([]Order{bumped}, DefaultRules())
	if x.PayoutPaise != y.PayoutPaise || x.GSTPassthroughPaise != 0 || y.GSTPassthroughPaise != 0 {
		t.Fatalf("payout moved with platform charges or s.9(5) GST: %d vs %d (passthrough %d/%d)", x.PayoutPaise, y.PayoutPaise, x.GSTPassthroughPaise, y.GSTPassthroughPaise)
	}
}

func TestTCSOnlyOnSupplierLiableLines(t *testing.T) {
	eco := ComputeRestaurant([]Order{pricedOrder(t, "CLOUD_KITCHEN_TAKEAWAY", 1500, 0)}, DefaultRules())
	if eco.TCSBasePaise != 0 || eco.TCSPaise != 0 {
		t.Fatalf("TCS on a s.9(5) order: base %d tcs %d", eco.TCSBasePaise, eco.TCSPaise)
	}
	supplier := ComputeRestaurant([]Order{pricedOrder(t, "OUTDOOR_CATERING", 1500, 0)}, DefaultRules())
	// OUTDOOR_CATERING is supplier-liable at 5%: TCS base is the restaurant's
	// taxable value only, never the platform's fees.
	if supplier.TCSBasePaise != 58000 || supplier.TCSPaise != 290 || supplier.GSTPassthroughPaise != 2900 {
		t.Fatalf("supplier-liable: %+v", supplier)
	}
}

func TestComputeRestaurantAggregatesAndRounding(t *testing.T) {
	// Three orders of 100 paise at 15% commission: per-order commission
	// rounds half up (15 each); GST on commission is on the period total
	// (45 * 18% = 8.1 -> 8).
	o := Order{ItemSubtotalPaise: 100, FinalAmountPaise: 100, CommissionBP: 1500}
	got := ComputeRestaurant([]Order{o, o, o}, DefaultRules())
	if got.OrderCount != 3 || got.NetSupplyPaise != 300 || got.CommissionPaise != 45 || got.CommissionGSTPaise != 8 || got.OrdersWithoutTaxBreakdown != 3 {
		t.Fatalf("aggregate = %+v", got)
	}
	if got.PayoutPaise != 300-45-8 {
		t.Fatalf("payout = %d", got.PayoutPaise)
	}
	if len(got.Orders) != 3 {
		t.Fatalf("per-order detail = %d", len(got.Orders))
	}
	empty := ComputeRestaurant(nil, DefaultRules())
	if empty.PayoutPaise != 0 || empty.OrderCount != 0 {
		t.Fatalf("empty = %+v", empty)
	}
}

func TestRestaurantDiscountReducesNetSupply(t *testing.T) {
	o := Order{ItemSubtotalPaise: 10000, AddonTotalPaise: 1000, PackagingFeePaise: 500, RestaurantDiscountPaise: 1500, FinalAmountPaise: 10000, CommissionBP: 2000}
	got := ComputeRestaurant([]Order{o}, DefaultRules())
	if got.NetSupplyPaise != 10000 || got.CommissionPaise != 2000 {
		t.Fatalf("discounted = %+v", got)
	}
}

func TestRulesFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	r, err := RulesFromEnv(env(nil))
	if err != nil || r != (Rules{CommissionGSTBP: 1800, TCSRateBP: 50}) {
		t.Fatalf("defaults = %+v, %v", r, err)
	}
	r, err = RulesFromEnv(env(map[string]string{EnvCommissionGSTBP: "0", EnvTCSRateBP: "100"}))
	if err != nil || r != (Rules{CommissionGSTBP: 0, TCSRateBP: 100}) {
		t.Fatalf("configured = %+v, %v", r, err)
	}
	for _, bad := range []map[string]string{{EnvCommissionGSTBP: "-1"}, {EnvTCSRateBP: "10001"}, {EnvTCSRateBP: "0.5"}} {
		if _, err := RulesFromEnv(env(bad)); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
}
