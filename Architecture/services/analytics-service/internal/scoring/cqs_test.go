package scoring

import (
	"math"
	"testing"
)

func closeTo(t *testing.T, got, want float64, label string) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %.12f, want %.12f", label, got, want)
	}
}

// The formula the creator fund is about to pay against, asserted term by
// term against hand-computed values so a future edit to the weights
// cannot pass silently.
func TestComputeCQSMatchesTheWeightedFormulaOnKnownInputs(t *testing.T) {
	// 10 000 impressions => imp1k = 10, so an engagement count of N
	// gives a rate of N/10 per 1000, normalised by the 50-per-1000
	// soft cap: normalized = (N/10)/50 = N/500.
	m := &AggregateMetrics{
		AvgPercentViewed:   60, // -> 0.6 normalised
		Impressions:        10_000,
		Likes:              500, // rate 50/1000  -> 1.0 (at the cap)
		Comments:           250, // rate 25/1000  -> 0.5
		Shares:             100, // rate 10/1000  -> 0.2
		Saves:              50,  // rate 5/1000   -> 0.1
		FollowsFromContent: 25,  // rate 2.5/1000 -> 0.05
		Reports:            10,
		NotInterested:      15, // combined 25 -> rate 2.5/1000 -> 0.05
	}
	want := 0.45*0.6 + 0.10*1.0 + 0.05*0.5 + 0.20*0.2 + 0.05*0.1 + 0.10*0.05 - 0.05*0.05
	closeTo(t, ComputeCQS(m), want, "video CQS")

	// Watch time is the dominant term: the same engagement with half the
	// retention must score materially lower.
	half := *m
	half.AvgPercentViewed = 30
	if ComputeCQS(&half) >= ComputeCQS(m) {
		t.Fatal("halving retention did not lower the score")
	}
	closeTo(t, ComputeCQS(m)-ComputeCQS(&half), 0.45*0.3, "retention delta")
}

// Impressions are the denominator of every rate in the formula. Zero of
// them must return zero, not NaN or a divide-by-zero panic.
func TestComputeCQSWithNoImpressionsIsZeroNotNaN(t *testing.T) {
	got := ComputeCQS(&AggregateMetrics{
		AvgPercentViewed: 90, Impressions: 0,
		Likes: 100, Shares: 50, Comments: 20,
	})
	if got != 0 {
		t.Fatalf("zero-impression CQS = %v, want 0", got)
	}
	if math.IsNaN(got) || math.IsInf(got, 0) {
		t.Fatalf("zero-impression CQS is not a finite number: %v", got)
	}
	if ComputeCQS(nil) != 0 {
		t.Fatal("nil metrics did not score zero")
	}
}

func TestComputeCQSIsAlwaysClampedToTheUnitInterval(t *testing.T) {
	// Everything maxed out cannot exceed 1.
	best := ComputeCQS(&AggregateMetrics{
		AvgPercentViewed: 100, Impressions: 1000,
		Likes: 100_000, Comments: 100_000, Shares: 100_000,
		Saves: 100_000, FollowsFromContent: 100_000,
	})
	if best < 0 || best > 1 {
		t.Fatalf("best-case CQS = %v, outside [0,1]", best)
	}

	// Overwhelming negatives cannot drive it below zero.
	worst := ComputeCQS(&AggregateMetrics{
		AvgPercentViewed: 0.0001, Impressions: 1000,
		Reports: 1_000_000, NotInterested: 1_000_000,
	})
	if worst < 0 || worst > 1 {
		t.Fatalf("worst-case CQS = %v, outside [0,1]", worst)
	}

	// avg_percent_viewed above 100 (a looping reel) is clamped, so it
	// cannot buy more than the 0.45 the retention term is worth.
	over := ComputeCQS(&AggregateMetrics{AvgPercentViewed: 400, Impressions: 1000})
	closeTo(t, over, 0.45, "clamped retention-only CQS")
}

// A reel with no watch time recorded falls to the engagement-only
// formula, which weights likes and comments far more heavily.
func TestNonVideoContentUsesTheEngagementFormula(t *testing.T) {
	m := &AggregateMetrics{
		AvgPercentViewed: 0,
		Impressions:      1000, // imp1k = 1, so rate == count, /50 normalised
		Likes:            25,   // -> 0.5
		Comments:         10,   // -> 0.2
		Shares:           5,    // -> 0.1
		Saves:            5,    // -> 0.1
	}
	want := 0.35*0.5 + 0.25*0.2 + 0.20*0.1 + 0.10*0.1 + 0.10*0 - 0.05*0
	closeTo(t, ComputeCQS(m), want, "engagement-only CQS")
}

// The negative term must actually bite: two identical videos, one of
// which is being reported, must not score the same.
func TestNegativeSignalsReduceTheScore(t *testing.T) {
	clean := &AggregateMetrics{AvgPercentViewed: 70, Impressions: 5000, Likes: 100, Shares: 40}
	reported := *clean
	reported.Reports = 200
	reported.NotInterested = 300

	if ComputeCQS(&reported) >= ComputeCQS(clean) {
		t.Fatalf("reported content scored %v, no lower than clean %v",
			ComputeCQS(&reported), ComputeCQS(clean))
	}
}
