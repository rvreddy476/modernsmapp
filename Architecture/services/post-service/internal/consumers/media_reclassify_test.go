package consumers

import "testing"

// After transcode measures a video, the consumer may only rewrite the
// content_type of a post whose kind nobody chose. A reel is what the author
// posted as a reel; a video is what the author posted as a video.
func TestReclassifyDecisionNeverDowngradesAnExplicitKind(t *testing.T) {
	cases := []struct {
		name     string
		current  string
		explicit bool
		measured string
		wantType string
		wantKeep bool
	}{
		// Explicit kinds stand whatever the measurement says.
		{"explicit flick measured long_video (landscape/long reel)", "flick", true, "long_video", "flick", true},
		{"explicit long_video measured flick (short vertical from Tube)", "long_video", true, "flick", "long_video", true},
		{"explicit flick measured flick", "flick", true, "flick", "flick", true},
		{"explicit long_video measured long_video", "long_video", true, "long_video", "long_video", true},
		// A flick is never downgraded even on rows that predate the flag.
		{"legacy flick measured long_video", "flick", false, "long_video", "flick", true},
		// A plain post that defaulted to long_video follows the measurement.
		{"defaulted long_video measured flick → flick", "long_video", false, "flick", "flick", false},
		{"defaulted long_video measured long_video → unchanged", "long_video", false, "long_video", "long_video", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, keep := reclassifyDecision(tc.current, tc.explicit, tc.measured)
			if got != tc.wantType || keep != tc.wantKeep {
				t.Fatalf("reclassifyDecision(%q, explicit=%v, measured=%q) = (%q, keep=%v), want (%q, keep=%v)",
					tc.current, tc.explicit, tc.measured, got, keep, tc.wantType, tc.wantKeep)
			}
		})
	}
}
