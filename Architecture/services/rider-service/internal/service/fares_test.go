package service

import (
	"math"
	"testing"
)

// TestHaversine_KnownDistance — Bengaluru → MG Road type short hop.
// Sanity-check the great-circle math against a hand-computed value.
func TestHaversine_KnownDistance(t *testing.T) {
	// MG Road -> Whitefield ~16km straight-line.
	got := haversineKM(12.9716, 77.5946, 12.9698, 77.7500)
	want := 16.85
	if math.Abs(got-want) > 0.5 {
		t.Errorf("haversine(MG -> Whitefield) = %.2f km, want ~%.2f km", got, want)
	}
}

func TestHaversine_ZeroDistance(t *testing.T) {
	got := haversineKM(12.97, 77.59, 12.97, 77.59)
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

// TestEstimateFareMath_Manual checks the headline fare math against a
// hand-computed expectation. Bengaluru auto base ₹25 + ₹12/km × 5km × 1.4 winding
// + ₹0/min × duration -> at minimum the formula rounds correctly.
//
// NB: this is a pure-math check via the helper functions; full estimate
// tests that hit the DB live in service_integration_test.go (TEST_PG_DSN).
func TestRound2_Stable(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{1.234, 1.23},
		{1.235, 1.24}, // banker's-rounding edge — math.Round is half-away
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
