package postgres

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// B8 unit tests: no database.

// paiseFromDecimal reads a rupee amount through its two-decimal text form, so
// the expectation does not share PaiseOf's arithmetic.
func paiseFromDecimal(t *testing.T, rupees float64) int64 {
	t.Helper()
	s := strings.Replace(strconv.FormatFloat(rupees, 'f', 2, 64), ".", "", 1)
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

type fakeScanner []any

func (f fakeScanner) Scan(dest ...any) error {
	for i, d := range dest {
		switch p := d.(type) {
		case *uuid.UUID:
			*p = f[i].(uuid.UUID)
		case *string:
			*p = f[i].(string)
		case *float64:
			*p = f[i].(float64)
		case *bool:
			*p = f[i].(bool)
		case *int:
			*p = f[i].(int)
		}
	}
	return nil
}

// assertPaiseSiblings walks a JSON value: every "<key>_paise" whose "<key>"
// sibling is a number must equal that number in paise, and at least `min`
// such pairs must exist.
func assertPaiseSiblings(t *testing.T, what string, v any, min int) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	pairs := 0
	var walk func(any)
	walk = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			for k, val := range n {
				if !strings.HasSuffix(k, "_paise") {
					walk(val)
					continue
				}
				sibling, ok := n[strings.TrimSuffix(k, "_paise")].(float64)
				if !ok {
					continue
				}
				pairs++
				if got := int64(val.(float64)); got != paiseFromDecimal(t, sibling) {
					t.Errorf("%s: %s = %d, want %d (%s = %v)", what, k, got, paiseFromDecimal(t, sibling), strings.TrimSuffix(k, "_paise"), sibling)
				}
			}
		case []any:
			for _, e := range n {
				walk(e)
			}
		}
	}
	walk(decoded)
	if pairs < min {
		t.Fatalf("%s: %d float/paise pairs, want at least %d (%s)", what, pairs, min, raw)
	}
}

func TestB8PaiseSiblingsMatchFloats(t *testing.T) {
	for _, v := range []float64{0.01, 0.07, 0.29, 1.1, 19.99, 35.12, 649.12, 1234.56, 99999.99} {
		item := MenuItem{ID: uuid.New(), BasePrice: v, DiscountPrice: v}
		item.FillPaise()
		assertPaiseSiblings(t, "menu item", item, 2)

		b := PriceBreakdown{ItemSubtotal: v, AddonTotal: v, PackagingFee: v, TaxTotal: v, DeliveryFee: v, PlatformFee: v,
			RestaurantDiscount: v, CouponDiscount: v, FinalAmount: v}
		assertPaiseSiblings(t, "partner order", PartnerOrderOf(&Order{ID: uuid.New(), Totals: b}), 9)

		k := KitchenOrder{ID: uuid.New(), FinalAmount: v}
		k.FillDerived(nil)
		assertPaiseSiblings(t, "kitchen queue row", k, 1)

		// Every amount is a real two-decimal rupee value, as NUMERIC(12,2) stores.
		assertPaiseSiblings(t, "reports summary", PartnerSummaryMap(uuid.New(), 1, 1, 0, v, 0.07, 0.01), 4)
		assertPaiseSiblings(t, "settlement", PartnerSettlementMap(uuid.New(), PartnerSettlementRow{
			Gross: v, Commission: v, RefundAdjustment: v, Penalty: v, Payout: v}), 5)

		variant, err := scanVariant(fakeScanner{uuid.New(), uuid.New(), "Half", v, true, 0})
		if err != nil {
			t.Fatal(err)
		}
		assertPaiseSiblings(t, "variant", variant, 1)
		addon, err := scanAddon(fakeScanner{uuid.New(), uuid.New(), "Cheese", v, true, 0})
		if err != nil {
			t.Fatal(err)
		}
		assertPaiseSiblings(t, "add-on", addon, 1)
	}
}

