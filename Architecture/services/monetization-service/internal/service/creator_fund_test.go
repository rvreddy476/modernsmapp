package service

import (
	"testing"
)

// TestDecideEligibility — gate model is "content count is a floor, then
// ANY of (view-score, watch-time) lifts you in". Re-asserting it here so
// later tweaks don't accidentally widen the gate (e.g. drop the floor
// or AND the two thresholds).
func TestDecideEligibility(t *testing.T) {
	cfg := DefaultCreatorFundConfig()

	cases := []struct {
		name     string
		views    float64
		watchMs  int64
		count    int
		want     string
		viewMet  bool
		watchMet bool
		countMet bool
	}{
		{"empty", 0, 0, 0, "ineligible", false, false, false},
		{"only views, below content floor", 5000, 0, 1, "ineligible", true, false, false},
		{"only watch-time, below content floor", 0, 200_000_000, 2, "ineligible", false, true, false},
		{"content floor met, no metric thresh", 0, 0, 5, "ineligible", false, false, true},
		{"content floor + views", 2000, 0, 5, "eligible", true, false, true},
		{"content floor + watch-time", 0, 50_000_000, 5, "eligible", false, true, true},
		{"all three", 5000, 50_000_000, 10, "eligible", true, true, true},
		{"exactly at threshold", cfg.EligibilityViewScore, cfg.EligibilityWatchTimeMs, cfg.EligibilityContentCount, "eligible", true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := DecideEligibility(tc.views, tc.watchMs, tc.count, cfg)
			if d.Status != tc.want {
				t.Errorf("status: got %s, want %s (decision=%+v)", d.Status, tc.want, d)
			}
			if d.MetViewScoreThreshold != tc.viewMet {
				t.Errorf("met view: got %v, want %v", d.MetViewScoreThreshold, tc.viewMet)
			}
			if d.MetWatchTimeThreshold != tc.watchMet {
				t.Errorf("met watch: got %v, want %v", d.MetWatchTimeThreshold, tc.watchMet)
			}
			if d.MetContentCountThreshold != tc.countMet {
				t.Errorf("met count: got %v, want %v", d.MetContentCountThreshold, tc.countMet)
			}
		})
	}
}

