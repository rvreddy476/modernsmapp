package pricing

import (
	"errors"
	"testing"
	"time"
)

// TestGSTIN is the placeholder platform GSTIN the local stack uses
// (FOOD_PLATFORM_GSTIN in docker-compose): Karnataka, checksum-valid.
const TestGSTIN = "29ZZZPZ0000Z1Z6"

var (
	autoRule = FareRule{
		BasePaise: 2500, PerKMPaise: 1200, PerMinutePaise: 0, MinimumPaise: 4000,
		PlatformFeePaise: 500, CancellationFeePaise: 1500,
		WaitingFreeMinutes: 3, WaitingPerMinutePaise: 150, CancelFreeSeconds: 120,
	}
	onDate = time.Date(2026, time.September, 18, 9, 0, 0, 0, time.UTC)
)

func flat() TaxComputer { return DefaultFlatRateTax() }

func mustCompute(t *testing.T, in Input) Breakdown {
	t.Helper()
	b, err := Compute(in)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	return b
}

func TestCompute_IntegerMoneyNoSurge(t *testing.T) {
	// 5.5 km auto: 2500 + 5500*1200/1000 = 9100; platform fee 500.
	b := mustCompute(t, Input{Rule: autoRule, DistanceMeters: 5500, DurationSeconds: 900, Tax: flat(), InvoiceDate: onDate})
	if b.BasePaise != 2500 || b.DistancePaise != 6600 || b.TimePaise != 0 || b.SurgePaise != 0 {
		t.Fatalf("components %+v", b)
	}
	if b.RideFarePaise != 9100 || b.TaxableRideFarePaise != 9100 || b.MinimumTopUpPaise != 0 {
		t.Fatalf("ride fare %+v", b)
	}
	// 5% of 9100 = 455; 18% of 500 = 90.
	if b.TaxPaise != 545 || b.TotalPaise != 9100+500+545 {
		t.Fatalf("tax %d total %d", b.TaxPaise, b.TotalPaise)
	}
	if len(b.TaxLines) != 2 || b.TaxLines[0].Ref != RefRideFare || b.TaxLines[1].Ref != RefPlatformFee {
		t.Fatalf("tax lines %+v", b.TaxLines)
	}
	if b.TaxNote != TaxNote || b.SurgeReason != SurgeNone || b.FarePolicyVersion != FarePolicyVersion {
		t.Fatalf("labels %+v", b)
	}
	if b.DistanceMeters != 5500 || b.DurationSeconds != 900 {
		t.Fatalf("priced route %d m %d s", b.DistanceMeters, b.DurationSeconds)
	}
}

func TestCompute_TruncatesTowardZero(t *testing.T) {
	// 1234 m at 1200/km = 1480.8 -> 1480; 61 s at 100/min = 101.66 -> 101.
	r := autoRule
	r.PerMinutePaise = 100
	r.MinimumPaise = 0
	b := mustCompute(t, Input{Rule: r, DistanceMeters: 1234, DurationSeconds: 61, Tax: flat(), InvoiceDate: onDate})
	if b.DistancePaise != 1480 || b.TimePaise != 101 {
		t.Fatalf("distance %d time %d", b.DistancePaise, b.TimePaise)
	}
}

func TestCompute_MinimumFareTopUp(t *testing.T) {
	// 500 m: 2500 + 600 = 3100 < 4000 minimum.
	b := mustCompute(t, Input{Rule: autoRule, DistanceMeters: 500, Tax: flat(), InvoiceDate: onDate})
	if b.RideFarePaise != 4000 || b.MinimumTopUpPaise != 900 {
		t.Fatalf("minimum not applied: %+v", b)
	}
}

func TestCompute_SurgeAppliesToRideFareOnly(t *testing.T) {
	b := mustCompute(t, Input{Rule: autoRule, DistanceMeters: 5500, SurgeBPS: 2500, SurgeReason: SurgePeakHours, WindowName: "Morning peak", Tax: flat(), InvoiceDate: onDate})
	// 9100 * 2500 / 10000 = 2275.
	if b.SurgePaise != 2275 || b.RideFarePaise != 11375 || b.PlatformFeePaise != 500 {
		t.Fatalf("surge %+v", b)
	}
	if b.SurgeBasisPoints != 2500 || b.SurgeReason != SurgePeakHours || b.WindowName != "Morning peak" {
		t.Fatalf("surge labels %+v", b)
	}
}

func TestCompute_CouponReducesTaxableRideFareNeverPlatformFee(t *testing.T) {
	c := &Coupon{ID: "c1", Code: "SAVE20", DiscountType: CouponPercent, PercentBPS: 2000, MaxDiscountPaise: 5000}
	b := mustCompute(t, Input{Rule: autoRule, DistanceMeters: 5500, Coupon: c, Tax: flat(), InvoiceDate: onDate})
	// 20% of 9100 = 1820.
	if b.DiscountPaise != 1820 || b.TaxableRideFarePaise != 7280 || b.PlatformFeePaise != 500 {
		t.Fatalf("coupon %+v", b)
	}
	if b.CouponCode != "SAVE20" || b.CouponID != "c1" {
		t.Fatalf("coupon labels %+v", b)
	}
	// Tax on the discounted fare: 5% of 7280 = 364; fee 90.
	if b.TaxPaise != 454 || b.TotalPaise != 7280+500+454 {
		t.Fatalf("tax after discount %d total %d", b.TaxPaise, b.TotalPaise)
	}
}

