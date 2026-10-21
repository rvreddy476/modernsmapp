package processing

import (
	"testing"

	"github.com/atpost/shared/postclassify"
)

// The worker's short-form encode (fast preset, 720p cap) follows the same
// rule post-service classifies by: ≤ 600 s (founder, 2026-10-21) AND
// portrait or square. Landscape keeps its 1080p/4K rungs at any length.
func TestIsShortFormEncodeAtTenMinutes(t *testing.T) {
	if ReelMaxDurationSeconds != postclassify.FlickMaxDurationSeconds {
		t.Fatalf("ReelMaxDurationSeconds = %d, want postclassify's %d", ReelMaxDurationSeconds, postclassify.FlickMaxDurationSeconds)
	}
	if ReelMaxDurationSeconds != 600 {
		t.Fatalf("ReelMaxDurationSeconds = %d, want 600", ReelMaxDurationSeconds)
	}
	meta := func(sec, w, h int) *VideoMeta { return &VideoMeta{DurationSeconds: sec, Width: w, Height: h} }
	cases := []struct {
		name string
		meta *VideoMeta
		want bool
	}{
		{"599 s portrait", meta(599, 1080, 1920), true},
		{"600 s portrait (inclusive)", meta(600, 1080, 1920), true},
		{"601 s portrait", meta(601, 1080, 1920), false},
		{"600 s square", meta(600, 1080, 1080), true},
		{"601 s square", meta(601, 1080, 1080), false},
		{"301 s portrait (old cap + 1) is now short-form", meta(301, 720, 1280), true},
		{"599 s landscape keeps 1080p", meta(599, 1920, 1080), false},
		{"600 s landscape keeps 1080p", meta(600, 1920, 1080), false},
		{"60 s landscape keeps 1080p", meta(60, 1920, 1080), false},
		{"3 h landscape", meta(3*60*60, 3840, 2160), false},
		{"unknown duration", meta(0, 1080, 1920), false},
		{"unknown dimensions", meta(30, 0, 0), false},
		{"nil meta", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsShortFormEncode(tc.meta); got != tc.want {
				t.Fatalf("IsShortFormEncode(%+v) = %v, want %v", tc.meta, got, tc.want)
			}
		})
	}
}
