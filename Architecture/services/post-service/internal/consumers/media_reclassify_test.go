package consumers

import "testing"

// After transcode measures a video, the consumer may only rewrite the
// content_type of a post whose kind nobody chose. A reel is what the author
// posted as a reel; a video is what the author posted as a video — within
// the Shorts cap: a flick measured over 10 minutes becomes a long video.
func TestReclassifyDecisionNeverDowngradesAnExplicitKind(t *testing.T) {
	const short, overCap = 60, 601
	cases := []struct {
		name     string
		current  string
		explicit bool
		measured string
		duration int
		wantType string
		wantKeep bool
	}{
		// Explicit kinds stand whatever the frame says.
		{"explicit flick measured long_video (landscape reel)", "flick", true, "long_video", short, "flick", true},
		{"explicit long_video measured flick (short vertical from Tube)", "long_video", true, "flick", short, "long_video", true},
		{"explicit flick measured flick", "flick", true, "flick", short, "flick", true},
		{"explicit flick at exactly 10 minutes", "flick", true, "flick", 600, "flick", true},
		{"explicit long_video measured long_video", "long_video", true, "long_video", overCap, "long_video", true},
		// A flick is never downgraded by its frame, even on rows that
		// predate the flag.
		{"legacy flick measured long_video (landscape)", "flick", false, "long_video", short, "flick", true},
		// ...but the Shorts cap holds whatever the flag (founder, 2026-10-21).
		{"explicit flick over 10 minutes → long_video", "flick", true, "long_video", overCap, "long_video", false},
		{"legacy flick over 10 minutes → long_video", "flick", false, "long_video", overCap, "long_video", false},
		// A post is a post (founder, 2026-09-25): never rewritten by a
		// measurement, whether or not the explicit flag is set.
		{"post measured flick stays post", "post", false, "flick", short, "post", true},
		{"post measured long_video stays post", "post", false, "long_video", overCap, "post", true},
		{"explicit post measured flick stays post", "post", true, "flick", short, "post", true},
		// A plain post that defaulted to long_video follows the measurement.
		{"defaulted long_video measured flick → flick", "long_video", false, "flick", short, "flick", false},
		{"defaulted long_video measured long_video → unchanged", "long_video", false, "long_video", overCap, "long_video", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, keep := reclassifyDecision(tc.current, tc.explicit, tc.measured, tc.duration)
			if got != tc.wantType || keep != tc.wantKeep {
				t.Fatalf("reclassifyDecision(%q, explicit=%v, measured=%q, %ds) = (%q, keep=%v), want (%q, keep=%v)",
					tc.current, tc.explicit, tc.measured, tc.duration, got, keep, tc.wantType, tc.wantKeep)
			}
		})
	}
}
