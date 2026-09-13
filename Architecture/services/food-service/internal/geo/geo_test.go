package geo

import (
	"math"
	"testing"
)

func TestHaversineKM_KnownValue(t *testing.T) {
	// MG Road -> Whitefield-ish, same values as rider-service's test.
	got := HaversineKM(12.9716, 77.5946, 12.9698, 77.7500)
	if math.Abs(got-16.85) > 0.2 {
		t.Fatalf("got %.3f km, want ~16.85", got)
	}
	if HaversineKM(12.97, 77.59, 12.97, 77.59) != 0 {
		t.Fatal("zero distance not zero")
	}
}

func TestBoundingBoxContainsRadius(t *testing.T) {
	lat, lng, r := 12.9716, 77.5946, 5.0
	minLat, maxLat, minLng, maxLng := BoundingBox(lat, lng, r)
	kmPerDeg := earthRadiusKM * math.Pi / 180
	in := r * 0.999 // just inside the radius
	// Points just inside the radius in each cardinal direction must lie inside the box.
	for _, p := range [][2]float64{
		{lat + in/kmPerDeg, lng}, {lat - in/kmPerDeg, lng},
		{lat, lng + in/(kmPerDeg*math.Cos(lat*math.Pi/180))}, {lat, lng - in/(kmPerDeg*math.Cos(lat*math.Pi/180))},
	} {
		if HaversineKM(lat, lng, p[0], p[1]) > r {
			t.Fatalf("test point %v is not within %v km", p, r)
		}
		if p[0] < minLat-1e-6 || p[0] > maxLat+1e-6 || p[1] < minLng-1e-6 || p[1] > maxLng+1e-6 {
			t.Fatalf("point %v outside box [%f,%f]x[%f,%f]", p, minLat, maxLat, minLng, maxLng)
		}
	}
}
