package scoring

import "github.com/atpost/shared/postclassify"

// SessionEngagement holds per-session engagement flags.
type SessionEngagement struct {
	Liked         bool
	Commented     bool
	Shared        bool
	Saved         bool
	Followed      bool
	Reported      bool
	Blocked       bool
	NotInterested bool
}

// ComputeVQS calculates the View Quality Score for a single viewing session.
// PRD Section 10.1.
func ComputeVQS(contentType string, durationMS, watchedMS int64, percentViewed float64, engagement *SessionEngagement, trustFactor float64) float64 {
	// Threshold start. The kind is resolved through the one canonical
	// mapping, so a legacy "video" row is long-form (10 s) and a legacy
	// "reel" is short-form (3 s); comparing the raw literal here used to
	// hand "video" the reel threshold. A kind that is neither gets the
	// short-form bar, as it always did.
	thresholdMS := int64(3000) // Flicks: 3s
	if kind, ok := postclassify.CanonicalMonetizationType(contentType); ok && kind == postclassify.LongVideo {
		thresholdMS = 10000 // Long Video: 10s
	}

	// Base score
	base := 0.0
	if watchedMS >= 1000 {
		if watchedMS >= thresholdMS {
			base = 1.0
		} else {
			base = 0.2
		}
	}

	// Retention factor
	retention := 0.0
	if durationMS > 0 {
		retention = float64(watchedMS) / float64(durationMS)
	}
	retentionFactor := 0.2 + 0.8*retention
	if retentionFactor < 0 {
		retentionFactor = 0
	}
	if retentionFactor > 1.2 {
		retentionFactor = 1.2
	}

	// Engagement boost
	engagementBoost := 1.0
	if engagement != nil {
		if engagement.Liked {
			engagementBoost += 0.05
		}
		if engagement.Commented {
			engagementBoost += 0.15
		}
		if engagement.Shared {
			engagementBoost += 0.25
		}
		if engagement.Saved {
			engagementBoost += 0.20
		}
		if engagement.Followed {
			engagementBoost += 0.25
		}
	}

	// Negative penalty
	negativePenalty := 1.0
	if engagement != nil {
		if engagement.Reported || engagement.Blocked {
			negativePenalty = 0.2
		} else if engagement.NotInterested {
			negativePenalty = 0.5
		}
	}

	return base * retentionFactor * engagementBoost * negativePenalty * trustFactor
}
