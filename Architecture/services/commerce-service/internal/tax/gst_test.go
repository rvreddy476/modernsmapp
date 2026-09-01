package tax

import (
	"math/rand"
	"testing"

	"github.com/atpost/commerce-service/internal/money"
)

// ─── Golden vectors ──────────────────────────────────────────────────
//
// Hand-computed. These pin the D1 ruling: catalogue prices are GST-INCLUSIVE,
// so tax is extracted, never added. The first case is the one named in the
// review — ₹1,180 at 18% must yield ₹180 of tax, not ₹212.40 added on top.

func TestGolden_InclusiveExtraction(t *testing.T) {
	cases := []struct {
		name        string
		gross       money.Paise
		rate        RateBP
		wantTaxable money.Paise
		wantTax     money.Paise
	}{
		{"18pct_1180", 118000, Rate18, 100000, 18000},
		{"5pct_105", 10500, Rate5, 10000, 500},
		{"12pct_1120", 112000, Rate12, 100000, 12000},
		{"28pct_1280", 128000, Rate28, 100000, 28000},
		{"0pct_1000", 100000, Rate0, 100000, 0},
		// Awkward amounts: the point is taxable+tax==gross regardless.
		{"18pct_1", 1, Rate18, 0, 1},
		{"18pct_99", 99, Rate18, 83, 16},
		{"5pct_333", 333, Rate5, 317, 16},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			taxable, tx := extract(tc.gross, tc.rate)
			if taxable != tc.wantTaxable || tx != tc.wantTax {
				t.Fatalf("extract(%s, %d) = (%s, %s), want (%s, %s)",
					tc.gross, tc.rate, taxable, tx, tc.wantTaxable, tc.wantTax)
			}
			if taxable.Add(tx) != tc.gross {
				t.Fatalf("taxable+tax != gross")
			}
		})
	}
}