func TestB8PartnerOrderDropsTheDeliveryCodeAndKeepsTheOrder(t *testing.T) {
	o := &Order{ID: uuid.New(), Status: "PICKED_UP", DeliveryCode: "1234", Totals: PriceBreakdown{FinalAmount: 10}}
	view := PartnerOrderOf(o)
	raw, _ := json.Marshal(view)
	if strings.Contains(string(raw), "delivery_code") || strings.Contains(string(raw), "1234") {
		t.Fatalf("partner order carries the drop-off code: %s", raw)
	}
	if o.DeliveryCode != "1234" {
		t.Fatal("the source order was modified")
	}
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	totals, _ := decoded["totals"].(map[string]any)
	if decoded["status"] != "PICKED_UP" || totals["final_amount"] != 10.0 || totals["final_amount_paise"] != 1000.0 {
		t.Fatalf("partner order shape: %s", raw)
	}
	if PartnerOrdersOf(nil) != nil {
		t.Fatal("a nil order list must stay nil (serialised as before)")
	}
}

func TestB8KitchenDeadlineRFC3339(t *testing.T) {
	k := KitchenOrder{FinalAmount: 1}
	at := time.Date(2026, 9, 13, 12, 0, 5, 0, time.FixedZone("IST", 19800))
	k.FillDerived(&at)
	if k.AcceptDeadlineAtRFC3339 == nil || *k.AcceptDeadlineAtRFC3339 != "2026-09-13T06:30:05Z" {
		t.Fatalf("rfc3339 = %v", k.AcceptDeadlineAtRFC3339)
	}
	k.FillDerived(nil)
	if k.AcceptDeadlineAtRFC3339 != nil {
		t.Fatal("no deadline must stay absent")
	}
}

func i64(v int64) *int64 { return &v }

func TestB8ResolvePricePaise(t *testing.T) {
	cases := []struct {
		name     string
		paise    *int64
		rupees   *float64
		required bool
		want     *int64
		code     string
	}{
		{"paise only", i64(4999), nil, true, i64(4999), ""},
		{"rupees only", nil, f64(19.99), true, i64(1999), ""},
		{"both agree", i64(1999), f64(19.99), true, i64(1999), ""},
		{"both disagree", i64(2000), f64(19.99), true, nil, CodeMenuPriceMismatch},
		{"negative paise", i64(-1), nil, true, nil, CodeMenuPriceInvalid},
		{"negative rupees", nil, f64(-0.01), true, nil, CodeMenuPriceInvalid},
		{"three decimals", nil, f64(1.005), true, nil, CodeMenuPriceInvalid},
		{"too large", i64(maxMenuPricePaise + 1), nil, true, nil, CodeMenuPriceInvalid},
		{"missing required", nil, nil, true, nil, CodeMenuPriceRequired},
		{"missing optional", nil, nil, false, nil, ""},
		{"zero is a price", i64(0), nil, true, i64(0), ""},
	}
	for _, tc := range cases {
		got, err := ResolvePricePaise(tc.paise, tc.rupees, tc.required)
		if code := fieldCodeOf(err); code != tc.code {
			t.Errorf("%s: code %q (err %v), want %q", tc.name, code, err, tc.code)
			continue
		}
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestB8AddonGroupRule(t *testing.T) {
	for _, ok := range [][2]int{{0, 1}, {1, 1}, {2, 5}} {
		if err := ValidateAddonGroupRule(ok[0], ok[1]); err != nil {
			t.Errorf("min %d max %d refused: %v", ok[0], ok[1], err)
		}
	}
	for _, bad := range [][2]int{{-1, 1}, {0, 0}, {2, 1}, {0, 101}} {
		if fieldCodeOf(ValidateAddonGroupRule(bad[0], bad[1])) != CodeAddonGroupSelectInvalid {
			t.Errorf("min %d max %d accepted", bad[0], bad[1])
		}
	}
}
