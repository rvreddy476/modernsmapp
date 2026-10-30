package postgres

import (
	"errors"
	"testing"
)

// Audit 2026-10-30 M2: the substitution price rule in integer paise.
func TestPriceSubstitution(t *testing.T) {
	cases := []struct {
		name                string
		orig, sugg, qty, bp int64
		supply, tax, price  int64
		err                 error
	}{
		{"cheaper at 5%", 25000, 15000, 1, 500, -10000, -500, -10500, nil},
		{"cheaper x2 at 18%, half up", 10050, 10000, 2, 1800, -100, -18, -118, nil},
		{"half paise rounds away from zero", 1000, 990, 1, 500, -10, -1, -11, nil},
		{"same price moves nothing", 25000, 25000, 3, 500, 0, 0, 0, nil},
		{"no tax rate", 25000, 20000, 1, 0, -5000, 0, -5000, nil},
		{"dearer by one paisa is refused", 25000, 25001, 1, 0, 0, 0, 0, ErrSubstitutionPriceIncrease},
		{"dearer is refused", 25000, 45000, 1, 500, 0, 0, 0, ErrSubstitutionPriceIncrease},
		{"zero quantity is invalid", 25000, 1000, 0, 500, 0, 0, 0, ErrSubstitutionItemInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PriceSubstitution(tc.orig, tc.sugg, tc.qty, tc.bp)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if tc.err != nil {
				return
			}
			if got.SupplyDiffPaise != tc.supply || got.TaxDiffPaise != tc.tax || got.PriceDiffPaise != tc.price {
				t.Fatalf("got %+v, want supply %d tax %d price %d", got, tc.supply, tc.tax, tc.price)
			}
		})
	}
}
