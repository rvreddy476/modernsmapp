package postgres

import "testing"

// duration_ms on the wire (Tube, 2026-09-05): ffprobe milliseconds when the
// row has them, the legacy whole-second column otherwise, 0 when unknown.
func TestDurationMsValue(t *testing.T) {
	ms, sec := 5070, 5
	cases := []struct {
		name string
		m    *MediaAsset
		want int
	}{
		{"nil asset", nil, 0},
		{"image without duration", &MediaAsset{}, 0},
		{"ffprobe milliseconds win", &MediaAsset{DurationMs: &ms, DurationSeconds: &sec}, 5070},
		{"legacy row scales seconds", &MediaAsset{DurationSeconds: &sec}, 5000},
	}
	for _, tc := range cases {
		if got := tc.m.DurationMsValue(); got != tc.want {
			t.Errorf("%s: got %d want %d", tc.name, got, tc.want)
		}
	}
}
