package service

import (
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
)

// A reel is what the author posted as a reel; a video is what the author
// posted as a video (founder, 2026-09-04/05) — and a post is a post
// (founder, 2026-09-25): a plain "post" that carries a video stays a post,
// whatever the clip measures, and is reported explicit so the transcode
// consumer leaves it alone.
func TestResolveVideoContentTypeHonoursExplicitIntent(t *testing.T) {
	const short, long = 60, flickMaxDurationSeconds + 1
	cases := []struct {
		name         string
		intent       string
		duration     int
		w, h         int
		wantType     string
		wantExplicit bool
	}{
		// The author's kind is the kind, whatever the frame or the clock says.
		{"flick + landscape stays flick", "flick", short, 1920, 1080, "flick", true},
		{"flick + long stays flick", "flick", long, 1080, 1920, "flick", true},
		{"legacy reel + landscape stays flick", "reel", short, 1920, 1080, "flick", true},
		{"long_video + portrait short stays long_video", "long_video", short, 1080, 1920, "long_video", true},
		{"long_video + square short stays long_video", "long_video", short, 1080, 1080, "long_video", true},
		{"legacy video + portrait short stays long_video", "video", short, 1080, 1920, "long_video", true},
		// Pending transcode: intent still wins, and is still explicit.
		{"flick pending transcode", "flick", 0, 0, 0, "flick", true},
		{"long_video pending transcode", "long_video", 0, 0, 0, "long_video", true},
		// A post is a post: the measurement that used to make these a flick
		// or a long_video no longer applies, and the answer is explicit.
		{"post + portrait short stays post (was flick)", "post", short, 1080, 1920, "post", true},
		{"post + square short stays post (was flick)", "post", short, 1080, 1080, "post", true},
		{"post + landscape stays post (was long_video)", "post", short, 1920, 1080, "post", true},
		{"post + portrait long stays post (was long_video)", "post", long, 1080, 1920, "post", true},
		{"post pending transcode stays post (was long_video)", "post", 0, 0, 0, "post", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, explicit := resolveVideoContentType(tc.intent, tc.duration, tc.w, tc.h)
			if got != tc.wantType || explicit != tc.wantExplicit {
				t.Fatalf("resolveVideoContentType(%q, %d, %dx%d) = (%q, %v), want (%q, %v)",
					tc.intent, tc.duration, tc.w, tc.h, got, explicit, tc.wantType, tc.wantExplicit)
			}
		})
	}
}

// The PATCH category endpoint keeps its own guard: long_video is always
// allowed, flick only when the measurement permits it.
func TestValidateCategoryOverrideUnchanged(t *testing.T) {
	meta := func(duration float64, orientation string) *postgres.VideoMetadata {
		return &postgres.VideoMetadata{DurationSeconds: duration, Orientation: orientation}
	}
	if err := ValidateCategoryOverride(meta(60, "landscape"), "flick"); err == nil {
		t.Fatal("landscape → flick override accepted")
	}
	if err := ValidateCategoryOverride(meta(60, "landscape"), "long_video"); err != nil {
		t.Fatalf("landscape → long_video override refused: %v", err)
	}
	if err := ValidateCategoryOverride(meta(flickMaxDurationSeconds+1, "portrait"), "flick"); err == nil {
		t.Fatal("over-length → flick override accepted")
	}
	if err := ValidateCategoryOverride(meta(60, "portrait"), "flick"); err != nil {
		t.Fatalf("portrait short → flick override refused: %v", err)
	}
}
