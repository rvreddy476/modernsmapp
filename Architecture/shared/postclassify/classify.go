// Package postclassify owns the rule that decides whether a video
// post is a "flick" (short-form vertical) or a "long_video"
// (long-form). Single source of truth so post-service's CreatePost
// path, the MediaTranscodeConsumer reclassifier, and any future
// caller (admin tools, reclassification workers) all agree.
//
// The rule, per spec v2.1:
//
//	flick      ≤180s AND (portrait OR square)
//	long_video everything else
//
// "Reel" / "short" are legacy synonyms that map to "flick".
package postclassify

// FlickMaxDurationSeconds is the upper bound on flick duration.
// 180s = 3 minutes. Match YouTube Shorts / Instagram Reels.
const FlickMaxDurationSeconds = 180

// ContentType strings used across the platform. Defined here so any
// caller can compare against the canonical constants instead of
// re-typing the literal.
const (
	Flick     = "flick"
	LongVideo = "long_video"
)

// Classify returns "flick" if the video qualifies (≤180s, portrait
// or square), otherwise "long_video". Inputs are the post-transcode
// values from the media-service pipeline:
//
//	durationSeconds  > 0 once transcode finishes; 0 means unknown.
//	width, height    > 0 once transcode finishes; 0 means unknown.
//
// When duration or dimensions are unknown the function returns
// LongVideo as the safe default. Callers that have an explicit
// "intent" (mobile sending content_type=flick) should NOT call this
// before transcode — they should keep the explicit intent and let
// the MediaTranscodeConsumer reclassify after the pipeline produces
// real numbers.
func Classify(durationSeconds int, width, height int) string {
	if durationSeconds <= 0 || width <= 0 || height <= 0 {
		return LongVideo
	}
	if durationSeconds > FlickMaxDurationSeconds {
		return LongVideo
	}
	// Portrait (h > w) or square (h == w). Landscape is always long.
	if height < width {
		return LongVideo
	}
	return Flick
}

// IsShortForm returns true for content_type values rendered in the
// reels/flicks feed. "reel" and "short" are recognised for legacy
// rows still in the timeline tables.
func IsShortForm(contentType string) bool {
	switch contentType {
	case Flick, "reel", "short":
		return true
	}
	return false
}

// IsLongForm returns true for content_type values rendered in the
// posttube/long-video feed. "video" is the legacy synonym.
func IsLongForm(contentType string) bool {
	switch contentType {
	case LongVideo, "video":
		return true
	}
	return false
}
