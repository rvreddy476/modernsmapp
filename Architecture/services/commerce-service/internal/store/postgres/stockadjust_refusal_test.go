package postgres

import (
	"errors"
	"testing"
)

// Removing more stock than exists is a typed refusal (409 INSUFFICIENT_STOCK
// at the edge), not a bare error the handler answered as 500. Below the
// reserved line stays ErrStockBelowReserved.
func TestStockAfter(t *testing.T) {
	cases := []struct {
		name                   string
		total, reserved, delta int
		want                   int
		wantErr                error
	}{
		{"restock", 3, 0, 10, 13, nil},
		{"write down to zero", 3, 0, -3, 0, nil},
		{"write down to the reserved line", 10, 4, -6, 4, nil},
		{"one more than exists", 3, 0, -4, 0, ErrInsufficientStock},
		{"from nothing", 0, 0, -1, 0, ErrInsufficientStock},
		{"more than exists with reservations", 5, 2, -6, 0, ErrInsufficientStock},
		{"into the reserved units", 10, 4, -7, 0, ErrStockBelowReserved},
	}
	for _, c := range cases {
		got, err := stockAfter(c.total, c.reserved, c.delta)
		if c.wantErr == nil {
			if err != nil || got != c.want {
				t.Errorf("%s: got %d, %v; want %d", c.name, got, err, c.want)
			}
			continue
		}
		if !errors.Is(err, c.wantErr) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.wantErr)
		}
		if c.wantErr == ErrInsufficientStock && errors.Is(err, ErrStockBelowReserved) {
			t.Errorf("%s: an over-removal is also reported as a reservation breach", c.name)
		}
	}
}
