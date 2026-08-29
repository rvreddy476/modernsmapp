package service

import (
	"math"
	"testing"

	"github.com/atpost/rider-service/internal/geo"
)

// TestHaversine_KnownDistance — Bengaluru → MG Road type short hop.
func TestHaversine_KnownDistance(t *testing.T) {
	// MG Road -> Whitefield ~16km straight-line.
	got := geo.HaversineKM(12.9716, 77.5946, 12.9698, 77.7500)
	want := 16.85
	if math.Abs(got-want) > 0.5 {
		t.Errorf("haversine(MG -> Whitefield) = %.2f km, want ~%.2f km", got, want)
	}
}

func TestHaversine_ZeroDistance(t *testing.T) {
	got := geo.HaversineKM(12.97, 77.59, 12.97, 77.59)
	if got != 0 {
		t.Errorf("same point should be 0 km, got %v", got)
	}
}

func TestValidLatLng(t *testing.T) {
	cases := []struct {
		lat, lng float64
		ok       bool
	}{
		{12.97, 77.59, true},
		{0, 0, false},      // (0,0) is rejected — null-island sentinel
		{-90, 180, true},   // edge of allowed range
		{91, 0, false},     // out of range
		{0, 181, false},    // out of range
	}
	for _, c := range cases {
		if validLatLng(c.lat, c.lng) != c.ok {
			t.Errorf("validLatLng(%v,%v) = %v, want %v", c.lat, c.lng, !c.ok, c.ok)
		}
	}
}

func TestRound2_Stable(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{1.234, 1.23},
		{1.235, 1.24},
		{0.0, 0.0},
		{99.999, 100.0},
	}
	for _, c := range cases {
		got := round2(c.in)
		if got != c.want {
			t.Errorf("round2(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSurgeBasisPointsMath(t *testing.T) {
	cases := []struct {
		surgeMultiplier float64
		wantBps         int64
	}{
		{1.0, 0},
		{1.25, 2500},
		{1.5, 5000},
		{2.0, 10000},
	}
	for _, tc := range cases {
		got := int64(math.Round((tc.surgeMultiplier - 1.0) * 10000))
		if got != tc.wantBps {
			t.Errorf("surge %v -> bps %d, want %d", tc.surgeMultiplier, got, tc.wantBps)
		}
	}
}
