package postgres

import (
	"testing"
	"time"
)

// Food server lane (9 Nov 2026): seconds_to_expiry is the server's own count,
// rounded down and never negative, from expires_at as Postgres writes it.
func TestOfferSecondsToExpiry(t *testing.T) {
	now := time.Date(2026, 9, 13, 6, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		expires string
		want    *int64
	}{
		{"2026-09-13 06:30:20+00", ptr64(20)},
		{"2026-09-13 06:30:19.900001+00", ptr64(19)},
		{"2026-09-13 12:00:20+05:30", ptr64(20)},
		{"2026-09-13T06:30:05Z", ptr64(5)},
		{"2026-09-13 06:30:00+00", ptr64(0)},
		{"2026-09-13 06:29:00+00", ptr64(0)},
		{"not a time", nil},
	} {
		got := OfferSecondsToExpiry(tc.expires, now)
		switch {
		case tc.want == nil && got != nil:
			t.Fatalf("%q: got %d, want absent", tc.expires, *got)
		case tc.want != nil && (got == nil || *got != *tc.want):
			t.Fatalf("%q: got %v, want %d", tc.expires, got, *tc.want)
		}
	}
}

func ptr64(v int64) *int64 { return &v }
