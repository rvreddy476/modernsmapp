package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/orderstate"
	"github.com/atpost/food-service/internal/routing"
)

// Unit tests for B6 placement ETA maths; no database.

// deliveryMinutesForRoute over the default haversine keeps lane 0c's
// estimateDeliveryMinutes table exactly.
func TestDeliveryMinutesForRoute_KeepsTheLane0cNumbers(t *testing.T) {
	cases := []struct {
		prep      int
		km, speed float64
		want      int
	}{
		{20, 2.9, 20, 29},
		{15, 0, 20, 15},
		{10, 10, 20, 40},
		{10, 10, 0, 40}, // speed falls back to 20 km/h
		{25, 0.1, 20, 26},
	}
	from := routing.LatLng{Lat: 12.9, Lng: 77.6}
	for _, c := range cases {
		to := routing.LatLng{Lat: 12.9 + c.km/kmPerDegLat, Lng: 77.6}
		r, err := routing.Haversine{SpeedKmh: c.speed}.Route(context.Background(), from, to)
		if err != nil {
			t.Fatal(err)
		}
		if got := deliveryMinutesForRoute(c.prep, r.Duration); got != c.want {
			t.Errorf("prep %d, %v km at %v km/h: %d min (ride %v), want %d", c.prep, c.km, c.speed, got, r.Duration, c.want)
		}
	}
	// Float noise just above a whole minute does not add one.
	if got := deliveryMinutesForRoute(20, 9*time.Minute+300*time.Microsecond); got != 29 {
		t.Fatalf("9m0.0003s ride = %d, want 29", got)
	}
	if got := placementETASeconds(20, 522*time.Second); got != 1722 {
		t.Fatalf("placement eta seconds = %v, want 1722", got)
	}
}

func TestPlacementLeg_UsesThePricedRideOnlyForTheSamePoints(t *testing.T) {
	s := New(nil)
	rest, cust := routing.LatLng{Lat: 12.9, Lng: 77.6}, routing.LatLng{Lat: 12.93, Lng: 77.6}
	google := routing.Route{DistanceMeters: 4100, Duration: 15 * time.Minute, Source: routing.SourceGoogle}
	haversine, _ := s.ordering.Haversine().Route(context.Background(), rest, cust)

	if got := s.placementLeg(&RouteEstimate{From: rest, To: cust, Route: google}, rest, cust); got != google {
		t.Fatalf("same points: %+v", got)
	}
	for name, priced := range map[string]*RouteEstimate{
		"none":             nil,
		"address changed":  {From: rest, To: routing.LatLng{Lat: 12.95, Lng: 77.6}, Route: google},
		"cart changed":     {From: routing.LatLng{Lat: 12.8, Lng: 77.6}, To: cust, Route: google},
		"unlabelled route": {From: rest, To: cust, Route: routing.Route{Duration: time.Minute}},
	} {
		if got := s.placementLeg(priced, rest, cust); got != haversine {
			t.Fatalf("%s: %+v, want the haversine ride %+v", name, got, haversine)
		}
	}
}

func TestETAVisibleOnlyWhileTheOrderCanArrive(t *testing.T) {
	for status, want := range map[string]bool{
		orderstate.PaymentPending: true, orderstate.Confirmed: true, orderstate.DeliveryAssigned: true,
		orderstate.PickedUp: true, orderstate.OutForDelivery: true,
		orderstate.Delivered: false, orderstate.CancelledByAdmin: false, orderstate.RestaurantRejected: false,
		orderstate.Refunded: false, orderstate.PaymentFailed: false,
	} {
		if ETAVisible(status) != want {
			t.Errorf("ETAVisible(%s) = %v", status, !want)
		}
	}
}

func TestLatLngFromSnapshot(t *testing.T) {
	if p := latLngFromSnapshot([]byte(`{"latitude":12.97,"longitude":77.59,"city":"Bengaluru"}`)); p == nil || *p != (routing.LatLng{Lat: 12.97, Lng: 77.59}) {
		t.Fatalf("snapshot = %v", p)
	}
	for _, raw := range []string{`{}`, `{"latitude":0,"longitude":0}`, `{"latitude":"12"}`, ``} {
		if p := latLngFromSnapshot([]byte(raw)); p != nil {
			t.Fatalf("%q = %v, want nil", raw, p)
		}
	}
}
