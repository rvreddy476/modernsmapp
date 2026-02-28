package scoring

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
	// Threshold start
	thresholdMS := int64(3000) // Reels: 3s
	if contentType == "long_video" {
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
