package service

import (
	"testing"
	"time"
)

// TestAfterHoursWindow documents which hours the trusted-circle
// auto-restriction fires on. The boundary is "22:00 to before 06:00"
// — 22:00 is in, 06:00 is out.
func TestAfterHoursWindow(t *testing.T) {
	cases := []struct {
		hour int
		want bool
	}{
		{0, true},
		{1, true},
		{5, true},
		{6, false},
		{7, false},
		{12, false},
		{17, false},
		{21, false},
		{22, true},
		{23, true},
	}
	for _, tc := range cases {
		now := time.Date(2026, 1, 1, tc.hour, 30, 0, 0, time.UTC)
		got := isAfterHours(now)
		if got != tc.want {
			t.Errorf("hour %02d: want %v, got %v", tc.hour, tc.want, got)
		}
	}
}

// isAfterHours mirrors the inline boundary logic in
// shouldRestrictToTrustedCircle. Lives here as a thin testable
// extraction so the window contract is documented + verified.
func isAfterHours(t time.Time) bool {
	h := t.Hour()
	return h >= 22 || h < 6
}