// The defect this package exists to prevent: adding GST on top of an
// inclusive price. If someone reintroduces exclusive semantics this fails.
func TestGolden_DoesNotAddTaxOnTop(t *testing.T) {
	out, err := Compute(Input{
		Lines: []Line{{Ref: "a", GrossInclusive: 118000, Rate: Rate18}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Total != 118000 {
		t.Fatalf("total = %s, want 1180.00 — GST must be extracted, not added", out.Total)
	}
	if out.TotalTax != 18000 {
		t.Fatalf("tax = %s, want 180.00", out.TotalTax)
	}
}

func TestGolden_MixedSlabsWithDiscountAndShipping(t *testing.T) {
	// Two lines on different slabs, an order coupon and a delivery charge.
	// gross 1180.00 + 1120.00 = 2300.00; coupon 300.00; shipping 70.00
	// total charged = 2300 - 300 + 70 = 2070.00
	out, err := Compute(Input{
		Lines: []Line{
			{Ref: "l18", GrossInclusive: 118000, Rate: Rate18},
			{Ref: "l12", GrossInclusive: 112000, Rate: Rate12},
		},
		OrderDiscount: 30000,
		Shipping:      7000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Total != 207000 {
		t.Fatalf("total = %s, want 2070.00", out.Total)
	}
	if out.TotalTaxable.Add(out.TotalTax) != out.Total {
		t.Fatalf("taxable+tax != total")
	}
	// Allocation is proportional to gross: 118/230 and 112/230 of both the
	// coupon and the shipping.
	if got := out.Lines[0].AllocatedDiscount; got != 15391 {
		t.Fatalf("line0 discount alloc = %s, want 153.91", got)
	}
	if got := out.Lines[1].AllocatedDiscount; got != 14609 {
		t.Fatalf("line1 discount alloc = %s, want 146.09", got)
	}
	if out.Lines[0].AllocatedDiscount.Add(out.Lines[1].AllocatedDiscount) != 30000 {
		t.Fatalf("discount allocation does not sum to the coupon")
	}
	if out.Lines[0].AllocatedShipping.Add(out.Lines[1].AllocatedShipping) != 7000 {
		t.Fatalf("shipping allocation does not sum to the charge")
	}
}

func TestGolden_IntrastateSplitsCGSTSGST(t *testing.T) {
	out, err := Compute(Input{
		Lines:      []Line{{Ref: "a", GrossInclusive: 118000, Rate: Rate18}},
		Interstate: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.TotalCGST != 9000 || out.TotalSGST != 9000 {
		t.Fatalf("cgst/sgst = %s/%s, want 90.00/90.00", out.TotalCGST, out.TotalSGST)
	}
	if out.TotalIGST != 0 {
		t.Fatalf("igst must be zero intrastate")
	}
}

func TestGolden_InterstateUsesIGST(t *testing.T) {
	out, err := Compute(Input{
		Lines:      []Line{{Ref: "a", GrossInclusive: 118000, Rate: Rate18}},
		Interstate: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.TotalIGST != 18000 || out.TotalCGST != 0 || out.TotalSGST != 0 {
		t.Fatalf("interstate must put the whole tax in IGST, got c=%s s=%s i=%s",
			out.TotalCGST, out.TotalSGST, out.TotalIGST)
	}
}

// An odd tax amount must still split without losing a paise.
func TestGolden_OddTaxSplitsWithoutLoss(t *testing.T) {
	out, err := Compute(Input{
		Lines: []Line{{Ref: "a", GrossInclusive: 99, Rate: Rate18}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.TotalCGST.Add(out.TotalSGST) != out.TotalTax {
		t.Fatalf("cgst+sgst (%s+%s) != tax %s", out.TotalCGST, out.TotalSGST, out.TotalTax)
	}
}

func TestQuantityAggregation(t *testing.T) {
	unit := money.Paise(33333) // ₹333.33, deliberately not divisible
	out, err := Compute(Input{
		Lines: []Line{{Ref: "a", GrossInclusive: unit.MulQty(7), Rate: Rate18}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Total != unit.MulQty(7) {
		t.Fatalf("quantity aggregation lost money")
	}
	if out.TotalTaxable.Add(out.TotalTax) != out.Total {
		t.Fatalf("taxable+tax != total for qty line")
	}
}

// ─── Allocation ──────────────────────────────────────────────────────

func TestAllocate_SumsExactly(t *testing.T) {
	cases := []struct {
		amount  money.Paise
		weights []money.Paise
	}{
		{100, []money.Paise{1, 1, 1}},             // 33/33/34 — classic residual
		{1, []money.Paise{1, 1}},                  // single paise, two lines
		{7000, []money.Paise{118000, 112000}},     // the golden-vector case
		{999, []money.Paise{1, 2, 3, 4, 5, 6, 7}}, // many lines
	}
	for _, tc := range cases {
		got, err := Allocate(tc.amount, tc.weights)
		if err != nil {
			t.Fatal(err)
		}
		var sum money.Paise
		for _, g := range got {
			if g.IsNegative() {
				t.Fatalf("negative part in %v", got)
			}
			sum = sum.Add(g)
		}
		if sum != tc.amount {
			t.Fatalf("Allocate(%s, %v) = %v summing to %s", tc.amount, tc.weights, got, sum)
		}
	}
}

// Determinism is what lets a refund reproduce the original allocation.
func TestAllocate_IsDeterministic(t *testing.T) {
	weights := []money.Paise{1, 1, 1, 1, 1, 1, 1}
	first, _ := Allocate(1000, weights)
	for i := 0; i < 200; i++ {
		again, _ := Allocate(1000, weights)
		for j := range first {
			if first[j] != again[j] {
				t.Fatalf("allocation is not deterministic at index %d", j)
			}
		}
	}
}

func TestAllocate_AllZeroWeightsDoesNotDropMoney(t *testing.T) {
	got, err := Allocate(500, []money.Paise{0, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	var sum money.Paise
	for _, g := range got {
		sum = sum.Add(g)
	}
	if sum != 500 {
		t.Fatalf("zero-weight allocation dropped money: %v", got)
	}
}

// ─── Property tests (A4 / review §6) ─────────────────────────────────
//
// Golden vectors prove the cases we thought of. These prove the invariant
// over inputs we did not: whatever the slab mix, quantities, discount and
// shipping, the stored components must sum to the amount charged.

func TestProperty_ComponentsAlwaysSumToCharged(t *testing.T) {
	rng := rand.New(rand.NewSource(20260826))
	rates := []RateBP{Rate0, Rate5, Rate12, Rate18, Rate28}

	for iter := 0; iter < 20000; iter++ {
		n := 1 + rng.Intn(6)
		lines := make([]Line, n)
		var weightSum money.Paise
		for i := range lines {
			gross := money.Paise(1 + rng.Int63n(5_000_00))
			lineDisc := money.Paise(0)
			if rng.Intn(4) == 0 {
				lineDisc = money.Paise(rng.Int63n(int64(gross) + 1))
			}
			lines[i] = Line{
				Ref:            string(rune('a' + i)),
				GrossInclusive: gross,
				LineDiscount:   lineDisc,
				Rate:           rates[rng.Intn(len(rates))],
			}
			weightSum = weightSum.Add(gross.Sub(lineDisc))
		}
		var discount money.Paise
		if weightSum > 0 {
			discount = money.Paise(rng.Int63n(int64(weightSum) + 1))
		}
		shipping := money.Paise(rng.Int63n(200_00))
		interstate := rng.Intn(2) == 0

		out, err := Compute(Input{
			Lines:         lines,
			OrderDiscount: discount,
			Shipping:      shipping,
			Interstate:    interstate,
		})
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}

		// The invariant the money depends on.
		if out.TotalTaxable.Add(out.TotalTax) != out.Total {
			t.Fatalf("iter %d: taxable %s + tax %s != total %s",
				iter, out.TotalTaxable, out.TotalTax, out.Total)
		}
		want := weightSum.Sub(discount).Add(shipping)
		if out.Total != want {
			t.Fatalf("iter %d: total %s != gross-net %s", iter, out.Total, want)
		}
		if out.TotalCGST.Add(out.TotalSGST).Add(out.TotalIGST) != out.TotalTax {
			t.Fatalf("iter %d: split != tax", iter)
		}
		// Per-line identities.
		var netSum, dSum, sSum money.Paise
		for _, l := range out.Lines {
			if l.Taxable.Add(l.Tax) != l.NetInclusive {
				t.Fatalf("iter %d: line %s taxable+tax != net", iter, l.Ref)
			}
			if interstate && (l.CGST != 0 || l.SGST != 0) {
				t.Fatalf("iter %d: interstate line carries cgst/sgst", iter)
			}
			if !interstate && l.IGST != 0 {
				t.Fatalf("iter %d: intrastate line carries igst", iter)
			}
			netSum = netSum.Add(l.NetInclusive)
			dSum = dSum.Add(l.AllocatedDiscount)
			sSum = sSum.Add(l.AllocatedShipping)
		}
		if netSum != out.Total {
			t.Fatalf("iter %d: line nets do not sum to total", iter)
		}
		if dSum != discount {
			t.Fatalf("iter %d: allocated discount %s != coupon %s", iter, dSum, discount)
		}
		if sSum != shipping {
			t.Fatalf("iter %d: allocated shipping %s != charge %s", iter, sSum, shipping)
		}
	}
}

func TestProperty_RefundNeverExceedsAndAlwaysReconciles(t *testing.T) {
	rng := rand.New(rand.NewSource(4242))
	rates := []RateBP{Rate0, Rate5, Rate12, Rate18, Rate28}

	for iter := 0; iter < 10000; iter++ {
		n := 1 + rng.Intn(4)
		lines := make([]Line, n)
		for i := range lines {
			lines[i] = Line{
				Ref:            string(rune('a' + i)),
				GrossInclusive: money.Paise(100 + rng.Int63n(2_000_00)),
				Rate:           rates[rng.Intn(len(rates))],
			}
		}
		out, err := Compute(Input{Lines: lines, Shipping: money.Paise(rng.Int63n(100_00))})
		if err != nil {
			t.Fatal(err)
		}
		refund := money.Paise(rng.Int63n(int64(out.Total) + 1))
		parts, err := RefundComponents(refund, out.Lines, out.Interstate)
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		var sum, taxSum, taxableSum money.Paise
		for _, p := range parts {
			if p.Taxable.Add(p.Tax) != p.NetInclusive {
				t.Fatalf("iter %d: refund line components do not reconcile", iter)
			}
			sum = sum.Add(p.NetInclusive)
			taxSum = taxSum.Add(p.Tax)
			taxableSum = taxableSum.Add(p.Taxable)
		}
		if sum != refund {
			t.Fatalf("iter %d: refund parts %s != refund %s", iter, sum, refund)
		}
		if taxableSum.Add(taxSum) != refund {
			t.Fatalf("iter %d: refund taxable+tax != refund", iter)
		}
		if refund == out.Total {
			// A full refund must reverse exactly what was charged.
			if taxSum != out.TotalTax {
				t.Fatalf("iter %d: full refund tax %s != charged tax %s", iter, taxSum, out.TotalTax)
			}
		}
	}
}

func TestRefundCannotExceedOrder(t *testing.T) {
	out, _ := Compute(Input{Lines: []Line{{Ref: "a", GrossInclusive: 10000, Rate: Rate18}}})
	if _, err := RefundComponents(10001, out.Lines, false); err == nil {
		t.Fatal("expected a refund above the order total to be refused")
	}
}

func TestOrderDiscountCannotExceedEligibleSubtotal(t *testing.T) {
	_, err := Compute(Input{
		Lines:         []Line{{Ref: "a", GrossInclusive: 10000, Rate: Rate18}},
		OrderDiscount: 10001,
	})
	if err == nil {
		t.Fatal("expected an over-large coupon to be refused, not silently clamped")
	}
}

func TestRateFromPercent(t *testing.T) {
	for _, tc := range []struct {
		pct  float64
		want RateBP
		ok   bool
	}{
		{0, Rate0, true}, {5, Rate5, true}, {12, Rate12, true},
		{18, Rate18, true}, {28, Rate28, true}, {2.5, 250, true},
		{18.005, 0, false}, {-1, 0, false}, {101, 0, false},
	} {
		got, err := RateFromPercent(tc.pct)
		if tc.ok && (err != nil || got != tc.want) {
			t.Fatalf("RateFromPercent(%v) = (%v, %v), want %v", tc.pct, got, err, tc.want)
		}
		if !tc.ok && err == nil {
			t.Fatalf("RateFromPercent(%v) should have failed", tc.pct)
		}
	}
}
