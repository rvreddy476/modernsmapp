package pricing

import (
	"testing"
	"time"
)

func ist(y int, m time.Month, d, hh, mm int) time.Time {
	return LocalTime(time.Date(y, m, d, hh, mm, 0, 0, time.FixedZone("IST", 19800)), "Asia/Kolkata")
}

func seedWindows() []Window {
	from := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	return []Window{
		{ID: "m", Name: "Morning peak", DaysOfWeek: Weekdays, StartMinute: 480, EndMinute: 630, MultiplierBPS: 12500, Priority: 10, IsActive: true, EffectiveFrom: from},
		{ID: "e", Name: "Evening peak", DaysOfWeek: Weekdays, StartMinute: 1050, EndMinute: 1230, MultiplierBPS: 12500, Priority: 10, IsActive: true, EffectiveFrom: from},
		{ID: "n", Name: "Night", VehicleType: "auto", DaysOfWeek: EveryDay, StartMinute: 1380, EndMinute: 300, MultiplierBPS: 12000, Priority: 5, IsActive: true, EffectiveFrom: from},
	}
}

func TestSelectWindow_OnlyInsideItsHours(t *testing.T) {
	ws := seedWindows()
	// Friday 18 Sep 2026.
	if w := SelectWindow(ws, "auto", ist(2026, 9, 18, 9, 0)); w == nil || w.Name != "Morning peak" {
		t.Fatalf("09:00 Friday: %+v", w)
	}
	if w := SelectWindow(ws, "auto", ist(2026, 9, 18, 10, 30)); w != nil {
		t.Fatalf("10:30 is outside the morning window (end exclusive): %+v", w)
	}
	if w := SelectWindow(ws, "auto", ist(2026, 9, 18, 12, 0)); w != nil {
		t.Fatalf("noon has no window: %+v", w)
	}
	if w := SelectWindow(ws, "auto", ist(2026, 9, 18, 18, 0)); w == nil || w.Name != "Evening peak" {
		t.Fatalf("18:00 Friday: %+v", w)
	}
	// Saturday 19 Sep: no peak.
	if w := SelectWindow(ws, "auto", ist(2026, 9, 19, 9, 0)); w != nil {
		t.Fatalf("Saturday morning must not be peak: %+v", w)
	}
}

func TestSelectWindow_WrapsMidnightAndVehicleScoped(t *testing.T) {
	ws := seedWindows()
	if w := SelectWindow(ws, "auto", ist(2026, 9, 18, 23, 30)); w == nil || w.Name != "Night" {
		t.Fatalf("23:30 auto: %+v", w)
	}
	if w := SelectWindow(ws, "auto", ist(2026, 9, 19, 2, 0)); w == nil || w.Name != "Night" {
		t.Fatalf("02:00 auto: %+v", w)
	}
	if w := SelectWindow(ws, "auto", ist(2026, 9, 19, 5, 0)); w != nil {
		t.Fatalf("05:00 is outside the night window: %+v", w)
	}
	if w := SelectWindow(ws, "sedan", ist(2026, 9, 18, 23, 30)); w != nil {
		t.Fatalf("night is auto/bike only: %+v", w)
	}
}

func TestSelectWindow_PriorityWinsNeverStacks(t *testing.T) {
	ws := seedWindows()
	// A night window that overlaps the evening peak on Friday 20:00-20:30 has
	// lower priority, so the peak wins; the multipliers are not summed.
	ws = append(ws, Window{ID: "x", Name: "Late", DaysOfWeek: EveryDay, StartMinute: 1200, EndMinute: 1440, MultiplierBPS: 13000, Priority: 1, IsActive: true, EffectiveFrom: ws[0].EffectiveFrom})
	w := SelectWindow(ws, "auto", ist(2026, 9, 18, 20, 15))
	if w == nil || w.Name != "Evening peak" {
		t.Fatalf("priority: %+v", w)
	}
	if got := WindowExtraBPS(w); got != 2500 {
		t.Fatalf("extra bps %d", got)
	}
}

func TestSelectWindow_InactiveAndExpiredIgnored(t *testing.T) {
	ws := seedWindows()
	ws[0].IsActive = false
	if w := SelectWindow(ws, "auto", ist(2026, 9, 18, 9, 0)); w != nil {
		t.Fatalf("inactive window applied: %+v", w)
	}
	ws[0].IsActive = true
	to := time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)
	ws[0].EffectiveTo = &to
	if w := SelectWindow(ws, "auto", ist(2026, 9, 18, 9, 0)); w != nil {
		t.Fatalf("expired window applied: %+v", w)
	}
}

func TestDayBit(t *testing.T) {
	if DayBit(time.Monday) != Monday || DayBit(time.Sunday) != Sunday || DayBit(time.Saturday) != Saturday {
		t.Fatalf("bits %d %d %d", DayBit(time.Monday), DayBit(time.Sunday), DayBit(time.Saturday))
	}
}

func TestDemandBPS_StepsAndCap(t *testing.T) {
	cases := []struct {
		req, online int
		cap, want   int64
	}{
		{0, 0, 5000, 0},
		{3, 5, 5000, 0},    // 0.6
		{5, 5, 5000, 2000}, // 1.0
		{9, 5, 5000, 2000}, // 1.8
		{10, 5, 5000, 4000},
		{15, 5, 5000, 5000},
		{100, 1, 5000, 5000},
		{100, 0, 5000, 5000}, // zero partners count as one
		{100, 0, 3000, 3000}, // cap from env
		{7, 5, 1000, 1000},   // a step above the cap is capped
	}
	for _, c := range cases {
		if got := DemandBPS(c.req, c.online, c.cap); got != c.want {
			t.Errorf("DemandBPS(%d,%d,%d) = %d, want %d", c.req, c.online, c.cap, got, c.want)
		}
	}
}

