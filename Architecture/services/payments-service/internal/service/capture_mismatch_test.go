package service

import "testing"

// A refused capture is refunded unless it is a lower capture while bank offers
// are on (the offer rule's alarm), and only for a capture.
func TestShouldRefundMismatch(t *testing.T) {
	for _, tc := range []struct {
		status        string
		lower, offers bool
		want          bool
	}{
		{"succeeded", false, false, true},
		{"succeeded", true, false, true},
		{"succeeded", false, true, true},
		{"succeeded", true, true, false},
		{"failed", false, false, false},
		{"refunded", false, false, false},
	} {
		if got := shouldRefundMismatch(tc.status, tc.lower, tc.offers); got != tc.want {
			t.Errorf("shouldRefundMismatch(%s, lower=%v, offers=%v) = %v, want %v", tc.status, tc.lower, tc.offers, got, tc.want)
		}
	}
}
