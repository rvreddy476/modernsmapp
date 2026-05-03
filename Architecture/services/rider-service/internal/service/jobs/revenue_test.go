package jobs

import (
	"testing"
	"time"
)

// TestDayBoundsIST_BoundariesAreOneDayApart verifies the IST window is
// exactly 24 hours wide and IST midnight aligns with UTC 18:30 of the
// previous day (IST = UTC + 5:30).
func TestDayBoundsIST_BoundariesAreOneDayApart(t *testing.T) {
	ref := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC) // noon UTC = 17:30 IST
	startUTC, endUTC := dayBoundsIST(ref)
	if endUTC.Sub(startUTC) != 24*time.Hour {
		t.Errorf("IST day length = %v, want 24h", endUTC.Sub(startUTC))
	}
}

// TestDayBoundsIST_StartIsISTMidnight ensures the start aligns with
// 00:00 IST (= 18:30 UTC of the previous day).
func TestDayBoundsIST_StartIsISTMidnight(t *testing.T) {
	ref := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC) // 05:30 IST on May 2
	startUTC, _ := dayBoundsIST(ref)
	// IST midnight on May 2 == 18:30 UTC on May 1.
	wantStart := time.Date(2026, 5, 1, 18, 30, 0, 0, time.UTC)
	if !startUTC.Equal(wantStart) {
		t.Errorf("start = %v, want %v", startUTC, wantStart)
	}
}

// TestDayBoundsIST_NearMidnightIST verifies a UTC time just after IST
// midnight returns the new IST day. 19:00 UTC = 00:30 IST next day.
func TestDayBoundsIST_NearMidnightIST(t *testing.T) {
	ref := time.Date(2026, 5, 1, 19, 0, 0, 0, time.UTC)
	startUTC, endUTC := dayBoundsIST(ref)
	if !startUTC.Equal(time.Date(2026, 5, 1, 18, 30, 0, 0, time.UTC)) {
		t.Errorf("start = %v, want 2026-05-01T18:30Z", startUTC)
	}
	if !endUTC.Equal(time.Date(2026, 5, 2, 18, 30, 0, 0, time.UTC)) {
		t.Errorf("end = %v, want 2026-05-02T18:30Z", endUTC)
	}
}
