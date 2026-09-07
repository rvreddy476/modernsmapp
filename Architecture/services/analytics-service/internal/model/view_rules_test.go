package model

import (
	"testing"

	"github.com/atpost/shared/postclassify"
)

// The bug this file pins down: analytics used to own a second
// content-type rule (durationMS <= 90000 -> "reel") that disagreed with
// shared/postclassify (<=300s portrait/square -> "flick"). The label now
// comes from postclassify alone, and the 90 seconds survives only as the
// view-counting bar it always was.

func TestShortFormViewBarIsNotAContentTypeBoundary(t *testing.T) {
	// A two-minute flick: short-form by kind (it is in the reels feed and
	// is settled at the flick RPM), but too long for the three-second
	// display-view bar.
	const twoMinutes = 120_000

	if !postclassify.IsShortForm(ContentTypeFlick) {
		t.Fatalf("flick must be short-form by kind")
	}
	if got := postclassify.Classify(120, 1080, 1920); got != postclassify.Flick {
		t.Fatalf("a 120s portrait video is a flick, got %q", got)
	}

	if IsDisplayView(ContentTypeFlick, twoMinutes, 3_000, 2.5, 0) {
		t.Fatalf("3s of a two-minute flick must not count as a display view")
	}
	if !IsDisplayView(ContentTypeFlick, twoMinutes, 30_000, 25.0, 0) {
		t.Fatalf("30s of a two-minute flick must count as a display view")
	}
}

func TestDisplayViewBars(t *testing.T) {
	const (
		shortFlick = 30_000  // 30s
		longFlick  = 240_000 // 4 min — still a flick, over the view bar
		tube       = 600_000 // 10 min long_video
	)

	cases := []struct {
		name          string
		contentType   string
		durationMS    int64
		watchedMS     int64
		percentViewed float64
		loopCount     int
		want          bool
	}{
		// Short-form bar: 3s / 25% / one loop under 3s.
		{"flick 3s watched", ContentTypeFlick, shortFlick, 3_000, 10, 0, true},
		{"flick 2.9s watched, under 25%", ContentTypeFlick, shortFlick, 2_900, 9.6, 0, false},
		{"flick under 3s but 25% viewed", ContentTypeFlick, shortFlick, 2_000, 25.0, 0, true},
		{"legacy 'reel' label gets the same bar", ContentTypeReel, shortFlick, 3_000, 10, 0, true},
		{"legacy 'short' label gets the same bar", "short", shortFlick, 3_000, 10, 0, true},
		{"sub-3s flick needs a full loop", ContentTypeFlick, 2_000, 900, 45, 0, true},
		{"sub-3s flick, one loop, tiny watch", ContentTypeFlick, 2_000, 400, 20, 1, true},
		{"sub-3s flick, no loop, tiny watch", ContentTypeFlick, 2_000, 400, 20, 0, false},

		// Exactly on the bar is still short form.
		{"flick exactly 90s", ContentTypeFlick, ShortFormViewRuleMaxDurationMS, 3_000, 3.4, 0, true},
		{"flick one ms over 90s", ContentTypeFlick, ShortFormViewRuleMaxDurationMS + 1, 3_000, 3.4, 0, false},

		// Long-form bar: 30s / (under 60s) 50%.
		{"4-min flick, 3s watched", ContentTypeFlick, longFlick, 3_000, 1.25, 0, false},
		{"4-min flick, 30s watched", ContentTypeFlick, longFlick, 30_000, 12.5, 0, true},
		{"long_video 30s watched", ContentTypeLongVideo, tube, 30_000, 5, 0, true},
		{"long_video 29s watched", ContentTypeLongVideo, tube, 29_000, 4.8, 0, false},
		{"legacy 'video' label gets the long bar", "video", tube, 29_000, 4.8, 0, false},

		// A short Tube post stays long-form by the author's choice, so it
		// keeps the long-form bar it has always had.
		{"50s long_video, 3s watched", ContentTypeLongVideo, 50_000, 3_000, 6, 0, false},
		{"50s long_video, 50% watched", ContentTypeLongVideo, 50_000, 25_000, 50, 0, true},

		// Unknown duration: the label is the only evidence.
		{"flick, no duration reported", ContentTypeFlick, 0, 3_000, 0, 0, true},
		{"long_video, no duration reported", ContentTypeLongVideo, 0, 3_000, 0, 0, false},

		// Unlabelled ownership rows ("post", or pre-split legacy).
		{"unlabelled short item", "post", 40_000, 3_000, 7.5, 0, true},
		{"unlabelled long item, 3s", "post", 300_000, 3_000, 1, 0, false},
		{"unlabelled long item, 30s", "post", 300_000, 30_000, 10, 0, true},
		{"unlabelled, no duration", "post", 0, 3_000, 0, 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsDisplayView(tc.contentType, tc.durationMS, tc.watchedMS, tc.percentViewed, tc.loopCount)
			if got != tc.want {
				t.Fatalf("IsDisplayView(%q, %d, %d, %.2f, %d) = %v, want %v",
					tc.contentType, tc.durationMS, tc.watchedMS, tc.percentViewed, tc.loopCount, got, tc.want)
			}
		})
	}
}

// The view bar must stay inside the range postclassify calls a flick,
// otherwise the two rules would be arguing again from the other side.
func TestShortFormViewBarSitsInsideTheFlickRange(t *testing.T) {
	flickMaxMS := int64(postclassify.FlickMaxDurationSeconds) * 1000
	if ShortFormViewRuleMaxDurationMS > flickMaxMS {
		t.Fatalf("short-form view bar (%dms) exceeds the flick ceiling (%dms): "+
			"a long_video would be eligible for the reel view threshold",
			int64(ShortFormViewRuleMaxDurationMS), flickMaxMS)
	}
}

func TestContentTypeConstantsTrackPostclassify(t *testing.T) {
	if ContentTypeFlick != postclassify.Flick {
		t.Fatalf("ContentTypeFlick=%q, postclassify.Flick=%q", ContentTypeFlick, postclassify.Flick)
	}
	if ContentTypeLongVideo != postclassify.LongVideo {
		t.Fatalf("ContentTypeLongVideo=%q, postclassify.LongVideo=%q", ContentTypeLongVideo, postclassify.LongVideo)
	}
	if !postclassify.IsShortForm(ContentTypeReel) {
		t.Fatalf("legacy %q must still read as short form", ContentTypeReel)
	}
}

func TestIsEngagedView(t *testing.T) {
	if !IsEngagedView(10_000, 0, false) {
		t.Fatalf("10s watched is an engaged view")
	}
	if !IsEngagedView(0, 50, false) {
		t.Fatalf("50%% viewed is an engaged view")
	}
	if !IsEngagedView(0, 0, true) {
		t.Fatalf("engagement alone is an engaged view")
	}
	if IsEngagedView(9_999, 49.9, false) {
		t.Fatalf("just under every bar is not an engaged view")
	}
}