func TestEffectiveSurge_MaxNeverSum(t *testing.T) {
	peak := &Window{Name: "Morning peak", MultiplierBPS: 12500}
	if bps, reason, name := EffectiveSurge(peak, 2000); bps != 2500 || reason != SurgePeakHours || name != "Morning peak" {
		t.Fatalf("window wins: %d %s %s", bps, reason, name)
	}
	if bps, reason, _ := EffectiveSurge(peak, 4000); bps != 4000 || reason != SurgeHighDemand {
		t.Fatalf("demand wins: %d %s", bps, reason)
	}
	if bps, reason, name := EffectiveSurge(nil, 0); bps != 0 || reason != SurgeNone || name != "" {
		t.Fatalf("none: %d %s %s", bps, reason, name)
	}
	if bps, reason, _ := EffectiveSurge(peak, 2500); bps != 2500 || reason != SurgePeakHours {
		t.Fatalf("tie goes to the window: %d %s", bps, reason)
	}
}

func TestWaitingCharge(t *testing.T) {
	r := autoRule
	cases := []struct {
		secs      int
		wantMin   int
		wantPaise int64
	}{
		{0, 0, 0}, {179, 0, 0}, {180, 0, 0}, {239, 0, 0}, {240, 1, 150}, {419, 3, 450}, {600, 7, 1050},
	}
	for _, c := range cases {
		m, p := WaitingCharge(r, c.secs)
		if m != c.wantMin || p != c.wantPaise {
			t.Errorf("WaitingCharge(%d) = %d min %d p, want %d %d", c.secs, m, p, c.wantMin, c.wantPaise)
		}
	}
}

func TestCancellationFee(t *testing.T) {
	r := autoRule
	now := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	early := now.Add(-60 * time.Second)
	late := now.Add(-121 * time.Second)
	if fee := CancellationFee(r, CancelByCustomer, nil, now); fee != 0 {
		t.Fatalf("no partner: %d", fee)
	}
	if fee := CancellationFee(r, CancelByCustomer, &early, now); fee != 0 {
		t.Fatalf("inside free window: %d", fee)
	}
	if fee := CancellationFee(r, CancelByCustomer, &late, now); fee != 1500 {
		t.Fatalf("after free window: %d", fee)
	}
	if fee := CancellationFee(r, CancelNoShow, &late, now); fee != 1500 {
		t.Fatalf("no-show: %d", fee)
	}
	for _, by := range []string{CancelByPartner, CancelByAdmin, CancelBySystem} {
		if fee := CancellationFee(r, by, &late, now); fee != 0 {
			t.Fatalf("%s cancels: %d", by, fee)
		}
	}
}

func TestTrackedDistance_FiltersImpossibleHops(t *testing.T) {
	t0 := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	pts := []TrackPoint{
		{12.9716, 77.5946, t0},
		{12.9750, 77.5980, t0.Add(60 * time.Second)}, // ~530 m
		{13.0750, 77.7000, t0.Add(65 * time.Second)}, // ~16 km in 5 s: teleport, ignored
		{13.0760, 77.7010, t0.Add(125 * time.Second)},
	}
	got := TrackedDistanceMeters(pts)
	if got < 600 || got > 750 {
		t.Fatalf("tracked %d m, want ~530 + ~155", got)
	}
	if TrackedDistanceMeters(nil) != 0 || TrackedDistanceMeters(pts[:1]) != 0 {
		t.Fatal("empty tracks must be 0")
	}
}

func TestDeviates(t *testing.T) {
	cases := []struct {
		quoted, tracked int
		want            bool
	}{
		{5000, 5000, false},
		{5000, 5900, false}, // +18%
		{5000, 6100, false}, // +22% but only 1.1 km? 6100-5000 = 1100 > 1000 and 22% -> true
		{5000, 6000, false}, // +20% exactly and exactly 1 km: neither strictly more
		{2000, 3500, true},
		{10000, 11500, false}, // +15% though 1.5 km
		{3000, 4500, true},
	}
	cases[2].want = true
	for _, c := range cases {
		if got := Deviates(c.quoted, c.tracked); got != c.want {
			t.Errorf("Deviates(%d,%d) = %v, want %v", c.quoted, c.tracked, got, c.want)
		}
	}
}

func TestCouponDiscountFor(t *testing.T) {
	pct := &Coupon{DiscountType: CouponPercent, PercentBPS: 5000, MaxDiscountPaise: 3000, MinFarePaise: 5000}
	if d := pct.DiscountFor(4999); d != 0 {
		t.Fatalf("below min fare: %d", d)
	}
	if d := pct.DiscountFor(5000); d != 2500 {
		t.Fatalf("50%%: %d", d)
	}
	if d := pct.DiscountFor(10000); d != 3000 {
		t.Fatalf("capped: %d", d)
	}
	flatC := &Coupon{DiscountType: CouponFlat, ValuePaise: 7000}
	if d := flatC.DiscountFor(5000); d != 5000 {
		t.Fatalf("flat never exceeds the fare: %d", d)
	}
	var nilC *Coupon
	if nilC.DiscountFor(5000) != 0 {
		t.Fatal("nil coupon")
	}
}
