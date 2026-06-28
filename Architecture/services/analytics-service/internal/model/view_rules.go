package model

// IsDisplayView determines whether a viewing session qualifies as a "display view"
// per the PRD Section 4 rules:
//
//	Reels (<=90s):
//	  - Watched >= 3 seconds, OR
//	  - Watched >= 25% of reel length, OR
//	  - If reel length < 3s: watched >= 1 full loop
//
//	Long Video (>90s):
//	  - Watched >= 30 seconds, OR
//	  - If video length < 60s: watched >= 50% of length
func IsDisplayView(contentType string, durationMS, watchedMS int64, percentViewed float64, loopCount int) bool {
	switch contentType {
	case ContentTypeReel, "flick":
		// Watched >= 3 seconds
		if watchedMS >= 3000 {
			return true
		}
		// Watched >= 25% of reel length
		if percentViewed >= 25.0 {
			return true
		}
		// Very short reel (< 3s): at least one full loop
		if durationMS > 0 && durationMS < 3000 && loopCount >= 1 {
			return true
		}
		return false

	case ContentTypeLongVideo:
		// Watched >= 30 seconds
		if watchedMS >= 30000 {
			return true
		}
		// Short long-form (< 60s): watched >= 50%
		if durationMS > 0 && durationMS < 60000 && percentViewed >= 50.0 {
			return true
		}
		return false

	default:
		// Unknown content type — fall back to reel rules
		return watchedMS >= 3000
	}
}

// IsEngagedView checks the stronger "engaged view" definition used internally:
// >= 10s watched OR >= 50% viewed OR meaningful engagement.
func IsEngagedView(watchedMS int64, percentViewed float64, hasEngagement bool) bool {
	if watchedMS >= 10000 {
		return true
	}
	if percentViewed >= 50.0 {
		return true
	}
	return hasEngagement
}

// ClassifyContentType determines whether content is a reel or long video based on duration.
func ClassifyContentType(durationMS int64) string {
	if durationMS <= 90000 {
		return ContentTypeReel
	}
	return ContentTypeLongVideo
}