// TestComputeGrossPaise — paise-precision integer multiplication with
// implicit floor at /1000. Re-asserts that we never accidentally use
// floats and round away creator earnings.
func TestComputeGrossPaise(t *testing.T) {
	cases := []struct {
		name  string
		views int64
		rpm   int64
		want  int64
	}{
		{"zero views", 0, 5000, 0},
		{"zero rpm", 1000, 0, 0},
		{"negative views", -1, 5000, 0},
		{"flat 1000 views @ 5000 rpm", 1000, 5000, 5000},
		{"500 views @ 5000 rpm rounds down to 2500", 500, 5000, 2500},
		{"999 views @ 5000 rpm rounds down (4995)", 999, 5000, 4995},
		{"1 view @ 5000 rpm rounds down to 5", 1, 5000, 5},
		{"1 view @ 300 rpm rounds down to 0", 1, 300, 0},
		{"100 views @ 300 rpm = 30 paise", 100, 300, 30},
		{"1M views @ 5000 rpm = 5M paise (₹50k)", 1_000_000, 5000, 5_000_000},
		{"1B views @ 5000 rpm doesn't overflow", 1_000_000_000, 5000, 5_000_000_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeGrossPaise(tc.views, tc.rpm)
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// TestSplitEarnings — net + fee == gross, always. The basis-points math
// floors the fee so the creator never loses paise to rounding (any
// 0.x paise edge case goes to the creator). This is a deliberate choice.
func TestSplitEarnings(t *testing.T) {
	cases := []struct {
		name    string
		gross   int64
		feeBps  int64
		wantNet int64
		wantFee int64
	}{
		{"zero gross", 0, 3000, 0, 0},
		{"negative gross treated as zero", -100, 3000, 0, 0},
		{"clean 30%", 10000, 3000, 7000, 3000},
		{"odd value rounds fee down", 100, 3333, 67, 33}, // fee = 100*3333/10000 = 33.33 → 33
		{"100% to platform", 1000, 10000, 0, 1000},
		{"0% to platform", 1000, 0, 1000, 0},
		{"1 paise gross, 30% fee → 1 net, 0 fee", 1, 3000, 1, 0},
		{"net + fee always equals gross", 999_999_999, 3000, 999_999_999 - (999_999_999 * 3000 / 10000), 999_999_999 * 3000 / 10000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			net, fee := SplitEarnings(tc.gross, tc.feeBps)
			if net != tc.wantNet {
				t.Errorf("net: got %d, want %d", net, tc.wantNet)
			}
			if fee != tc.wantFee {
				t.Errorf("fee: got %d, want %d", fee, tc.wantFee)
			}
			// Hard invariant for non-zero gross: parts must sum to whole.
			if tc.gross > 0 && net+fee != tc.gross {
				t.Errorf("split invariant violated: net=%d + fee=%d != gross=%d", net, fee, tc.gross)
			}
		})
	}
}

// TestSplitEarningsInvariant — sweep a wider range to make sure the
// "net + fee == gross" promise holds for every paise boundary near a
// 30% fee. Catches off-by-one errors that would otherwise only show up
// at scale.
func TestSplitEarningsInvariant(t *testing.T) {
	for gross := int64(1); gross <= 10000; gross++ {
		net, fee := SplitEarnings(gross, 3000)
		if net+fee != gross {
			t.Fatalf("invariant violated at gross=%d: net=%d, fee=%d", gross, net, fee)
		}
		if net < 0 || fee < 0 {
			t.Fatalf("negative split at gross=%d: net=%d, fee=%d", gross, net, fee)
		}
	}
}

// TestEarningsFormulaEndToEnd composes ComputeGrossPaise → SplitEarnings
// the same way SettleCreatorFundDay does, and asserts the full numeric
// chain for the canonical scenarios the studio dashboard will quote
// to creators. A regression here means a creator's "you earned X
// today" number is wrong — the worst kind of monetization bug.
//
// Reference scenarios use the launch-baseline rates seeded by
// migration 010:
//   - long_video: 5000 paise per 1000 views (₹50/CPM)
//   - flick:       300 paise per 1000 views (₹3/CPM)
//
// At the default 30% platform fee:
func TestEarningsFormulaEndToEnd(t *testing.T) {
	const platformFeeBps = int64(3000) // 30%

	cases := []struct {
		name        string
		views       int64
		rpmPaise    int64
		wantGross   int64
		wantFee     int64
		wantNet     int64
	}{
		{
			name:      "long_video, 1000 views @ ₹50 CPM",
			views:     1000,
			rpmPaise:  5000,
			wantGross: 5000, // ₹50.00
			wantFee:   1500, // ₹15.00
			wantNet:   3500, // ₹35.00 to creator
		},
		{
			name:      "long_video, 100k views @ ₹50 CPM (a small viral)",
			views:     100_000,
			rpmPaise:  5000,
			wantGross: 500_000,  // ₹5,000
			wantFee:   150_000,  // ₹1,500
			wantNet:   350_000,  // ₹3,500 to creator
		},
		{
			name:      "long_video, 1M views @ ₹50 CPM (real viral)",
			views:     1_000_000,
			rpmPaise:  5000,
			wantGross: 5_000_000,  // ₹50,000
			wantFee:   1_500_000,  // ₹15,000
			wantNet:   3_500_000,  // ₹35,000 to creator
		},
		{
			name:      "flick, 1000 views @ ₹3 CPM",
			views:     1000,
			rpmPaise:  300,
			wantGross: 300, // ₹3.00
			wantFee:   90,
			wantNet:   210,
		},
		{
			name:      "flick, 1M views @ ₹3 CPM",
			views:     1_000_000,
			rpmPaise:  300,
			wantGross: 300_000, // ₹3,000
			wantFee:   90_000,
			wantNet:   210_000,
		},
		{
			name:      "tiny: 1 view earns nothing because of /1000 floor",
			views:     1,
			rpmPaise:  5000,
			wantGross: 5,
			wantFee:   1, // 5 * 3000 / 10000 = 1
			wantNet:   4,
		},
		{
			name:      "no rate configured for content_type → no earnings",
			views:     1_000_000,
			rpmPaise:  0,
			wantGross: 0,
			wantFee:   0,
			wantNet:   0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gross := ComputeGrossPaise(tc.views, tc.rpmPaise)
			net, fee := SplitEarnings(gross, platformFeeBps)
			if gross != tc.wantGross {
				t.Errorf("gross: got %d, want %d", gross, tc.wantGross)
			}
			if fee != tc.wantFee {
				t.Errorf("fee: got %d, want %d", fee, tc.wantFee)
			}
			if net != tc.wantNet {
				t.Errorf("net: got %d, want %d", net, tc.wantNet)
			}
			// Hard ledger invariant: every earning row must satisfy
			// gross == net + fee. Already covered by
			// TestSplitEarningsInvariant for arbitrary gross, but
			// re-asserted here for the composed values.
			if gross > 0 && gross != net+fee {
				t.Errorf("ledger invariant: gross=%d != net+fee=%d", gross, net+fee)
			}
		})
	}
}

// TestEarningsRoundingFavorsCreator — when the platform-fee math
// floors and net = gross - fee, any 0.x paise rounding goes to the
// creator. Re-asserting because the alternative (rounding the net
// down, fee up) would be a creator-hostile silent change.
func TestEarningsRoundingFavorsCreator(t *testing.T) {
	// With gross=100, feeBps=3333 (33.33%), the literal split is
	// 33.33 platform / 66.67 creator. Integer math floors fee to 33,
	// net = 100 - 33 = 67. Creator gets the leftover 0.33 paise.
	gross := int64(100)
	feeBps := int64(3333)
	net, fee := SplitEarnings(gross, feeBps)
	if fee != 33 {
		t.Errorf("fee floor: got %d, want 33", fee)
	}
	if net != 67 {
		t.Errorf("net (creator-favored): got %d, want 67", net)
	}
	if net+fee != gross {
		t.Errorf("ledger invariant violated")
	}
}
