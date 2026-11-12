package premium

import (
	"errors"
	"testing"
	"time"

	"github.com/atpost/shared/gst"
	"github.com/atpost/shared/kyc"
)

// testGSTIN is a synthetic, checksum-valid GSTIN for state (not a real one).
func testGSTIN(t *testing.T, state string) string {
	t.Helper()
	base := state + "ZZZCZ0000Z1Z"
	c, err := kyc.GSTINCheckDigit(base)
	if err != nil {
		t.Fatal(err)
	}
	return base + string(c)
}

var taxAt = time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)

func mustTax(t *testing.T, gstin string) *Tax {
	t.Helper()
	tx, err := NewTax(nil, gstin)
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

// Founder's prices exclude GST: 18% is added on top, rounded by shared/gst.
func TestPremiumGSTIsAddedOnTop(t *testing.T) {
	for _, tx := range []*Tax{mustTax(t, testGSTIN(t, "29")), EstimatedTax()} {
		for _, c := range []struct{ taxable, tax, total int64 }{{14900, 2682, 17582}, {149900, 26982, 176882}} {
			b, err := tx.Compute(c.taxable, "", taxAt)
			if err != nil {
				t.Fatal(err)
			}
			if b.TaxablePaise != c.taxable || b.TaxPaise != c.tax || b.TotalPaise != c.total {
				t.Fatalf("computed=%t %d: %+v", tx.Computed(), c.taxable, b)
			}
			if b.RateBP != 1800 || b.SAC != "998439" || !b.Provisional || !b.Valid() {
				t.Fatalf("row: %+v", b)
			}
			// No buyer address on record: the place of supply is the
			// supplier's state, so CGST + SGST.
			if b.IGSTPaise != 0 || b.CGSTPaise != c.tax/2 || b.SGSTPaise != c.tax-c.tax/2 {
				t.Fatalf("intra-state split: %+v", b)
			}
		}
	}
}

func TestPremiumGSTInterStateWhenTheBuyerStateDiffers(t *testing.T) {
	tx := mustTax(t, testGSTIN(t, "29"))
	b, err := tx.Compute(14900, "36", taxAt)
	if err != nil {
		t.Fatal(err)
	}
	if b.IGSTPaise != 2682 || b.CGSTPaise != 0 || b.SGSTPaise != 0 || b.TotalPaise != 17582 {
		t.Fatalf("inter-state: %+v", b)
	}
	b, err = tx.Compute(14900, "29", taxAt)
	if err != nil || b.IGSTPaise != 0 || b.CGSTPaise != 1341 || b.SGSTPaise != 1341 {
		t.Fatalf("same state: %+v %v", b, err)
	}
	if _, err := tx.Compute(14900, "99", taxAt); !errors.Is(err, ErrTaxUnavailable) {
		t.Fatalf("bad buyer state: %v", err)
	}
}

// The dev estimate is exactly shared/gst's exclusive rounding.
func TestPremiumGSTEstimateMatchesCompute(t *testing.T) {
	computed, est := mustTax(t, testGSTIN(t, "29")), EstimatedTax()
	for n := MinPricePaise; n < MinPricePaise+20000; n += 7 {
		a, err1 := computed.Compute(n, "", taxAt)
		b, err2 := est.Compute(n, "", taxAt)
		if err1 != nil || err2 != nil || a != b {
			t.Fatalf("%d: computed %+v (%v) estimate %+v (%v)", n, a, err1, b, err2)
		}
	}
}

func TestPremiumGSTRefusals(t *testing.T) {
	tx := EstimatedTax()
	for _, n := range []int64{0, -1} {
		if _, err := tx.Compute(n, "", taxAt); !errors.Is(err, ErrTaxUnavailable) {
			t.Fatalf("%d: %v", n, err)
		}
	}
	var none *Tax
	if _, err := none.Compute(14900, "", taxAt); !errors.Is(err, ErrTaxUnavailable) {
		t.Fatalf("nil: %v", err)
	}
	// A table without the category: nothing is priced.
	only, err := gst.NewRateTable([]gst.RateRow{{Category: gst.CategoryPlatformFee, Supplier: gst.SupplierPlatform, RateBP: 1800,
		SAC: "998599", EffectiveFrom: taxAt}})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range []string{"", testGSTIN(t, "29")} {
		tx, _ := NewTax(only, g)
		if _, err := tx.Compute(14900, "", taxAt); !errors.Is(err, ErrTaxUnavailable) {
			t.Fatalf("gstin %q: %v", g, err)
		}
	}
}

// Production refuses Premium without the platform GSTIN; a bad GSTIN is
// refused everywhere; dev runs on the estimate.
func TestPremiumTaxFromEnv(t *testing.T) {
	good := testGSTIN(t, "36")
	for _, envName := range []string{"production", "prod", " Production "} {
		if _, err := TaxFromEnv(env(map[string]string{}), envName); !errors.Is(err, ErrPlatformGSTINRequired) {
			t.Fatalf("%s without GSTIN: %v", envName, err)
		}
	}
	tx, err := TaxFromEnv(env(map[string]string{EnvPlatformGSTIN: " " + good + " "}), "production")
	if err != nil || !tx.Computed() {
		t.Fatalf("production with GSTIN: %v", err)
	}
	for _, envName := range []string{"production", "development"} {
		if _, err := TaxFromEnv(env(map[string]string{EnvPlatformGSTIN: "36ZZZCZ0000Z1Z0"}), envName); !errors.Is(err, ErrPlatformGSTINInvalid) {
			t.Fatalf("%s bad GSTIN: %v", envName, err)
		}
	}
	tx, err = TaxFromEnv(env(map[string]string{}), "development")
	if err != nil || tx.Computed() {
		t.Fatalf("dev without GSTIN: %v", err)
	}
}

func TestCatalogueShowsTheGSTInclusiveTotal(t *testing.T) {
	rows := []PlanRow{{Code: Plan30d, Name: "m", DurationDays: 30, Active: true, SortOrder: 1},
		{Code: Plan365d, Name: "y", DurationDays: 365, Active: true, SortOrder: 2}}
	got := Catalogue(rows, Prices{Plan30d: 14900, Plan365d: 149900}, EstimatedTax(), taxAt)
	for i, w := range []struct{ price, gstPaise, total int64 }{{14900, 2682, 17582}, {149900, 26982, 176882}} {
		p := got[i]
		if !p.Available || !p.PricesExcludeGST || *p.PricePaise != w.price || *p.TaxablePaise != w.price ||
			*p.GSTPaise != w.gstPaise || *p.TotalPaise != w.total || *p.GSTRateBP != 1800 || !p.TaxProvisional {
			t.Fatalf("plan %d: %+v", i, p)
		}
	}
	// A plan whose tax cannot be computed is never offered at the pre-tax price.
	got = Catalogue(rows, Prices{Plan30d: 14900}, nil, taxAt)
	if got[0].Available || got[0].PricePaise != nil || got[0].TotalPaise != nil {
		t.Fatalf("untaxable plan offered: %+v", got[0])
	}
}

func TestBreakdownValid(t *testing.T) {
	good := Breakdown{TaxablePaise: 14900, CGSTPaise: 1341, SGSTPaise: 1341, TaxPaise: 2682, TotalPaise: 17582, RateBP: 1800, SAC: "998439"}
	if !good.Valid() {
		t.Fatal("good split refused")
	}
	for name, mut := range map[string]func(*Breakdown){
		"total is not taxable + tax": func(b *Breakdown) { b.TotalPaise = b.TaxablePaise },
		"parts do not make the tax":  func(b *Breakdown) { b.IGSTPaise = 1 },
		"no taxable value":           func(b *Breakdown) { b.TaxablePaise, b.TotalPaise = 0, b.TaxPaise },
		"negative part":              func(b *Breakdown) { b.CGSTPaise, b.SGSTPaise = -1, 2683 },
	} {
		b := good
		mut(&b)
		if b.Valid() {
			t.Errorf("%s: accepted %+v", name, b)
		}
	}
}

// An unassigned buyer state is refused on the estimate path too.
func TestPremiumGSTEstimateRefusesABadBuyerState(t *testing.T) {
	if _, err := EstimatedTax().Compute(14900, "99", taxAt); !errors.Is(err, ErrTaxUnavailable) {
		t.Fatalf("bad buyer state: %v", err)
	}
}
