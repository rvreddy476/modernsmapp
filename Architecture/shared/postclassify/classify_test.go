package postclassify

import "testing"

// TestClassify pins down the rule. Any future tweak (e.g. lift the
// duration cap) needs to come through this table so the change is
// explicit + the rationale lives next to the test case.
func TestClassify(t *testing.T) {
	cases := []struct {
		name     string
		duration int
		w, h     int
		want     string
	}{
		// Happy paths
		{"30s portrait → flick", 30, 720, 1280, Flick},
		{"180s portrait at the cap → flick", 180, 720, 1280, Flick},
		{"square video at boundary → flick", 60, 1080, 1080, Flick},

		// Long-form paths
		{"181s portrait → long_video (over cap)", 181, 720, 1280, LongVideo},
		{"30s landscape → long_video (orientation)", 30, 1920, 1080, LongVideo},
		{"5min landscape → long_video", 300, 1920, 1080, LongVideo},

		// Unknown-input paths default safely to long_video
		{"duration 0 → long_video (unknown)", 0, 720, 1280, LongVideo},
		{"width 0 → long_video (unknown)", 30, 0, 1280, LongVideo},
		{"height 0 → long_video (unknown)", 30, 720, 0, LongVideo},
		{"all zero → long_video", 0, 0, 0, LongVideo},

		// Negatives treated as unknown
		{"negative duration → long_video", -1, 720, 1280, LongVideo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.duration, tc.w, tc.h)
			if got != tc.want {
				t.Errorf("Classify(%d, %d, %d): got %q, want %q",
					tc.duration, tc.w, tc.h, got, tc.want)
			}
		})
	}
}

func TestIsShortForm(t *testing.T) {
	for _, ct := range []string{"flick", "reel", "short"} {
		if !IsShortForm(ct) {
			t.Errorf("IsShortForm(%q) = false, want true", ct)
		}
	}
	for _, ct := range []string{"long_video", "video", "post", "poll", ""} {
		if IsShortForm(ct) {
			t.Errorf("IsShortForm(%q) = true, want false", ct)
		}
	}
}

func TestIsLongForm(t *testing.T) {
	for _, ct := range []string{"long_video", "video"} {
		if !IsLongForm(ct) {
			t.Errorf("IsLongForm(%q) = false, want true", ct)
		}
	}
	for _, ct := range []string{"flick", "reel", "short", "post", "poll", ""} {
		if IsLongForm(ct) {
			t.Errorf("IsLongForm(%q) = true, want false", ct)
		}
	}
}
