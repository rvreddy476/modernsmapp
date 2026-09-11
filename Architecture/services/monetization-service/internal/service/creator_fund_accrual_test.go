package service

import (
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// One flick view at 300 paise per 1000 is 0.3 paise = 300,000 micro-paise.
// The old per-step truncation made it zero; the micro-paise figure keeps
// it, and the frozen 1.0x multiplier leaves it exactly alone.
func TestComputeGrossMicroPaiseKeepsSubPaiseAmounts(t *testing.T) {
	if got := ComputeGrossMicroPaise(1, 300, NeutralMultiplierBps); got != 300_000 {
		t.Fatalf("one flick view = %d micro-paise, want 300000", got)
	}
	// 4,230 long_video views at Rs 50 per 1000 is Rs 211.50 = 21,150 paise:
	// the 2026-09-07 row, reproduced exactly in micro-paise.
	if got := ComputeGrossMicroPaise(4_230, 5_000, NeutralMultiplierBps); got != 21_150*MicroPaisePerPaise {
		t.Fatalf("4230 long_video views = %d micro-paise, want %d", got, 21_150*MicroPaisePerPaise)
	}
	// The multiplier scales in basis points without losing the remainder
	// the old formula lost: 0.85x of 0.3 paise is 0.255 paise.
	if got := ComputeGrossMicroPaise(1, 300, 8_500); got != 255_000 {
		t.Fatalf("0.85x of one flick view = %d, want 255000", got)
	}
	for _, bad := range [][3]int64{{0, 300, 10_000}, {5, 0, 10_000}, {5, 300, 0}, {-1, 300, 10_000}} {
		if got := ComputeGrossMicroPaise(bad[0], bad[1], bad[2]); got != 0 {
			t.Errorf("ComputeGrossMicroPaise(%v) = %d, want 0", bad, got)
		}
	}
	// Whole-paise inputs agree with the old integer formula for every
	// multiplier the launch band can produce.
	for _, bps := range []int64{8_500, 9_000, 10_000, 11_000, 12_500} {
		old := ApplyQualityMultiplier(ComputeGrossPaise(10_000, 5_000), bps)
		if got := ComputeGrossMicroPaise(10_000, 5_000, bps); got != old*MicroPaisePerPaise {
			t.Errorf("bps %d: micro %d != old formula %d paise", bps, got, old)
		}
	}
}

// The carry boundary is the ONLY truncation: paise out plus the carry is
// the total exactly, and four 0.3-paise days pay one paise on the fourth.
func TestApplyCarryTruncatesOnceAndCarriesTheRest(t *testing.T) {
	carry := int64(0)
	var paid int64
	for day := 1; day <= 4; day++ {
		gross, next := ApplyCarry(300_000, carry)
		if gross*MicroPaisePerPaise+next != 300_000+carry {
			t.Fatalf("day %d: %d paise + %d carry != %d total", day, gross, next, 300_000+carry)
		}
		paid += gross
		carry = next
		if day < 4 && gross != 0 {
			t.Fatalf("day %d paid %d paise early", day, gross)
		}
	}
	if paid != 1 || carry != 200_000 {
		t.Fatalf("after four days paid %d paise with %d carried, want 1 and 200000", paid, carry)
	}
	if gross, next := ApplyCarry(-5, -5); gross != 0 || next != 0 {
		t.Errorf("negative inputs are clamped, got %d/%d", gross, next)
	}
}

// The pricing step under the locks: uncapped, capped, exhausted.
func TestPriceAccrualDayHonoursTheBudget(t *testing.T) {
	const fee = int64(3_000)
	// Uncapped: full accrual, remainder carried.
	p := PriceAccrualDay(1_500_000, 200_000, nil, fee)
	if p.GrossPaise != 1 || p.CarryOutMicroPaise != 700_000 || p.SkipReason != "" {
		t.Fatalf("uncapped = %+v", p)
	}
	if p.NetPaise+p.PlatformFeePaise != p.GrossPaise {
		t.Fatalf("split does not add up: %+v", p)
	}
	// Capped: exactly what remains, marked, carry dropped.
	remaining := int64(3_000)
	p = PriceAccrualDay(5_000*MicroPaisePerPaise, 999_999, &remaining, fee)
	if p.GrossPaise != 3_000 || p.SkipReason != SkipBudgetCapped || p.CarryOutMicroPaise != 0 {
		t.Fatalf("capped = %+v", p)
	}
	net, feePaise := SplitEarnings(3_000, fee)
	if p.NetPaise != net || p.PlatformFeePaise != feePaise {
		t.Fatalf("capped split = %+v, want net %d fee %d", p, net, feePaise)
	}
	// Fits under the cap: untouched.
	remaining = 5_000
	p = PriceAccrualDay(5_000*MicroPaisePerPaise, 0, &remaining, fee)
	if p.GrossPaise != 5_000 || p.SkipReason != "" {
		t.Fatalf("fits = %+v", p)
	}
	// Exhausted: zero row, carry untouched.
	remaining = 0
	p = PriceAccrualDay(5_000*MicroPaisePerPaise, 123_456, &remaining, fee)
	if p.GrossPaise != 0 || p.NetPaise != 0 || p.SkipReason != SkipBudgetExhausted || p.CarryOutMicroPaise != 123_456 {
		t.Fatalf("exhausted = %+v", p)
	}
}

// Short-form earns as flick, long-form as long_video, and nothing else
// earns. Must agree with the shared postclassify rule.
func TestCanonicalMonetizationTypeMapsLegacyNames(t *testing.T) {
	cases := map[string]struct {
		want string
		ok   bool
	}{
		"flick": {"flick", true}, "reel": {"flick", true}, "short": {"flick", true},
		"long_video": {"long_video", true}, "video": {"long_video", true},
		"post": {"", false}, "image": {"", false}, "": {"", false}, "unknown": {"", false},
	}
	for in, c := range cases {
		got, ok := canonicalMonetizationType(in)
		if got != c.want || ok != c.ok {
			t.Errorf("canonicalMonetizationType(%q) = %q,%v want %q,%v", in, got, ok, c.want, c.ok)
		}
	}
}

// Rows of legacy types fold into their canonical group, the quality score
// is view-weighted, and the revision changes when anything does —
// including a rewrite that only restamped updated_at.
func TestAggregateDailyInputsGroupsAndFingerprints(t *testing.T) {
	now := time.Date(2026, 9, 7, 3, 0, 0, 0, time.UTC)
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	rows := []postgres.DailyInputRow{
		{ContentID: a, ContentType: "flick", ViewsDisplay: 100, WatchTimeMs: 1000, Impressions: 500, CQS: 0.2, UpdatedAt: now},
		{ContentID: b, ContentType: "reel", ViewsDisplay: 300, WatchTimeMs: 3000, Impressions: 900, CQS: 0.6, UpdatedAt: now},
		{ContentID: c, ContentType: "post", ViewsDisplay: 7, Impressions: 10, UpdatedAt: now},
	}
	groups, unsupported := AggregateDailyInputs(rows)
	if len(groups) != 1 || groups[0].Metric.ContentType != "flick" {
		t.Fatalf("groups = %+v", groups)
	}
	m := groups[0].Metric
	if m.ViewCount != 400 || m.WatchTimeMs != 4000 || m.Impressions != 1400 {
		t.Errorf("sums = %+v", m)
	}
	// (0.2*100 + 0.6*300) / 400 = 0.5
	if m.AvgCQS < 0.4999 || m.AvgCQS > 0.5001 {
		t.Errorf("view-weighted CQS = %v, want 0.5", m.AvgCQS)
	}
	if len(unsupported) != 1 || unsupported[0].RawType != "post" || unsupported[0].Views != 7 {
		t.Errorf("unsupported = %+v", unsupported)
	}
	if len(groups[0].RawTypes) != 2 {
		t.Errorf("raw types folded into flick = %v, want [flick reel]", groups[0].RawTypes)
	}

	rev := groups[0].Revision
	if len(rev) != 64 {
		t.Fatalf("revision = %q", rev)
	}
	// Same rows in another order: same revision.
	swapped := []postgres.DailyInputRow{rows[1], rows[0]}
	if got := InputRevision(swapped); got != rev {
		t.Error("revision depends on row order")
	}
	// A changed value: a different revision.
	changed := []postgres.DailyInputRow{rows[0], rows[1]}
	changed[1].ViewsDisplay++
	if got := InputRevision(changed); got == rev {
		t.Error("revision ignores a changed value")
	}
}

// The revision fingerprints VALUES, not row timestamps. Phase 1E's rollup
// is delete-then-insert inside a 48-hour window, so a day accrued at 03:00
// is routinely rewritten with identical values and a fresh updated_at;
// that must not read as "the inputs changed". A changed value must.
func TestInputRevisionIgnoresUpdatedAtButNotValues(t *testing.T) {
	day := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	a, b := uuid.New(), uuid.New()
	first := []postgres.DailyInputRow{
		{ContentID: a, DayBucket: day, ContentType: "long_video", ViewsDisplay: 4230, WatchTimeMs: 9_000_000, Impressions: 20_000, CQS: 0.41, UpdatedAt: day.Add(3 * time.Hour)},
		{ContentID: b, DayBucket: day, ContentType: "long_video", ViewsDisplay: 12, WatchTimeMs: 40_000, Impressions: 90, CQS: 0.2, UpdatedAt: day.Add(3 * time.Hour)},
	}
	rewritten := []postgres.DailyInputRow{first[1], first[0]} // reordered too
	rewritten[0].UpdatedAt = day.Add(20 * time.Hour)
	rewritten[1].UpdatedAt = day.Add(20 * time.Hour)
	if InputRevision(first) != InputRevision(rewritten) {
		t.Fatalf("revision changed when only updated_at (and row order) changed:\n %s\n %s", InputRevision(first), InputRevision(rewritten))
	}
	changed := []postgres.DailyInputRow{first[0], first[1]}
	changed[0].ViewsDisplay++
	if InputRevision(first) == InputRevision(changed) {
		t.Fatal("revision did not change when views_display changed")
	}
	otherDay := []postgres.DailyInputRow{first[0], first[1]}
	otherDay[0].DayBucket = day.AddDate(0, 0, 1)
	if InputRevision(first) == InputRevision(otherDay) {
		t.Fatal("revision did not change when day_bucket changed")
	}
}

// Reversed rows and pre-revision rows are exempt from the check; a live
// row whose inputs moved is not.
func TestCheckInputRevisionExemptsReversedAndUnversionedRows(t *testing.T) {
	cur := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	old := "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	if err := checkInputRevision(nil, cur); err != nil {
		t.Error(err)
	}
	if err := checkInputRevision(&postgres.CreatorFundEarning{Status: "settled", InputRevision: ""}, cur); err != nil {
		t.Errorf("pre-019 row: %v", err)
	}
	if err := checkInputRevision(&postgres.CreatorFundEarning{Status: "reversed", InputRevision: old}, cur); err != nil {
		t.Errorf("reversed row: %v", err)
	}
	if err := checkInputRevision(&postgres.CreatorFundEarning{Status: "settled", InputRevision: cur}, cur); err != nil {
		t.Errorf("unchanged row: %v", err)
	}
	if err := checkInputRevision(&postgres.CreatorFundEarning{Status: "settled", InputRevision: old, DayBucket: time.Now()}, cur); err == nil {
		t.Error("changed inputs on a settled row were accepted")
	}
}
