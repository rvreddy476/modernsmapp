package postgres

import "testing"

// Audit 2026-10-30 M3: which cancels give the coupon back. Only an order that
// took no money; a paid (or refunding) order's coupon was redeemed.
func TestReleasesCouponOnCancel(t *testing.T) {
	for st, want := range map[string]bool{
		"pending": true, "processing": true, "failed": true,
		"paid": false, "refund_pending": false, "partially_refunded": false, "refunded": false,
		"": false, "unknown": false,
	} {
		if got := releasesCouponOnCancel(st); got != want {
			t.Errorf("releasesCouponOnCancel(%q) = %v, want %v", st, got, want)
		}
	}
}
