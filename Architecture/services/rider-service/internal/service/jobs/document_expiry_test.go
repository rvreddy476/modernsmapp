package jobs

import (
	"testing"
	"time"
)

// TestPickDocBucket_AllBuckets walks every threshold to make sure we pick
// the smallest-applicable bucket. The "smallest first" rule is what
// prevents a doc 12 days from expiry getting a 30d reminder when the
// 14d bucket is the right fit.
func TestPickDocBucket_AllBuckets(t *testing.T) {
	cases := []struct {
		name     string
		until    time.Duration
		bucket   string
	}{
		{"already_expired", -1 * time.Hour, "expired"},
		{"exactly_now", 0, "expired"},
		{"23h", 23 * time.Hour, "1d"},
		{"1d", 24 * time.Hour, "1d"},
		{"3d_minus_1h", 71 * time.Hour, "3d"},
		{"3d", 3 * 24 * time.Hour, "3d"},
		{"6d", 6 * 24 * time.Hour, "7d"},
		{"7d", 7 * 24 * time.Hour, "7d"},
		{"14d_minus_1h", 14*24*time.Hour - time.Hour, "14d"},
		{"14d", 14 * 24 * time.Hour, "14d"},
		{"21d", 21 * 24 * time.Hour, "30d"},
		{"30d", 30 * 24 * time.Hour, "30d"},
		{"31d", 31 * 24 * time.Hour, ""},
		{"60d", 60 * 24 * time.Hour, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pickDocBucket(c.until)
			if got != c.bucket {
				t.Errorf("pickDocBucket(%v) = %q, want %q", c.until, got, c.bucket)
			}
		})
	}
}

// TestPickDocBucket_SmallestWins verifies the "smallest applicable
// bucket" rule explicitly: at exactly 1d, we pick "1d" not "3d".
func TestPickDocBucket_SmallestWins(t *testing.T) {
	got := pickDocBucket(24 * time.Hour)
	if got != "1d" {
		t.Errorf("at 24h we should pick 1d, got %q", got)
	}
}