func TestCompute_CouponNeverBelowMinimumFare(t *testing.T) {
	// Ride fare 4300 (1500 m), minimum 4000: a Rs 30 flat coupon is clamped to Rs 3.
	c := &Coupon{Code: "BIG", DiscountType: CouponFlat, ValuePaise: 3000}
	b := mustCompute(t, Input{Rule: autoRule, DistanceMeters: 1500, Coupon: c, Tax: flat(), InvoiceDate: onDate})
	if b.RideFarePaise != 4300 || b.DiscountPaise != 300 || b.TaxableRideFarePaise != 4000 {
		t.Fatalf("clamp %+v", b)
	}
	// At the minimum already: nothing to discount.
	b = mustCompute(t, Input{Rule: autoRule, DistanceMeters: 500, Coupon: c, Tax: flat(), InvoiceDate: onDate})
	if b.DiscountPaise != 0 || b.TaxableRideFarePaise != 4000 {
		t.Fatalf("at minimum %+v", b)
	}
}

func TestCompute_WaitingAndOutstandingLines(t *testing.T) {
	b := mustCompute(t, Input{
		Rule: autoRule, DistanceMeters: 5500, WaitingSeconds: 6*60 + 59, // 6 min -> 3 chargeable
		OutstandingPaise: 1500, OutstandingIDs: []string{"o1"},
		Tax: flat(), InvoiceDate: onDate,
	})
	if b.WaitingMinutes != 3 || b.WaitingChargePaise != 450 {
		t.Fatalf("waiting %+v", b)
	}
	if b.OutstandingPaise != 1500 || len(b.OutstandingIDs) != 1 {
		t.Fatalf("outstanding %+v", b)
	}
	refs := []string{}
	for _, l := range b.TaxLines {
		refs = append(refs, l.Ref)
	}
	if len(refs) != 4 || refs[2] != RefWaitingCharge || refs[3] != RefOutstanding {
		t.Fatalf("refs %v", refs)
	}
	// 5% of 9100=455, 18% of 500=90, 5% of 450=23 (22.5 half-up), 5% of 1500=75.
	if b.TaxPaise != 455+90+23+75 || b.TotalPaise != 9100+500+450+1500+b.TaxPaise {
		t.Fatalf("totals %+v", b)
	}
}

func TestCompute_RideFareCap(t *testing.T) {
	b := mustCompute(t, Input{Rule: autoRule, DistanceMeters: 50000, RideFareCapPaise: 18200, Tax: flat(), InvoiceDate: onDate})
	if !b.CapApplied || b.RideFarePaise != 18200 {
		t.Fatalf("cap %+v", b)
	}
}

func TestCompute_RejectsBadInput(t *testing.T) {
	if _, err := Compute(Input{Rule: autoRule, DistanceMeters: -1, Tax: flat()}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("negative distance: %v", err)
	}
	if _, err := Compute(Input{Rule: autoRule, DistanceMeters: 1}); !errors.Is(err, ErrNoTax) {
		t.Fatalf("no tax: %v", err)
	}
}

func TestCompute_GSTComputerMatchesSharedGST(t *testing.T) {
	g, err := NewGSTComputer(nil, TestGSTIN, "29")
	if err != nil {
		t.Fatalf("gst computer: %v", err)
	}
	b := mustCompute(t, Input{Rule: autoRule, DistanceMeters: 5500, Tax: g, InvoiceDate: onDate})
	if b.TaxPaise != 545 || b.TotalPaise != 10145 {
		t.Fatalf("gst totals %+v", b)
	}
	if b.TaxLines[0].SAC != "996412" || b.TaxLines[0].Category != "PASSENGER_TRANSPORT_VIA_ECO" || b.TaxLines[0].RateBPS != 500 {
		t.Fatalf("ride line %+v", b.TaxLines[0])
	}
	if b.TaxLines[1].Category != "PLATFORM_FEE" || b.TaxLines[1].RateBPS != 1800 {
		t.Fatalf("fee line %+v", b.TaxLines[1])
	}
	if _, err := NewGSTComputer(nil, "not-a-gstin", "29"); !errors.Is(err, ErrPlatformGSTIN) {
		t.Fatalf("bad gstin: %v", err)
	}
	if code, ok := StateCodeForName("Karnataka"); !ok || code != "29" {
		t.Fatalf("state code: %q %v", code, ok)
	}
	if _, ok := StateCodeForName("Atlantis"); ok {
		t.Fatal("unknown state accepted")
	}
}

func TestCancellationBreakdown(t *testing.T) {
	b, err := CancellationBreakdown(1500, flat(), onDate)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.TaxLines) != 1 || b.TaxLines[0].Ref != RefCancellationFee || b.TaxPaise != 75 || b.TotalPaise != 1575 {
		t.Fatalf("cancellation %+v", b)
	}
	if b, _ := CancellationBreakdown(0, flat(), onDate); b.TotalPaise != 0 || len(b.TaxLines) != 0 {
		t.Fatalf("zero fee %+v", b)
	}
}
