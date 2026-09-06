package service

import (
	"math"
	"strings"
	"testing"
)

// enoughImpressions is a realistic "plenty of evidence" count.
const enoughImpressions = int64(1_000_000)

// pureBand is the launch band with the confidence shrink switched off,
// so a test can assert the curve itself to the exact basis point. The
// shrink is asserted separately in
// TestLowImpressionScoresAreShrunkTowardNeutral.
func pureBand() QualityBand {
	b := DefaultQualityBand()
	b.ConfidenceImpressions = 0
	return b
}

func TestQualityMultiplierHitsFloorMiddleAndCeiling(t *testing.T) {
	band := pureBand()

	cases := []struct {
		name string
		cqs  float64
		want int64
	}{
		// A creator whose content scores zero still keeps 85% of the
		// plain views x RPM amount. This is the whole point of a band:
		// genuine views are never worth nothing.
		{"floor at cqs 0", 0.0, 8500},
		// The pivot is neutral by construction — paid exactly what the
		// pre-quality formula paid.
		{"neutral at pivot", 0.35, 10000},
		// Halfway from zero to the pivot is halfway from floor to 1.0x.
		{"halfway below pivot", 0.175, 9250},
		// Halfway from pivot to 1.0 is halfway from 1.0x to ceiling.
		{"halfway above pivot", 0.675, 11250},
		{"ceiling at cqs 1", 1.0, 12500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeQualityMultiplierBps(tc.cqs, enoughImpressions, band)
			if got != tc.want {
				t.Fatalf("cqs=%.3f multiplier=%d want=%d", tc.cqs, got, tc.want)
			}
		})
	}
}

func TestQualityMultiplierIsMonotonicAndStaysInsideTheBand(t *testing.T) {
	band := pureBand()
	prev := int64(math.MinInt64)
	for i := 0; i <= 100; i++ {
		cqs := float64(i) / 100.0
		got := ComputeQualityMultiplierBps(cqs, enoughImpressions, band)
		if got < band.FloorBps || got > band.CeilingBps {
			t.Fatalf("cqs=%.2f multiplier=%d escaped band [%d,%d]", cqs, got, band.FloorBps, band.CeilingBps)
		}
		if got < prev {
			t.Fatalf("cqs=%.2f multiplier=%d went down from %d", cqs, got, prev)
		}
		prev = got
	}
}

func TestQualityMultiplierClampsScoresOutsideZeroToOne(t *testing.T) {
	band := pureBand()
	if got := ComputeQualityMultiplierBps(-5, enoughImpressions, band); got != band.FloorBps {
		t.Fatalf("negative cqs multiplier=%d want floor=%d", got, band.FloorBps)
	}
	if got := ComputeQualityMultiplierBps(7, enoughImpressions, band); got != band.CeilingBps {
		t.Fatalf("cqs above 1 multiplier=%d want ceiling=%d", got, band.CeilingBps)
	}
	if got := ComputeQualityMultiplierBps(math.NaN(), enoughImpressions, band); got != NeutralMultiplierBps {
		t.Fatalf("NaN cqs multiplier=%d want neutral=%d", got, NeutralMultiplierBps)
	}
}

// A creator with no impressions recorded must not be divided by zero and
// must not be punished for the absence of data — the score is unknown,
// not bad. They get the neutral 1.0x.
func TestZeroImpressionCreatorIsNeitherDividedByZeroNorPenalised(t *testing.T) {
	band := DefaultQualityBand()

	if got := ShrinkCQS(0, 0, band); got != band.PivotCQS {
		t.Fatalf("shrink with zero impressions = %.4f, want pivot %.4f", got, band.PivotCQS)
	}
	if got := ComputeQualityMultiplierBps(0, 0, band); got != NeutralMultiplierBps {
		t.Fatalf("zero-impression multiplier=%d want neutral=%d", got, NeutralMultiplierBps)
	}
	// A perfect score with no impressions behind it earns no bonus either.
	if got := ComputeQualityMultiplierBps(1.0, 0, band); got != NeutralMultiplierBps {
		t.Fatalf("zero-impression perfect-score multiplier=%d want neutral=%d", got, NeutralMultiplierBps)
	}
	// And the whole payout path survives it.
	gross, base, bps := ComputeQualityAdjustedGrossPaise(10_000, 5000, 0, 0, band)
	if base != 50_000 {
		t.Fatalf("base gross=%d want 50000", base)
	}
	if bps != NeutralMultiplierBps || gross != base {
		t.Fatalf("zero-impression payout gross=%d base=%d bps=%d", gross, base, bps)
	}
	// Negative impressions (a corrupt aggregate) behave the same way.
	if got := ComputeQualityMultiplierBps(0.9, -42, band); got != NeutralMultiplierBps {
		t.Fatalf("negative-impression multiplier=%d want neutral=%d", got, NeutralMultiplierBps)
	}
}

