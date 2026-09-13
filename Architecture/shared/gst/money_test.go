package gst

import (
	"errors"
	"testing"
)

// Golden vectors ported from commerce-service/internal/tax/gst_test.go.
func TestExtract_Golden(t *testing.T) {
	cases := []struct {
		net          Paise
		rate         RateBP
		taxable, tax Paise
	}{
		{118000, 1800, 100000, 18000},
		{99, 1800, 83, 16},
		{333, 500, 317, 16},
		{105000, 500, 100000, 5000},
		{0, 1800, 0, 0},
		{12345, 0, 12345, 0},
		{1, 500, 0, 1},
	}
	for _, c := range cases {
		taxable, tax := extract(c.net, c.rate)
		if taxable != c.taxable || tax != c.tax {
			t.Errorf("extract(%d, %d) = %d/%d, want %d/%d", c.net, c.rate, taxable, tax, c.taxable, c.tax)
		}
	}
}

func TestTaxOnExclusive_HalfUp(t *testing.T) {
	cases := []struct {
		taxable Paise
		rate    RateBP
		want    Paise
	}{
		{100000, 500, 5000},
		{10, 500, 1}, // 0.5 paise rounds up
		{9, 500, 0},  // 0.45 paise rounds down
		{50, 1800, 9},
		{0, 1800, 0},
		{77, 0, 0},
		{MaxAmount, 10000, MaxAmount},
	}
	for _, c := range cases {
		if got := taxOnExclusive(c.taxable, c.rate); got != c.want {
			t.Errorf("taxOnExclusive(%d, %d) = %d, want %d", c.taxable, c.rate, got, c.want)
		}
	}
}

func sumPaise(ps []Paise) Paise {
	var s Paise
	for _, p := range ps {
		s += p
	}
	return s
}

func TestAllocate_Golden(t *testing.T) {
	got, err := Allocate(100, []Paise{1, 1, 1})
	if err != nil {
		t.Fatal(err)
	}
	if sumPaise(got) != 100 || got[0] != 34 || got[1] != 33 || got[2] != 33 {
		t.Fatalf("Allocate(100, 1,1,1) = %v, want [34 33 33]", got)
	}
	got, err = Allocate(10, []Paise{1, 2, 3, 4})
	if err != nil || got[0] != 1 || got[1] != 2 || got[2] != 3 || got[3] != 4 {
		t.Fatalf("Allocate(10, 1..4) = %v, %v", got, err)
	}
	// Largest remainder wins, not the lowest index: 5 over 3,3,1 is
	// 2.142.., 2.142.., 0.714.. -> floors 2,2,0, residual 1 to index 2.
	got, err = Allocate(5, []Paise{3, 3, 1})
	if err != nil || got[0] != 2 || got[1] != 2 || got[2] != 1 {
		t.Fatalf("Allocate(5, 3,3,1) = %v, %v", got, err)
	}
	// A zero weight never receives a residual paise.
	got, err = Allocate(7, []Paise{0, 3, 3})
	if err != nil || got[0] != 0 || sumPaise(got) != 7 {
		t.Fatalf("Allocate(7, 0,3,3) = %v, %v", got, err)
	}
	got, err = Allocate(500, []Paise{0, 0, 0})
	if err != nil || got[0] != 500 || sumPaise(got) != 500 {
		t.Fatalf("zero weights dropped money: %v, %v", got, err)
	}
}

func TestAllocate_Refusals(t *testing.T) {
	if _, err := Allocate(-1, []Paise{1}); !errors.Is(err, ErrNegativeAmount) {
		t.Errorf("negative amount: %v", err)
	}
	if _, err := Allocate(1, []Paise{1, -1}); !errors.Is(err, ErrNegativeAmount) {
		t.Errorf("negative weight: %v", err)
	}
	if _, err := Allocate(1, nil); err == nil {
		t.Error("allocating across nothing succeeded")
	}
	if got, err := Allocate(0, nil); err != nil || len(got) != 0 {
		t.Errorf("zero across nothing: %v, %v", got, err)
	}
}
