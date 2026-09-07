package personalization

import (
	"math"
	"testing"
)

// The affinity model's properties, pinned. These are the claims the feed's
// ordering rests on; the numbers themselves are priors and will be
// retuned, but a change that breaks one of these has changed what the
// model MEANS, not just how loud it is.

func TestSquashAffinity_NeutralIsTheRankersColdStartFloor(t *testing.T) {
	// The single most important property. If a viewer with no history for
	// an author scored anything other than the ranker's own cold-start
	// constant, writing the affinity key would silently re-rank every
	// author the system has learned nothing about.
	if got := SquashAffinity(0); got != ColdStartInterest {
		t.Fatalf("SquashAffinity(0) = %v, want the ranker's cold-start floor %v", got, ColdStartInterest)
	}
}

func TestSquashAffinity_OneCompletedVideoBeatsAStranger(t *testing.T) {
	// The trap the piecewise form exists to avoid: a plain 1-exp(-raw/K)
	// over [0,1] puts one completed video at 0.28, BELOW the 0.3 an
	// unknown author gets — so watching someone once would DEMOTE them
	// beneath every stranger.
	oneCompletion := RawScore([]TypeBundle{{Type: "play_end", DecayedCount: 1, DecayedWatchFraction: 1.0}})
	got := SquashAffinity(oneCompletion)
	if got <= ColdStartInterest {
		t.Fatalf("one completed video scored %v, which is not above the %v a stranger gets", got, ColdStartInterest)
	}
	if got > 1 {
		t.Fatalf("one completed video scored %v, above the [0,1] range the ranker expects", got)
	}
}

func TestSquashAffinity_MonotoneAndBounded(t *testing.T) {
	prev := math.Inf(-1)
	for raw := -30.0; raw <= 30.0; raw += 0.1 {
		got := SquashAffinity(raw)
		if got < 0 || got > 1 {
			t.Fatalf("SquashAffinity(%v) = %v, outside [0,1]", raw, got)
		}
		if got < prev-1e-12 {
			t.Fatalf("SquashAffinity is not monotone at raw=%v (%v after %v)", raw, got, prev)
		}
		prev = got
	}
	// Continuous at the join.
	l, r := SquashAffinity(-1e-9), SquashAffinity(1e-9)
	if math.Abs(l-r) > 1e-6 {
		t.Fatalf("discontinuity at zero: %v vs %v", l, r)
	}
}

func TestSquashAffinity_Saturates(t *testing.T) {
	// A binge must not pin the feed to one creator: the twentieth video
	// by an author has to move their score far less than the first did.
	one := SquashAffinity(RawScore([]TypeBundle{{Type: "play_end", DecayedCount: 1, DecayedWatchFraction: 1}}))
	five := SquashAffinity(RawScore([]TypeBundle{{Type: "play_end", DecayedCount: 5, DecayedWatchFraction: 5}}))
	twenty := SquashAffinity(RawScore([]TypeBundle{{Type: "play_end", DecayedCount: 20, DecayedWatchFraction: 20}}))

	firstStep := one - ColdStartInterest
	lastStep := twenty - five
	if lastStep >= firstStep {
		t.Fatalf("the model is not saturating: the first video moved the score by %.4f, videos 6-20 by %.4f", firstStep, lastStep)
	}
	if twenty > 1 {
		t.Fatalf("twenty completions scored %v, above 1", twenty)
	}
}

func TestRawScore_WatchTimeAndCompletionDominate(t *testing.T) {
	// "from watch time and completion first (a finished video is worth
	// far more than an impression)".
	completion := RawScore([]TypeBundle{{Type: "play_end", DecayedCount: 1, DecayedWatchFraction: 1.0}})
	impression := RawScore([]TypeBundle{{Type: "impression", DecayedCount: 1}})
	if completion <= impression*10 {
		t.Errorf("a completed video (%.4f) is not decisively above an impression (%.4f)", completion, impression)
	}

	// A partial watch is worth its fraction and nothing more — the whole
	// reason play_end carries a watched fraction rather than a count.
	skim := RawScore([]TypeBundle{{Type: "play_end", DecayedCount: 1, DecayedWatchFraction: 0.1}})
	if math.Abs(skim-0.1*completion) > 1e-9 {
		t.Errorf("a 10%% watch scored %.4f, want a tenth of a completion (%.4f)", skim, 0.1*completion)
	}
}

func TestRawScore_DeliberateActsAreOrderedByWhatTheyCostTheViewer(t *testing.T) {
	one := func(kind string) float64 {
		return RawScore([]TypeBundle{{Type: kind, DecayedCount: 1}})
	}
	order := []string{"impression", "like", "comment_create", "save", "share", "follow_from_content"}
	for i := 1; i < len(order); i++ {
		lo, hi := one(order[i-1]), one(order[i])
		if hi <= lo {
			t.Errorf("%s (%.2f) should be worth more than %s (%.2f)", order[i], hi, order[i-1], lo)
		}
	}
}

