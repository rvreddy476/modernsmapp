package ranking

import (
	"math"
	"time"

	"github.com/google/uuid"
)

// Candidate is the ranking-internal representation of a post being scored.
// The service layer converts between service.FeedItem and ranking.Candidate
// to avoid an import cycle.
type Candidate struct {
	PostID      uuid.UUID
	AuthorID    uuid.UUID
	CreatedAt   time.Time
	ContentType string  // "text", "image", "video"
	Score       float64
}

// ScoreCandidates computes a ranking score for each candidate using the
// v2.0 spec Appendix A formula:
//
//	score = (interest * recency * mediaBoost) + momentum + socialProximity
//	        - authorPenalty - interactionPenalty
//
// penalty_same_author is deferred to the diversity placement pass.
func ScoreCandidates(candidates []Candidate, signals *ViewerSignals) []Candidate {
	now := time.Now()

	// Pre-compute the maximum velocity among all candidates so we can
	// normalize the momentum term.
	maxVelocity := 0.0
	for _, c := range candidates {
		pid := c.PostID.String()
		if v, ok := signals.Velocities[pid]; ok && v > maxVelocity {
			maxVelocity = v
		}
	}

	scored := make([]Candidate, len(candidates))
	copy(scored, candidates)

	for i := range scored {
		c := &scored[i]
		aid := c.AuthorID.String()
		pid := c.PostID.String()

		// 1. interest_score (0.0-1.0)
		interest := 0.3 // cold-start floor
		if a, ok := signals.AuthorAffinities[aid]; ok {
			interest = a
		}

		// 2. recency_factor (0.0-1.0)
		ageHours := now.Sub(c.CreatedAt).Hours()
		recency := math.Exp(-0.05 * ageHours)
		if ageHours < 0.5 { // <30 min
			recency = math.Max(recency, 0.9)
		}

		// 3. media_boost (1.0-1.5)
		mediaBoost := 1.0
		switch c.ContentType {
		case "image":
			mediaBoost = 1.2
		case "video":
			switch {
			case signals.MediaPrefs.VideoP95Dwell > 60:
				mediaBoost = 1.5
			case signals.MediaPrefs.VideoP95Dwell > 30:
				mediaBoost = 1.3
			default:
				mediaBoost = 1.1
			}
		}

		// 4. engagement_momentum (0.0-0.3)
		momentum := 0.0
		if maxVelocity > 0 {
			if v, ok := signals.Velocities[pid]; ok {
				momentum = 0.3 * (v / maxVelocity)
			}
		}

		// 5. social_proximity (0.0-0.2)
		socialProximity := 0.1 // baseline: all candidates are from followed users
		if signals.MutualFollows[aid] {
			socialProximity = 0.2
		}

		// 6. penalty_already_interacted (-0.5)
		interactionPenalty := 0.0
		if signals.Interactions[pid] {
			interactionPenalty = 0.5
		}

		c.Score = (interest*recency*mediaBoost) + momentum + socialProximity - interactionPenalty
	}

	return scored
}
