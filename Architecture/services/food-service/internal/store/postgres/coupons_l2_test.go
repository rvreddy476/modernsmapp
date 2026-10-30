package postgres

import (
	"errors"
	"testing"
)

// Audit 2026-10-30 L2: coupon maths in integer paise.
func TestCouponDiscountPaise(t *testing.T) {
	cases := []struct {
		name     string
		c        CouponTerms
		subtotal int64
		want     int64
		err      error
	}{
		{"flat", CouponTerms{Type: "FLAT", ValueHundredths: 5000, MaxDiscountPaise: 5000}, 30000, 5000, nil},
		{"flat capped at subtotal", CouponTerms{Type: "FLAT", ValueHundredths: 5000, MaxDiscountPaise: 5000}, 3000, 3000, nil},
		{"10% of 333.33 rounds half up", CouponTerms{Type: "PERCENTAGE", ValueHundredths: 1000, MaxDiscountPaise: 100000}, 33333, 3333, nil},
		{"12.5% of 1.00 is 12.5 paise -> 13", CouponTerms{Type: "PERCENTAGE", ValueHundredths: 1250, MaxDiscountPaise: 100000}, 100, 13, nil},
		{"percentage capped at max", CouponTerms{Type: "PERCENTAGE", ValueHundredths: 5000, MaxDiscountPaise: 10000}, 100000, 10000, nil},
		{"percentage just over max is capped", CouponTerms{Type: "PERCENTAGE", ValueHundredths: 1500, MaxDiscountPaise: 10000}, 100000, 10000, nil},
		{"under the minimum order", CouponTerms{Type: "FLAT", ValueHundredths: 5000, MaxDiscountPaise: 5000, MinOrderPaise: 19900}, 19899, 0, ErrCouponInvalid},
		{"exactly the minimum order", CouponTerms{Type: "FLAT", ValueHundredths: 5000, MaxDiscountPaise: 5000, MinOrderPaise: 19900}, 19900, 5000, nil},
		{"empty cart", CouponTerms{Type: "FLAT", ValueHundredths: 5000, MaxDiscountPaise: 5000}, 0, 0, ErrCouponInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CouponDiscountPaise(tc.c, tc.subtotal)
			if !errors.Is(err, tc.err) || got != tc.want {
				t.Fatalf("got %d, %v; want %d, %v", got, err, tc.want, tc.err)
			}
		})
	}
}
