package postgres

import (
	"errors"
	"testing"
	"time"
)

// Refund clamping for a bank-offer payment: never more money than was
// captured; a full refund returns exactly the capture; a partial X returns
// floor(X × captured / intent); the refund that completes the order value
// returns whatever is left of the capture.
func TestOfferRefundMoney(t *testing.T) {
	for _, tc := range []struct {
		name                                        string
		intent, captured, committedOrder, committed int64
		x                                           int64
		want                                        int64
		wantErr                                     error
	}{
		{"full refund returns the capture", 100000, 95000, 0, 0, 100000, 95000, nil},
		{"partial floors", 100000, 90001, 0, 0, 33333, 30000, nil},
		{"second partial floors", 100000, 90001, 33333, 30000, 33333, 30000, nil},
		{"final partial takes the rest", 100000, 90001, 66666, 60000, 33334, 30001, nil},
		{"partial clamped to what is left", 100000, 95000, 10000, 94990, 50000, 10, nil},
		{"rounds to zero is refused", 100000, 90000, 0, 0, 1, 0, ErrOfferRefundRoundsToZero},
		{"nothing left is refused", 100000, 95000, 50000, 95000, 10000, 0, ErrOfferRefundExceedsCaptured},
		{"no overflow at crore scale", 9_000_000_000_00, 8_999_999_999_99, 0, 0, 4_500_000_000_00, 4_499_999_999_99, nil},
	} {
		got, err := OfferRefundMoney(tc.intent, tc.captured, tc.committedOrder, tc.committed, tc.x)
		if tc.wantErr != nil {
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("%s: err = %v, want %v", tc.name, err, tc.wantErr)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s: got %d, %v; want %d", tc.name, got, err, tc.want)
		}
	}
}

// A run of partials ends at exactly the capture, whatever the split.
func TestOfferRefundMoney_PartialsSumToTheCapture(t *testing.T) {
	const intent, captured = 100000, 90001
	splits := [][]int64{{100000}, {1, 99999}, {33333, 33333, 33334}, {7, 13, 99980}, {50000, 25000, 12500, 12500}}
	for _, split := range splits {
		var order, money int64
		for _, x := range split {
			m, err := OfferRefundMoney(intent, captured, order, money, x)
			if errors.Is(err, ErrOfferRefundRoundsToZero) {
				continue // a 1-paise refund of order value is refused, not lost
			}
			if err != nil {
				t.Fatalf("%v: %v", split, err)
			}
			order += x
			money += m
		}
		if order == intent && money != captured {
			t.Errorf("%v: returned %d, captured %d", split, money, captured)
		}
		if money > captured {
			t.Errorf("%v: returned %d > captured %d", split, money, captured)
		}
	}
}

func TestValidateOffer(t *testing.T) {
	now := time.Now()
	ok := PaymentOffer{Title: "10% off", PaymentMethod: "card", DiscountType: "percentage", DiscountValue: 1000,
		FundedBy: "bank", StartsAt: now}
	if err := ValidateOffer(ok); err != nil {
		t.Fatalf("valid offer refused: %v", err)
	}
	neg := int64(0)
	before := now.Add(-time.Hour)
	for name, mut := range map[string]func(o *PaymentOffer){
		"blank title":        func(o *PaymentOffer) { o.Title = " " },
		"bad method":         func(o *PaymentOffer) { o.PaymentMethod = "wallet" },
		"bad type":           func(o *PaymentOffer) { o.DiscountType = "bogo" },
		"pct over 100":       func(o *PaymentOffer) { o.DiscountValue = 10001 },
		"zero value":         func(o *PaymentOffer) { o.DiscountValue = 0 },
		"zero cap":           func(o *PaymentOffer) { o.MaxDiscountMinor = &neg },
		"negative minimum":   func(o *PaymentOffer) { o.MinAmountMinor = -1 },
		"bad funder":         func(o *PaymentOffer) { o.FundedBy = "seller" },
		"ends before starts": func(o *PaymentOffer) { o.EndsAt = &before },
	} {
		o := ok
		mut(&o)
		if err := ValidateOffer(o); !errors.Is(err, ErrInvalidOffer) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	for id, want := range map[string]bool{
		"offer_ANZoaxsOww2X53": true, "offer_ABC123": true, "offer_ABC12": false,
		"offer_ABC-123456": false, "ANZoaxsOww2X53": false, "order_ANZoaxsOww2X53": false,
	} {
		if ValidProviderOfferID(id) != want {
			t.Errorf("ValidProviderOfferID(%q) != %v", id, want)
		}
	}
}
