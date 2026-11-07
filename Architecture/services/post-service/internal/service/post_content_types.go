// post_content_types.go decides what kind of post something is: the allowed
// content_type set, the Shorts length cap, video classification by
// duration and orientation, category overrides and the canonical spelling
// of legacy content types. Moved out of post.go unchanged.

package service

import (
	"fmt"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/postclassify"
)

// flickMaxDurationSeconds is the maximum duration (inclusive) for a video to
// be auto-classified as a "reel" (Flick). Videos longer than this are "video" (Long Video).
// Flick = up to 10 minutes, Long Video = more than 10 minutes (founder,
// 2026-10-21; was 5 minutes from 2026-09-05). It IS
// postclassify.FlickMaxDurationSeconds — change it there, never here.
const flickMaxDurationSeconds = postclassify.FlickMaxDurationSeconds

// ExceedsShortsCap reports whether a measured duration is too long for a
// Short (founder, 2026-10-21: Shorts run up to 10 minutes inclusive; longer
// is a long video). It is the one cap every path applies: create
// (resolveVideoContentType), the PATCH category override
// (ValidateCategoryOverride), the measurement (ClassifyVideo) and the
// transcode consumer's reclassification. An unknown duration (<= 0) does not
// exceed it; the consumer applies it again once transcode measures the clip.
func ExceedsShortsCap(durationSeconds float64) bool {
	return durationSeconds > float64(flickMaxDurationSeconds)
}

// validContentTypes is the allowed set for content_type.
// "voice" is Module 1 P0-6: a voice-only post (audio media + optional
// text). Mixed voice carousels are deferred.
var validContentTypes = map[string]bool{
	"post": true, "poll": true, "reel": true, "video": true,
	"flick": true, "long_video": true, "voice": true,
}

// isVoiceContentType reports whether a post is a voice post.
func isVoiceContentType(ct string) bool { return ct == "voice" }

// classifyVideoContentType returns "flick" or "long_video" based on duration and dimensions.
// Legacy callers: "reel" maps to "flick", "video" maps to "long_video".
func classifyVideoContentType(durationSeconds int) string {
	if durationSeconds > 0 && !ExceedsShortsCap(float64(durationSeconds)) {
		return "flick"
	}
	return "long_video"
}

// ClassifyVideo returns the computed category and orientation based on
// duration and dimensions. This is the *measurement*, recorded on
// video_metadata.computed_category for analytics. It decides a post's
// content_type only when the author expressed no kind — see
// resolveVideoContentType for the rule.
func ClassifyVideo(durationSeconds float64, width, height int) (category, orientation string) {
	orientation = deriveOrientation(width, height)
	if !ExceedsShortsCap(durationSeconds) && (orientation == "portrait" || orientation == "square") {
		return "flick", orientation
	}
	return "long_video", orientation
}

// deriveOrientation returns "portrait", "landscape", or "square" from dimensions.
func deriveOrientation(width, height int) string {
	if width <= 0 || height <= 0 {
		return "landscape"
	}
	ratio := float64(width) / float64(height)
	if ratio > 1.05 {
		return "landscape"
	}
	if ratio < 0.95 {
		return "portrait"
	}
	return "square"
}

// resolveVideoContentType decides the content_type of a post that carries a
// video, from the caller's (already-normalized) content_type intent and the
// measured duration + dimensions when transcode has produced them
// (durationSeconds <= 0 means "not yet").
//
// The rule (founder, 2026-09-04/05): a reel is what the author posted as a
// reel; a video is what the author posted as a video. An explicit "flick"
// stays a flick even when the frame is landscape, but not when the clip runs
// past the Shorts cap (founder, 2026-10-21): a flick over 10 minutes becomes
// a long video, by the same ExceedsShortsCap the PATCH category override
// refuses with, so create cannot post what PATCH forbids. An explicit
// "long_video" stays a long video even when the clip is portrait and short —
// a vertical clip posted from Tube belongs in Tube, not Reels.
// Legacy "reel"/"video" spellings were folded into these before this runs.
//
// And a post is a post (founder, 2026-09-25, superseding the earlier rule's
// last branch). A plain "post" that attaches a video used to be classified
// from the measurement — a short portrait clip became a flick and landed on
// the Reels page, anything else became a long_video and landed on Tube and
// in the creator fund — although the author chose neither; the web composer
// had inferred "video" from a MIME type. Now it stays a post: it shows in
// the feed, not in Reels or Tube, and CanonicalMonetizationType("post") is
// not ok, so it is never priced. Android's feed composer already sent
// "post"; the web one now does too, so both clients mean the same thing.
//
// The measurement branch below is kept for any other intent that reaches
// here with a video attached (a poll, for instance) — unchanged, and out of
// scope of this rule. `explicit` reports whether the answer was the author's
// choice, so the MediaTranscodeConsumer knows to leave it alone; a post is
// reported explicit for exactly that reason.
func resolveVideoContentType(intent string, durationSeconds int, width, height int) (contentType string, explicit bool) {
	switch intent {
	case "flick", "reel":
		if ExceedsShortsCap(float64(durationSeconds)) {
			return "long_video", true
		}
		return "flick", true
	case "long_video", "video":
		return "long_video", true
	case "post":
		return "post", true
	}
	if durationSeconds <= 0 {
		return "long_video", false
	}
	cat, _ := ClassifyVideo(float64(durationSeconds), width, height)
	return cat, false
}

// ValidateCategoryOverride checks if a category override request is valid.
// This guards the PATCH category endpoint only: an author may always move a
// video to long_video, but may only call it a flick when the measurement
// allows (≤ flickMaxDurationSeconds, not landscape).
func ValidateCategoryOverride(vm *postgres.VideoMetadata, requested string) error {
	if requested == "flick" {
		if ExceedsShortsCap(vm.DurationSeconds) {
			return fmt.Errorf("cannot classify as flick: duration exceeds %ds", flickMaxDurationSeconds)
		}
		if vm.Orientation == "landscape" {
			return fmt.Errorf("cannot classify as flick: landscape orientation")
		}
	}
	return nil // long_video is always valid
}

// CanonicalContentType normalizes a client-supplied content type (legacy
// spellings included) and reports whether it is one this service accepts.
func CanonicalContentType(ct string) (string, bool) {
	if ct == "" {
		return "", false
	}
	ct = normalizeLegacyContentType(ct)
	return ct, validContentTypes[ct]
}

// normalizeLegacyContentType maps old content types to new ones.
func normalizeLegacyContentType(contentType string) string {
	switch contentType {
	case "reel":
		return "flick"
	case "video":
		return "long_video"
	default:
		return contentType
	}
}

// isVideoContentType reports whether a post content_type carries video
// (short-form reel / flick or long-form). Used to scope auto-moderation.
func isVideoContentType(ct string) bool {
	switch ct {
	case "reel", "flick", "long_video", "video":
		return true
	}
	return false
}
