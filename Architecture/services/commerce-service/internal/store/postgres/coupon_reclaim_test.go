package postgres

import (
	"errors"
	"testing"
	"time"
)

// A payment retry re-claims its coupon only while the coupon is live; a coupon
// switched off or expired since checkout refuses the retry with its own code.
func TestReclaimRefusal(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Minute), now.Add(time.Hour)
	for _, tc := range []struct {
		name      string
		active    bool
		expiresAt *time.Time
		want      error
		code      string
	}{
		{"live, no expiry", true, nil, nil, ""},
		{"live, expires later", true, &future, nil, ""},
		{"switched off", false, nil, ErrCouponInvalid, "COUPON_INVALID"},
		{"switched off and expired", false, &past, ErrCouponInvalid, "COUPON_INVALID"},
		{"expired", true, &past, ErrCouponExpired, "COUPON_EXPIRED"},
		{"expires this instant", true, &now, ErrCouponExpired, "COUPON_EXPIRED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := reclaimRefusal(tc.active, tc.expiresAt, now)
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("reclaimRefusal = %v, want %v", err, tc.want)
			}
			if code, _ := CouponRefusalCode(err); code != tc.code {
				t.Fatalf("refusal code = %q, want %q", code, tc.code)
			}
		})
	}
}