// CQS is noisy when few people have seen the content, so the score is
// blended toward neutral until there is enough evidence.
func TestLowImpressionScoresAreShrunkTowardNeutral(t *testing.T) {
	band := DefaultQualityBand()

	// At exactly K impressions the measured score is trusted halfway.
	shrunk := ShrinkCQS(1.0, band.ConfidenceImpressions, band)
	want := band.PivotCQS + (1.0-band.PivotCQS)*0.5
	if math.Abs(shrunk-want) > 1e-9 {
		t.Fatalf("shrink at K impressions = %.4f, want %.4f", shrunk, want)
	}

	// A perfect score on 50 impressions must earn far less bonus than
	// the same score on a million.
	few := ComputeQualityMultiplierBps(1.0, 50, band)
	many := ComputeQualityMultiplierBps(1.0, enoughImpressions, band)
	if few >= many {
		t.Fatalf("thin evidence (%d bps) earned as much as thick evidence (%d bps)", few, many)
	}
	if few > 10_500 {
		t.Fatalf("50 impressions bought a %d bps bonus; shrink is not biting", few)
	}

	// Symmetrically, a terrible score on thin evidence must not be
	// punished to the floor.
	badFew := ComputeQualityMultiplierBps(0.0, 50, band)
	if badFew <= 9_800 {
		t.Fatalf("50 impressions of a zero score cost %d bps; shrink is not protecting", badFew)
	}
}

// The old behaviour must stay reachable: ComputeGrossPaise is untouched,
// and a neutral or disabled band reproduces it exactly.
func TestNeutralBandReproducesThePreQualityPayoutExactly(t *testing.T) {
	views, rpm := int64(12_345), int64(5000)
	plain := ComputeGrossPaise(views, rpm)
	if plain != views*rpm/1000 {
		t.Fatalf("ComputeGrossPaise changed behaviour: %d", plain)
	}

	for name, band := range map[string]QualityBand{
		"neutral band": NeutralQualityBand(),
		"disabled default": func() QualityBand {
			b := DefaultQualityBand()
			b.Enabled = false
			return b
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			for _, cqs := range []float64{0, 0.2, 0.35, 0.8, 1.0} {
				gross, base, bps := ComputeQualityAdjustedGrossPaise(views, rpm, cqs, enoughImpressions, band)
				if bps != NeutralMultiplierBps {
					t.Fatalf("cqs=%.2f multiplier=%d want neutral", cqs, bps)
				}
				if base != plain || gross != plain {
					t.Fatalf("cqs=%.2f gross=%d base=%d want %d", cqs, gross, base, plain)
				}
			}
		})
	}
}

func TestQualityAdjustedGrossUsesIntegerPaiseArithmetic(t *testing.T) {
	band := pureBand()

	// 10 000 views at Rs 50 per 1000 = Rs 500.00 = 50 000 paise.
	gross, base, bps := ComputeQualityAdjustedGrossPaise(10_000, 5000, 0.0, enoughImpressions, band)
	if base != 50_000 || bps != 8500 || gross != 42_500 {
		t.Fatalf("floor payout gross=%d base=%d bps=%d want 42500/50000/8500", gross, base, bps)
	}

	gross, _, bps = ComputeQualityAdjustedGrossPaise(10_000, 5000, 1.0, enoughImpressions, band)
	if bps != 12_500 || gross != 62_500 {
		t.Fatalf("ceiling payout gross=%d bps=%d want 62500/12500", gross, bps)
	}

	gross, _, bps = ComputeQualityAdjustedGrossPaise(10_000, 5000, 0.35, enoughImpressions, band)
	if bps != 10_000 || gross != 50_000 {
		t.Fatalf("pivot payout gross=%d bps=%d want 50000/10000", gross, bps)
	}

	// Zero and negative inputs never produce a payment.
	if got := ApplyQualityMultiplier(0, 12_500); got != 0 {
		t.Fatalf("zero base paid %d", got)
	}
	if got := ApplyQualityMultiplier(-100, 12_500); got != 0 {
		t.Fatalf("negative base paid %d", got)
	}
	if got, _, _ := ComputeQualityAdjustedGrossPaise(-5, 5000, 1.0, enoughImpressions, band); got != 0 {
		t.Fatalf("negative views paid %d", got)
	}
}

