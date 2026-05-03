package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestCreateRide_RoundTrip(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	cs, _ := s.ListActiveCities(ctx)
	if len(cs) == 0 {
		t.Skip("no seeded cities")
	}
	method := "cash"
	r, err := s.CreateRide(ctx, CreateRideInput{
		CustomerUserID: uuid.New(),
		CityID:         &cs[0].ID,
		VehicleType:    "auto",
		PickupAddress:  "MG Road, Bengaluru",
		PickupLat:      12.9716,
		PickupLng:      77.5946,
		DropAddress:    "Indiranagar",
		DropLat:        12.9784,
		DropLng:        77.6408,
		PaymentMethod:  &method,
	})
	if err != nil {
		t.Fatalf("create ride: %v", err)
	}
	if r.Status != "requested" {
		t.Fatalf("status: %s", r.Status)
	}
	if r.PickupLat == 0 || r.PickupLng == 0 {
		t.Fatalf("pickup coords lost in round-trip: %+v", r)
	}
	got, err := s.GetRide(ctx, r.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != r.ID {
		t.Fatalf("get returned different ride")
	}
}

func TestGetRide_NotFound(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	_, err := s.GetRide(context.Background(), uuid.New())
	if !errors.Is(err, ErrRideNotFound) {
		t.Fatalf("expected ErrRideNotFound; got %v", err)
	}
}

func TestListRidesByCustomer(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	cs, _ := s.ListActiveCities(ctx)
	if len(cs) == 0 {
		t.Skip("no seeded cities")
	}
	cust := uuid.New()
	for i := 0; i < 3; i++ {
		_, err := s.CreateRide(ctx, CreateRideInput{
			CustomerUserID: cust,
			CityID:         &cs[0].ID,
			VehicleType:    "auto",
			PickupAddress:  "P",
			PickupLat:      12.97,
			PickupLng:      77.59,
			DropAddress:    "D",
			DropLat:        12.93,
			DropLng:        77.62,
		})
		if err != nil {
			t.Fatalf("create ride %d: %v", i, err)
		}
	}
	rs, err := s.ListRidesByCustomer(ctx, cust, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rs) != 3 {
		t.Fatalf("expected 3 rides; got %d", len(rs))
	}
}
