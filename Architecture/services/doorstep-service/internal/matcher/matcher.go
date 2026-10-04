// Package matcher ranks the professionals who passed every hard filter for a
// slot (internal/slots). The shape is rider-service's matcher: hard filters
// are someone else's job, Score is a pure formula whose components are
// recorded for an admin to read, and Rank is a stable sort with a
// deterministic tie-break. No I/O.
//
//	score = smoothed_rating*20          (Bayesian: prior 4.5 over 5 ratings)
//	      + acceptance_rate*30          (Laplace-smoothed offers accepted)
//	      - cancellation_rate*40        (cancellations / (jobs + cancellations))
//	      - distance_km*2               (home to the address; A4 refines with
//	                                     the previous job's location)
//	      - week_load*3                 (fairness: jobs already this week)
//
// Ties go to the lower professional id, so the same inputs always pick the
// same professional.
package matcher

import (
	"sort"
	"strconv"

	"github.com/google/uuid"
)

// Weights and smoothing (placeholders, tuned with data after the pilot).
const (
	RatingPrior      = 4.5
	RatingPriorCount = 5.0
	WeightRating     = 20.0
	WeightAccept     = 30.0
	WeightCancel     = 40.0
	WeightDistanceKM = 2.0
	WeightWeekLoad   = 3.0
)

// Candidate is one qualified, free professional.
type Candidate struct {
	ProID          uuid.UUID
	DistanceM      float64
	RatingSum      int64
	RatingCount    int
	OffersReceived int
	OffersAccepted int
	Cancellations  int
	JobsCompleted  int
	WeekLoad       int
}

// SmoothedRating pulls a thin record toward the prior.
func SmoothedRating(c Candidate) float64 {
	return (float64(c.RatingSum) + RatingPrior*RatingPriorCount) / (float64(c.RatingCount) + RatingPriorCount)
}

// AcceptanceRate is Laplace-smoothed: a new professional starts at 0.5.
func AcceptanceRate(c Candidate) float64 {
	return (float64(c.OffersAccepted) + 1) / (float64(c.OffersReceived) + 2)
}

// CancellationRate is cancellations over jobs taken (0 with no history).
func CancellationRate(c Candidate) float64 {
	den := c.JobsCompleted + c.Cancellations
	if den <= 0 {
		return 0
	}
	return float64(c.Cancellations) / float64(den)
}

// Score is the formula above, with each component recorded.
func Score(c Candidate) (float64, []string) {
	rating := SmoothedRating(c) * WeightRating
	accept := AcceptanceRate(c) * WeightAccept
	cancel := -CancellationRate(c) * WeightCancel
	dist := -(c.DistanceM / 1000) * WeightDistanceKM
	load := -float64(c.WeekLoad) * WeightWeekLoad
	total := rating + accept + cancel + dist + load
	return total, []string{
		component("rating", rating), component("acceptance", accept), component("cancellation", cancel),
		component("distance", dist), component("week_load", load),
	}
}

// Scored is a candidate with its score and breakdown.
type Scored struct {
	Candidate Candidate
	Score     float64
	Reasons   []string
}

// Rank orders candidates best first; ties go to the lower id.
func Rank(cands []Candidate) []Scored {
	out := make([]Scored, 0, len(cands))
	for _, c := range cands {
		s, r := Score(c)
		out = append(out, Scored{Candidate: c, Score: s, Reasons: r})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Candidate.ProID.String() < out[j].Candidate.ProID.String()
	})
	return out
}

func component(label string, v float64) string {
	sign := "+"
	if v < 0 {
		sign = ""
	}
	return label + "=" + sign + strconv.FormatFloat(v, 'f', 2, 64)
}
