package geo

import (
	"math"
	"testing"
)

func TestEncode_KnownBengaluruPoint(t *testing.T) {
	// MG Road, Bengaluru: 12.9716, 77.5946. Precision-6 geohash should be
	// stable across runs.
	gh := Encode(12.9716, 77.5946, 6)
	if len(gh) != 6 {
		t.Fatalf("expected length 6; got %d (%q)", len(gh), gh)
	}
	// Idempotent: same input -> same output.
	gh2 := Encode(12.9716, 77.5946, 6)
	if gh != gh2 {
		t.Fatalf("encode is not deterministic")
	}
}

func TestEncode_PrecisionBounds(t *testing.T) {
	// Precision <=0 -> default 6; precision >12 -> capped at 12.
	a := Encode(12.97, 77.59, 0)
	if len(a) != 6 {
		t.Errorf("zero precision should default to 6; got %d", len(a))
	}
	b := Encode(12.97, 77.59, 100)
	if len(b) != 12 {
		t.Errorf(">12 precision should cap at 12; got %d", len(b))
	}
}

func TestEncode_NearbyPointsShareLeadingChars(t *testing.T) {
	a := Encode(12.9716, 77.5946, 6)
	// 100m east — should share the first 4-5 characters at precision 6.
	b := Encode(12.9716, 77.5950, 6)
	if a[:4] != b[:4] {
		t.Errorf("100m-apart geohashes diverged early: %q vs %q", a, b)
	}
}

func TestHaversineKM_KnownValue(t *testing.T) {
	// MG Road -> Whitefield ~16km straight-line per fares_test.go.
	got := HaversineKM(12.9716, 77.5946, 12.9698, 77.7500)
	if math.Abs(got-16.85) > 0.5 {
		t.Errorf("haversine = %.2f; want ~16.85", got)
	}
}

func TestHaversineKM_ZeroDistance(t *testing.T) {
	if got := HaversineKM(12.97, 77.59, 12.97, 77.59); got != 0 {
		t.Errorf("identical points should be 0; got %v", got)
	}
}

func TestNeighbors_IncludesSelf(t *testing.T) {
	gh := Encode(12.9716, 77.5946, 6)
	ns := Neighbors(gh)
	found := false
	for _, n := range ns {
		if n == gh {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("neighbors should include self; got %v", ns)
	}
	// Reasonable cardinality: between 4 (corner cell) and 9 (interior).
	if len(ns) < 4 || len(ns) > 9 {
		t.Errorf("neighbors count out of range: %d (%v)", len(ns), ns)
	}
}

func TestNeighbors_EmptyInput(t *testing.T) {
	if got := Neighbors(""); got != nil {
		t.Errorf("empty input should yield nil; got %v", got)
	}
}
