package personalization

import "math"

// The affinity model.
//
// The ranker in feed-service has always had an `interest_score` term fed
// from `user:affinities:{viewer}` — and nothing has ever written that key,
// so every viewer scored at the 0.3 cold-start floor and the feed was
// ordered by recency and velocity alone. This package is what writes it,
// and the model below is the whole of the opinion in it.
//
// # WHAT COUNTS
//
// Watch time first. A finished video is the strongest ordinary signal a
// viewer gives — it costs them minutes, it cannot be misclicked, and it is
// the one thing an impression is not. So `play_end` contributes the
// FRACTION of the video actually watched: a completion is worth 1.0, a
// 10 % skim is worth 0.1, and the difference between them is the whole
// point. An impression is worth 0.02 — a fiftieth of a completion — because
// scrolling past something is barely evidence of anything.
//
// Then the deliberate acts, in the order of how much they cost the viewer:
// a like is cheap, a comment less so, a save is a promise to come back, a
// share puts their own name on it, and following off the back of a video is
// the strongest positive act in the product.
//
// The negatives are not symmetric with the positives, on purpose. "Not
// interested" has to outweigh a handful of accidental watches or the button
// does not work; a report and a block have to bury the author outright.
//
// # WHY THESE NUMBERS AND NOT OTHERS
//
// They are ratios, not measurements: nobody has run an experiment on this
// product yet, and pretending otherwise would be worse than saying so. What
// they encode is an ordering that is defensible without data —
// watch > follow > share > save > comment > like > impression — and a scale
// where a single completed video is a meaningful nudge but four or five are
// needed before an author dominates. When there is enough traffic to fit
// these against retention, they should be fitted; until then they are
// honest priors and are all in one place so they can be replaced at once.
const (
	// WeightImpression: the content was on screen. Nearly nothing.
	WeightImpression = 0.02
	// WeightWatch scales the completed FRACTION of a playback, so the
	// value of one play_end is WeightWatch * percent_viewed/100.
	WeightWatch = 1.0
	// WeightLike — one tap, easily given.
	WeightLike = 0.5
	// WeightComment — writing something takes real attention.
	WeightComment = 0.7
	// WeightSave — "I want this again later".
	WeightSave = 0.8
	// WeightShare — the viewer put their own name on it.
	WeightShare = 1.0
	// WeightFollow — followed the creator because of this video. The
	// strongest positive act the product has.
	WeightFollow = 2.0

	// WeightNotInterested must outweigh several incidental watches, or
	// the button does not work. Three completions' worth.
	WeightNotInterested = -3.0
	// WeightReport — a report is about the content, but it is also a
	// clear statement that the viewer does not want more of it.
	WeightReport = -5.0
	// WeightBlockCreator — buries the author on its own.
	WeightBlockCreator = -10.0
)

// eventWeight is the per-occurrence value of an event type. play_end is
// absent deliberately: its value is not per-occurrence but proportional to
// how much of the video was watched, and it is applied separately.
var eventWeight = map[string]float64{
	"impression":          WeightImpression,
	"like":                WeightLike,
	"comment_create":      WeightComment,
	"save":                WeightSave,
	"share":               WeightShare,
	"follow_from_content": WeightFollow,
	"not_interested":      WeightNotInterested,
	"report":              WeightReport,
	"block_creator":       WeightBlockCreator,
}

// AffinityHalfLifeDays is the decay half-life applied to every event's
// contribution.
//
// "An author you binged in March is not who you want today." Two weeks is
// the shortest half-life that still survives a holiday: someone who does
// not open the app for ten days comes back with their tastes at ~62 % of
// their old strength rather than reset to zero. It also means a genuine
// change of interest shows up inside a month — after 60 days (the scan
// window) an event is worth 5 % of what it was worth fresh, so the window
// and the half-life are consistent with each other: nothing meaningful is
// being thrown away at the edge.
const AffinityHalfLifeDays = 14.0

// AffinityWindowDays is how far back the scan reads. Beyond four
// half-lives an event contributes under 6 % of its original value, so
// reading further would cost a bigger scan for a rounding error.
const AffinityWindowDays = 60

// affinitySaturation (K) sets how much accumulated evidence is "a lot".
//
// The squash below is 1 - exp(-raw/K), so K is the raw score at which a
// viewer is 63 % of the way to the ceiling. K = 3 makes one fresh
// completed video ≈ 0.50, a video plus a like ≈ 0.58, five completions
// ≈ 0.86. That is the shape we want: the first video you finish by an
// author moves them a long way, and the twentieth barely moves them at
// all, so a single binge cannot pin the feed to one creator.
const affinitySaturation = 3.0

// ColdStartInterest is the interest score the feed ranker already uses for
// an author it knows nothing about (ranking/scorer.go). It is reproduced
// here because it is the anchor of the squash, not an arbitrary constant:
// a viewer's affinity for an author they have never engaged with must come
// out at EXACTLY this value, or writing the key would change the ranking
// of authors we have learned nothing about.
const ColdStartInterest = 0.3

// SquashAffinity maps an accumulated, decayed raw score onto the [0, 1]
// interest scale the ranker's formula expects.
//
// The neutral point is pinned to the ranker's own cold-start floor:
//
//	raw = 0  → 0.30   (identical to an author we know nothing about)
//	raw > 0  → 0.30 + 0.70 * (1 - exp(-raw/K))     → approaches 1.0
//	raw < 0  → 0.30 * exp(raw/K)                   → approaches 0.0
//
// Pinning it matters. The obvious mapping — a plain 1 - exp(-raw/K) over
// [0,1] — puts one completed video at 0.28, BELOW the 0.3 an unknown
// author gets. Writing that key would demote every creator the viewer has
// watched once beneath every stranger, which is the exact opposite of the
// feature. The piecewise form is continuous at zero and monotone
// throughout, so more engagement always means a higher score.
func SquashAffinity(raw float64) float64 {
	if raw > 0 {
		return ColdStartInterest + (1-ColdStartInterest)*(1-math.Exp(-raw/affinitySaturation))
	}
	if raw < 0 {
		return ColdStartInterest * math.Exp(raw/affinitySaturation)
	}
	return ColdStartInterest
}

// TypeBundle is one (subject, event type) pair's decayed totals, as the
// SQL scan produces them. Keeping the arithmetic here rather than in the
// query means the weights above are the only definition of the model and
// are unit-testable without a database.
type TypeBundle struct {
	// Type is the events_raw event type.
	Type string
	// DecayedCount is the sum of the time-decay factor over every
	// occurrence — an occurrence-count in which older events count less.
	DecayedCount float64
	// DecayedWatchFraction is the sum of decay * (percent_viewed/100),
	// meaningful only for play_end.
	DecayedWatchFraction float64
}

// RawScore folds one subject's decayed event totals into the raw score the
// squash consumes. play_end is scaled by how much of each video was
// actually watched; every other type is counted at its per-occurrence
// weight. Unknown types contribute nothing rather than a default, so a new
// event type cannot silently start influencing the feed.
func RawScore(bundles []TypeBundle) float64 {
	var raw float64
	for _, b := range bundles {
		if b.Type == "play_end" {
			raw += WeightWatch * b.DecayedWatchFraction
			continue
		}
		if w, ok := eventWeight[b.Type]; ok {
			raw += w * b.DecayedCount
		}
	}
	return raw
}