func TestBadBandConfigurationCannotInvertOrGoNegative(t *testing.T) {
	inverted := QualityBand{FloorBps: 12_000, CeilingBps: 4_000, PivotCQS: 0.35, Enabled: true}
	for _, cqs := range []float64{0, 0.35, 1} {
		got := ComputeQualityMultiplierBps(cqs, enoughImpressions, inverted)
		if got != 12_000 {
			t.Fatalf("inverted band cqs=%.2f gave %d, want the floor 12000", cqs, got)
		}
	}

	nonsensePivot := QualityBand{FloorBps: 8_500, CeilingBps: 12_500, PivotCQS: 0, Enabled: true}
	if got := ComputeQualityMultiplierBps(0.35, enoughImpressions, nonsensePivot); got != NeutralMultiplierBps {
		t.Fatalf("zero pivot was not repaired to the default: %d", got)
	}

	negativeFloor := QualityBand{FloorBps: -500, CeilingBps: 12_500, PivotCQS: 0.35, Enabled: true}
	if got := ComputeQualityMultiplierBps(0, enoughImpressions, negativeFloor); got < 0 {
		t.Fatalf("negative floor produced a negative multiplier: %d", got)
	}
}

// The creator must be able to read why they were paid what they were.
func TestPayoutExplanationNamesEveryTermInTheFormula(t *testing.T) {
	band := DefaultQualityBand()
	views, rpm := int64(10_000), int64(5000)
	gross, base, bps := ComputeQualityAdjustedGrossPaise(views, rpm, 0.80, enoughImpressions, band)
	net, fee := SplitEarnings(gross, 3000)

	summary := ExplainQualityPayout(QualityPayoutExplanation{
		ViewCount:        views,
		RpmPaise:         rpm,
		BaseGrossPaise:   base,
		MeasuredCQS:      0.80,
		Impressions:      enoughImpressions,
		EffectiveCQS:     ShrinkCQS(0.80, enoughImpressions, band),
		MultiplierBps:    bps,
		FloorBps:         band.FloorBps,
		CeilingBps:       band.CeilingBps,
		PivotCQS:         band.PivotCQS,
		GrossPaise:       gross,
		PlatformFeeBps:   3000,
		PlatformFeePaise: fee,
		NetPaise:         net,
	})

	for _, fragment := range []string{
		"10000 views",  // the countable input
		"Rs 50.00",     // the rate
		"Rs 500.00",    // the plain views x RPM amount
		"0.80",         // the measured quality score
		"raised by",    // the direction the score moved the payment
		"0.85x-1.25x",  // the band it is confined to
		"30% platform", // the split
	} {
		if !strings.Contains(summary, fragment) {
			t.Fatalf("explanation is missing %q:\n%s", fragment, summary)
		}
	}

	// A zero-impression day must say so rather than imply a bad score.
	quiet := ExplainQualityPayout(QualityPayoutExplanation{
		ViewCount: 10, RpmPaise: rpm, BaseGrossPaise: 50,
		MeasuredCQS: 0, Impressions: 0, EffectiveCQS: band.PivotCQS,
		MultiplierBps: NeutralMultiplierBps, FloorBps: band.FloorBps,
		CeilingBps: band.CeilingBps, PivotCQS: band.PivotCQS,
		GrossPaise: 50, PlatformFeeBps: 3000, PlatformFeePaise: 15, NetPaise: 35,
	})
	if !strings.Contains(quiet, "No impressions were recorded") {
		t.Fatalf("zero-impression explanation does not explain itself:\n%s", quiet)
	}
}
