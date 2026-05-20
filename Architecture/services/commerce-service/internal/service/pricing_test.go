package service

import (
	"testing"

	"github.com/atpost/commerce-service/internal/store/postgres"
)

func ptr(v int) *int { return &v }

func TestResolveTieredPrice_FallsBackWithoutTiers(t *testing.T) {
	got := resolveTieredPrice(nil, 100.0, 5)
	if got != 100.0 {
		t.Errorf("no tiers should fall back to selling_price; got %v", got)
	}
}

func TestResolveTieredPrice_PicksHighestApplicableBand(t *testing.T) {
	// Ladder: 1-9 @ 100, 10-49 @ 90, 50+ @ 80.
	tiers := []*postgres.PriceTier{
		{MinQty: 1, MaxQty: ptr(9), Price: 100},
		{MinQty: 10, MaxQty: ptr(49), Price: 90},
		{MinQty: 50, MaxQty: nil, Price: 80},
	}
	cases := map[int]float64{
		1:   100, // boundary low
		9:   100, // boundary high of first
		10:  90,  // boundary low of second
		49:  90,  // boundary high of second
		50:  80,  // unbounded band
		500: 80,
	}
	for qty, want := range cases {
		got := resolveTieredPrice(tiers, 200.0, qty)
		if got != want {
			t.Errorf("qty=%d: got %v want %v (fallback ignored when band matches)", qty, got, want)
		}
	}
}

func TestResolveTieredPrice_QuantityBelowFirstBandFallsBack(t *testing.T) {
	// Ladder starts at min_qty=5 — buying 1 should fall back since no
	// band covers qty=1..4.
	tiers := []*postgres.PriceTier{
		{MinQty: 5, MaxQty: ptr(20), Price: 90},
	}
	got := resolveTieredPrice(tiers, 100.0, 3)
	if got != 100.0 {
		t.Errorf("qty below first band should fall back; got %v want 100.0", got)
	}
}

func TestValidateTierLadder_RejectsOverlap(t *testing.T) {
	tiers := []PriceTierInput{
		{MinQty: 1, MaxQty: ptr(10), Price: 100},
		{MinQty: 8, MaxQty: ptr(20), Price: 90}, // overlaps 8-10 with first
	}
	if err := validateTierLadder(tiers); err == nil {
		t.Error("expected overlap rejection, got nil")
	}
}

func TestValidateTierLadder_RejectsUnboundedBlockingLaterBand(t *testing.T) {
	tiers := []PriceTierInput{
		{MinQty: 1, MaxQty: nil, Price: 100}, // unbounded
		{MinQty: 50, MaxQty: nil, Price: 80}, // blocked
	}
	if err := validateTierLadder(tiers); err == nil {
		t.Error("expected unbounded-blocks-later rejection, got nil")
	}
}

func TestValidateTierLadder_AcceptsAdjacentBands(t *testing.T) {
	tiers := []PriceTierInput{
		{MinQty: 1, MaxQty: ptr(9), Price: 100},
		{MinQty: 10, MaxQty: ptr(49), Price: 90},
		{MinQty: 50, MaxQty: nil, Price: 80},
	}
	if err := validateTierLadder(tiers); err != nil {
		t.Errorf("adjacent ladder rejected: %v", err)
	}
}

func TestValidateTierLadder_RejectsInvalidBand(t *testing.T) {
	cases := [][]PriceTierInput{
		{{MinQty: 0, MaxQty: ptr(10), Price: 90}},        // min_qty < 1
		{{MinQty: 5, MaxQty: ptr(3), Price: 90}},         // max < min
		{{MinQty: 1, MaxQty: ptr(10), Price: 0}},         // price 0
		{{MinQty: 1, MaxQty: ptr(10), Price: -5}},        // price negative
	}
	for _, tiers := range cases {
		if err := validateTierLadder(tiers); err == nil {
			t.Errorf("expected reject for %+v", tiers)
		}
	}
}
