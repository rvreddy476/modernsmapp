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
	ContentType string // "text", "image", "video"
	Score       float64
	// Source is where the service found the candidate ("timeline",
	// "cold_start", "circle"). Carried through untouched so the reason a
	// post is in the feed survives re-ordering; the scorer never reads it.
	Source string
}

// AuthorPenalty converts the viewer's net feedback about an author into the
// scorer's authorPenalty term. Only a net-negative history penalises —
// each "Not interested" costs 0.25 — capped at 0.5 so one author can never
// be pushed below what an already-interacted post gets. "Interested"
// answers only ever cancel earlier "Not interested" ones; there is no boost.
func AuthorPenalty(netFeedback float64) float64 {
	if netFeedback >= 0 {
		return 0
	}
	return math.Min(0.5, 0.25*-netFeedback)
}

// MutedAuthorNet is the net feedback an author-level "Don't recommend this
// account" contributes: enough on its own to reach AuthorPenalty's cap.
const MutedAuthorNet = -2

// NetWithMute combines the viewer's post-level net feedback about an author
// with their author-level answer into the single value mirrored to
// feed:author_feedback:{viewer}. An active mute is a strong negative: it
// floors the post-level history at zero (a few earlier "Interested" taps
// must not soften "never recommend this account") and then adds
// MutedAuthorNet, so the author always lands at the maximum penalty.
// Without a mute the post-level net passes through unchanged.
func NetWithMute(postNet float64, muted bool) float64 {
	if !muted {
		return postNet
	}
	return math.Min(postNet, 0) + MutedAuthorNet
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
		case "reel":
			mediaBoost = 1.3
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

		// 4. engagement_momentum (0.0-0.3) with time-decay
		// Raw momentum is normalized to [0, 0.3], then multiplied by an
		// exponential decay factor so that newer posts benefit more from
		// high velocity. Decay constant -0.03/h gives ~0.97 at 1h, ~0.86
		// at 5h, ~0.70 at 12h, ~0.49 at 24h.
		momentum := 0.0
		if maxVelocity > 0 {
			if v, ok := signals.Velocities[pid]; ok {
				raw := 0.3 * (v / maxVelocity)
				ageDecay := math.Exp(-0.03 * ageHours)
				momentum = raw * ageDecay
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

		// 7. quality_boost (0.0-0.25): CQS-based boost for high-quality content
		qualityBoost := 0.0
		if cqs, ok := signals.ContentQuality[pid]; ok && cqs > 0 {
			qualityBoost = cqs * 0.25
		}

		// 8. author_penalty (0.0-0.5): the formula's authorPenalty term,
		// fed by the viewer's "Not interested" answers on this author's
		// posts (post "more" sheet). See AuthorPenalty.
		authorPenalty := AuthorPenalty(signals.AuthorFeedback[aid])

		c.Score = (interest * recency * mediaBoost) + momentum + socialProximity + qualityBoost - authorPenalty - interactionPenalty
	}

	return scored
}
