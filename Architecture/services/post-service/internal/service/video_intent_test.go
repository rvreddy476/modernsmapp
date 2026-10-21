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

// Founder, 2026-10-21: shorts run up to 10 minutes; anything longer is a
// long video automatically. The boundary is inclusive at 600 s and the
// frame still matters — landscape is long-form at any length.
func TestShortsBoundaryIsTenMinutes(t *testing.T) {
	if flickMaxDurationSeconds != 600 {
		t.Fatalf("flickMaxDurationSeconds = %d, want 600", flickMaxDurationSeconds)
	}
	cases := []struct {
		name     string
		duration float64
		w, h     int
		want     string
	}{
		{"599 s portrait", 599, 1080, 1920, "flick"},
		{"600 s portrait (inclusive)", 600, 1080, 1920, "flick"},
		{"601 s portrait", 601, 1080, 1920, "long_video"},
		{"600.4 s portrait (just over)", 600.4, 1080, 1920, "long_video"},
		{"600 s square", 600, 1080, 1080, "flick"},
		{"601 s square", 601, 1080, 1080, "long_video"},
		{"599 s landscape", 599, 1920, 1080, "long_video"},
		{"600 s landscape", 600, 1920, 1080, "long_video"},
		{"601 s landscape", 601, 1920, 1080, "long_video"},
		{"old 5-min boundary + 1 s portrait is now a flick", 301, 1080, 1920, "flick"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := ClassifyVideo(tc.duration, tc.w, tc.h); got != tc.want {
				t.Fatalf("ClassifyVideo(%v, %dx%d) = %q, want %q", tc.duration, tc.w, tc.h, got, tc.want)
			}
		})
	}
	// The integer path used when no orientation is known.
	for d, want := range map[int]string{599: "flick", 600: "flick", 601: "long_video"} {
		if got := classifyVideoContentType(d); got != want {
			t.Errorf("classifyVideoContentType(%d) = %q, want %q", d, got, want)
		}
	}
	// An unlabelled video (a poll) reaching the measurement branch.
	for d, want := range map[int]string{599: "flick", 600: "flick", 601: "long_video"} {
		if got, explicit := resolveVideoContentType("poll", d, 1080, 1920); got != want || explicit {
			t.Errorf("resolveVideoContentType(poll, %d, portrait) = (%q, %v), want (%q, false)", d, got, explicit, want)
		}
	}
}

func TestValidateCategoryOverrideAtTenMinutes(t *testing.T) {
	meta := func(duration float64, orientation string) *postgres.VideoMetadata {
		return &postgres.VideoMetadata{DurationSeconds: duration, Orientation: orientation}
	}
	for _, d := range []float64{301, 599, 600} {
		if err := ValidateCategoryOverride(meta(d, "portrait"), "flick"); err != nil {
			t.Errorf("%v s portrait → flick refused: %v", d, err)
		}
		if err := ValidateCategoryOverride(meta(d, "square"), "flick"); err != nil {
			t.Errorf("%v s square → flick refused: %v", d, err)
		}
	}
	for _, d := range []float64{600.5, 601, 3 * 60 * 60} {
		if err := ValidateCategoryOverride(meta(d, "portrait"), "flick"); err == nil {
			t.Errorf("%v s portrait → flick accepted", d)
		}
		if err := ValidateCategoryOverride(meta(d, "portrait"), "long_video"); err != nil {
			t.Errorf("%v s portrait → long_video refused: %v", d, err)
		}
	}
	if err := ValidateCategoryOverride(meta(600, "landscape"), "flick"); err == nil {
		t.Error("600 s landscape → flick accepted")
	}
}
