package model

import "github.com/atpost/shared/postclassify"

// ShortFormViewRuleMaxDurationMS is a VIEW-COUNTING threshold, not a
// content type.
//
// PRD Section 4 gives short-form content an easier bar for what counts
// as a display view (3s / 25%) than long-form (30s / 50%), because a
// reel is consumed in a swipe feed where a few seconds of dwell is a
// real signal. That bar was written as "reels (<=90s)", and this package
// used to turn the same 90 seconds into its own ClassifyContentType —
// a second, disagreeing answer to "what kind of post is this".
//
// It is not. The platform has exactly one content-type rule and it lives
// in shared/postclassify: <=300s AND portrait/square is a flick,
// everything else is a long_video. That is what post-service stores on
// the timeline, what PostCreated carries into analytics.content_ownership,
// and what monetization-service resolves its per-content-type RPM rate
// against. Analytics never classifies; it reads the label off the
// ownership projection.
//
// What survives from the 90 seconds is only the view bar: a piece of
// content has to actually be short to earn the short-form threshold.
// A four-minute flick is still a flick — it lives in the reels feed and
// is paid at the flick rate — but a viewer who watched three seconds of
// it has not watched it, so it is judged by the long-form bar.
const ShortFormViewRuleMaxDurationMS = 90_000

// usesShortFormViewRules decides which display-view bar applies.
//
// Both halves are required. The label alone is not enough: a 250-second
// flick would otherwise count a view after 3 seconds (1.2% of the
// video). The duration alone is not enough either: a 60-second post
// published to Tube is deliberately a long_video (founder, 2026-09-04/05
// — "a video is what the author posted as a video"), and handing it the
// reel bar would inflate long_video views, which are settled at the
// long_video RPM.
//
// durationMS <= 0 means the client could not report a duration; the
// label is then the only evidence there is, so it decides alone. That
// preserves the behaviour this function has always had for reels whose
// duration was missing.
func usesShortFormViewRules(contentType string, durationMS int64) bool {
	if !postclassify.IsShortForm(contentType) {
		return false
	}
	if durationMS <= 0 {
		return true
	}
	return durationMS <= ShortFormViewRuleMaxDurationMS
}

// IsDisplayView determines whether a viewing session qualifies as a
// "display view" per the PRD Section 4 rules.
//
//	Short form (a flick of <= ShortFormViewRuleMaxDurationMS):
//	  - Watched >= 3 seconds, OR
//	  - Watched >= 25% of its length, OR
//	  - If length < 3s: watched >= 1 full loop
//
//	Everything else (long_video, and any flick longer than the
//	short-form view bar):
//	  - Watched >= 30 seconds, OR
//	  - If length < 60s: watched >= 50% of length
//
// contentType is the value from analytics.content_ownership, which is
// post-service's postclassify decision. It is never the client's claim:
// IngestService rebuilds it from the projection before this is called.
func IsDisplayView(contentType string, durationMS, watchedMS int64, percentViewed float64, loopCount int) bool {
	if usesShortFormViewRules(contentType, durationMS) {
		// Watched >= 3 seconds
		if watchedMS >= 3000 {
			return true
		}
		// Watched >= 25% of its length
		if percentViewed >= 25.0 {
			return true
		}
		// Very short reel (< 3s): at least one full loop
		if durationMS > 0 && durationMS < 3000 && loopCount >= 1 {
			return true
		}
		return false
	}

	if postclassify.IsLongForm(contentType) || postclassify.IsShortForm(contentType) {
		// Watched >= 30 seconds
		if watchedMS >= 30000 {
			return true
		}
		// Short long-form (< 60s): watched >= 50%
		if durationMS > 0 && durationMS < 60000 && percentViewed >= 50.0 {
			return true
		}
		return false
	}

	// Unlabelled content — an ownership row that defaulted to "post"
	// because PostCreated carried no content_type, or a legacy row from
	// before the flick/long_video split. There is no kind to trust, so
	// duration decides on its own: something over the short-form bar
	// does not get the short-form threshold just because nobody said
	// what it was.
	if durationMS > ShortFormViewRuleMaxDurationMS {
		return watchedMS >= 30000
	}
	return watchedMS >= 3000
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