func TestRawScore_NegativesOutweighIncidentalWatching(t *testing.T) {
	// "Not interested" has to survive a handful of accidental watches, or
	// the button does not work.
	notInterested := RawScore([]TypeBundle{{Type: "not_interested", DecayedCount: 1}})
	twoCompletions := RawScore([]TypeBundle{{Type: "play_end", DecayedCount: 2, DecayedWatchFraction: 2}})
	if notInterested+twoCompletions >= 0 {
		t.Errorf("one 'not interested' (%.2f) did not survive two completions (%.2f)", notInterested, twoCompletions)
	}

	// A block buries the author outright: the squashed score must land
	// far below what a stranger gets.
	blocked := SquashAffinity(RawScore([]TypeBundle{
		{Type: "block_creator", DecayedCount: 1},
		{Type: "play_end", DecayedCount: 3, DecayedWatchFraction: 3},
	}))
	if blocked >= ColdStartInterest/2 {
		t.Errorf("a blocked creator scored %.4f, not decisively below the %.2f a stranger gets", blocked, ColdStartInterest)
	}
}

func TestRawScore_UnknownEventTypesContributeNothing(t *testing.T) {
	// A new event type must not start influencing the feed by accident.
	// The thirteen ingested types are enumerated in weights.go; anything
	// else scores zero until somebody decides what it is worth.
	got := RawScore([]TypeBundle{
		{Type: "watch_heartbeat", DecayedCount: 500, DecayedWatchFraction: 500},
		{Type: "milestone", DecayedCount: 40},
		{Type: "something_invented_next_year", DecayedCount: 99},
	})
	if got != 0 {
		t.Fatalf("unrecognised event types contributed %v, want 0", got)
	}
}

func TestTopN_KeepsTheStrongestSignalsInBothDirections(t *testing.T) {
	// The cap must not amount to "keep the highest scores", or every
	// negative would be dropped first and rejecting an author would stop
	// meaning anything as soon as a viewer had a few hundred of them.
	scores := map[string]float64{
		"loved":    0.95,
		"hated":    0.02,
		"mild":     0.35,
		"neutral":  ColdStartInterest, // carries no information
		"mild-neg": 0.26,
	}
	kept := topN(scores, 2, ColdStartInterest)
	if len(kept) != 4 { // two field/value pairs
		t.Fatalf("topN returned %d entries, want 2 pairs", len(kept)/2)
	}
	got := map[string]bool{kept[0].(string): true, kept[2].(string): true}
	if !got["loved"] || !got["hated"] {
		t.Fatalf("topN kept %v, want the two strongest opinions (loved, hated)", got)
	}

	// A neutral entry is never written: it would tell the ranker exactly
	// what it already assumes.
	all := topN(scores, 10, ColdStartInterest)
	for i := 0; i < len(all); i += 2 {
		if all[i].(string) == "neutral" {
			t.Fatalf("topN wrote a neutral entry")
		}
	}
	if len(all)/2 != 4 {
		t.Fatalf("topN wrote %d entries, want the 4 non-neutral ones", len(all)/2)
	}
}

func TestTopN_IsDeterministic(t *testing.T) {
	// Two runs over unchanged data must write identical hashes, or every
	// pass churns Redis and no two debugging sessions see the same thing.
	scores := map[string]float64{}
	for _, k := range []string{"a", "b", "c", "d", "e", "f"} {
		scores[k] = 0.8 // identical scores: only the tie-break can order these
	}
	first := topN(scores, 3, ColdStartInterest)
	for i := 0; i < 20; i++ {
		again := topN(scores, 3, ColdStartInterest)
		for j := range first {
			if first[j] != again[j] {
				t.Fatalf("topN is not deterministic: %v then %v", first, again)
			}
		}
	}
}

func TestDecayWindowAndHalfLifeAreConsistent(t *testing.T) {
	// The scan window should be far enough out that dropping events at
	// its edge costs nothing measurable, and no further.
	atEdge := math.Exp(-math.Ln2 * AffinityWindowDays / AffinityHalfLifeDays)
	if atEdge > 0.06 {
		t.Errorf("an event at the edge of the %d-day window still carries %.3f of its value; the window is too short for a %.0f-day half-life",
			AffinityWindowDays, atEdge, AffinityHalfLifeDays)
	}
	// Someone back from a ten-day holiday should not find their tastes reset.
	afterTenDays := math.Exp(-math.Ln2 * 10 / AffinityHalfLifeDays)
	if afterTenDays < 0.5 {
		t.Errorf("after ten days away a viewer retains only %.2f of their affinity; the half-life is too aggressive", afterTenDays)
	}
}
