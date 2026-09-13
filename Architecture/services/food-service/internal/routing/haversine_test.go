package routing

import (
	"context"
	"math"
	"testing"
	"time"
)

const kmPerDegLat = 6371.0 * math.Pi / 180.0

// The defaults keep lane 0c's estimate: 2.9 km at 20 km/h is 8.7 min, 9 when
// rounded up (the PlaceOrder integration test pins 20 + 9 = 29).
func TestHaversine_DefaultsKeepTheLane0cEstimate(t *testing.T) {
	to := LatLng{Lat: 12.9 + 2.9/kmPerDegLat, Lng: 77.6}
	r, err := Haversine{}.Route(context.Background(), LatLng{Lat: 12.9, Lng: 77.6}, to)
	if err != nil {
		t.Fatal(err)
	}
	if r.Source != SourceHaversine || r.DistanceMeters != 2900 {
		t.Fatalf("route = %+v", r)
	}
	if got := math.Ceil(r.Duration.Minutes()); got != 9 {
		t.Fatalf("ride = %v (%v min rounded up), want 9", r.Duration, got)
	}
}

func TestHaversine_WindingStretchesDistanceAndTime(t *testing.T) {
	from, to := LatLng{Lat: 12.9, Lng: 77.6}, LatLng{Lat: 12.9 + 10/kmPerDegLat, Lng: 77.6}
	plain, _ := Haversine{SpeedKmh: 20, WindingFactor: 1}.Route(context.Background(), from, to)
	wound, _ := Haversine{SpeedKmh: 20, WindingFactor: 1.35}.Route(context.Background(), from, to)
	if plain.Duration != 30*time.Minute || wound.DistanceMeters != 13500 {
		t.Fatalf("plain %+v wound %+v", plain, wound)
	}
	if d := wound.Duration - time.Duration(1.35*float64(30*time.Minute)); d > time.Second || d < -time.Second {
		t.Fatalf("wound ride = %v", wound.Duration)
	}
	// 1.35 at 27 km/h is the same ride time as 1.0 at 20 km/h.
	same, _ := Haversine{SpeedKmh: 27, WindingFactor: 1.35}.Route(context.Background(), from, to)
	if d := same.Duration - plain.Duration; d > time.Second || d < -time.Second {
		t.Fatalf("calibrated ride = %v, want %v", same.Duration, plain.Duration)
	}
	// A factor below 1 would make the road shorter than the straight line.
	short, _ := Haversine{SpeedKmh: 20, WindingFactor: 0.5}.Route(context.Background(), from, to)
	if short != plain {
		t.Fatalf("winding 0.5 = %+v, want the default %+v", short, plain)
	}
}

func TestHaversine_RefusesOutOfRangeCoordinates(t *testing.T) {
	if _, err := (Haversine{}).Route(context.Background(), LatLng{Lat: math.NaN()}, blr); ClassOf(err) != FailureRequest {
		t.Fatalf("NaN: %v", err)
	}
}

func TestConfigFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	cfg, err := ConfigFromEnv(env(nil))
	if err != nil || cfg.GoogleKey != "" || cfg.Timeout != 2*time.Second {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
	cfg, err = ConfigFromEnv(env(map[string]string{"GOOGLE_MAPS_SERVER_KEY": testKey, "FOOD_ROUTING_TIMEOUT_MS": "750"}))
	if err != nil || cfg.GoogleKey != testKey || cfg.Timeout != 750*time.Millisecond {
		t.Fatalf("set: %+v %v", cfg, err)
	}
	for _, bad := range []string{"0", "-1", "2s", "10001"} {
		if _, err := ConfigFromEnv(env(map[string]string{"FOOD_ROUTING_TIMEOUT_MS": bad})); err == nil {
			t.Fatalf("FOOD_ROUTING_TIMEOUT_MS=%s accepted", bad)
		}
	}
}
