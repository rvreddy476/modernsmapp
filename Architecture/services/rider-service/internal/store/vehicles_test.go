package store

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCreateVehicle_RoundTrip(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, err := s.CreatePartner(ctx, CreatePartnerInput{
		UserID:      uuid.New(),
		PartnerType: "owner_driver",
		FullName:    "Veh Test",
		Phone:       "+919900002000",
	})
	if err != nil {
		t.Fatalf("partner: %v", err)
	}
	v, err := s.CreateVehicle(ctx, CreateVehicleInput{
		PartnerID:          p.ID,
		VehicleType:        "auto",
		RegistrationNumber: "KA01AB1234",
	})
	if err != nil {
		t.Fatalf("vehicle: %v", err)
	}
	if v.Status != "pending" {
		t.Fatalf("status: %s", v.Status)
	}
	got, err := s.GetVehicle(ctx, v.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RegistrationNumber != "KA01AB1234" {
		t.Fatalf("reg num round-trip: %s", got.RegistrationNumber)
	}
}

func TestCreateVehicle_DuplicateRegistrationFails(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, _ := s.CreatePartner(ctx, CreatePartnerInput{
		UserID:      uuid.New(),
		PartnerType: "owner_driver",
		FullName:    "Dup Test 1",
		Phone:       "+919900002001",
	})
	_, err := s.CreateVehicle(ctx, CreateVehicleInput{
		PartnerID:          p.ID,
		VehicleType:        "auto",
		RegistrationNumber: "KA02DUP9999",
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	p2, _ := s.CreatePartner(ctx, CreatePartnerInput{
		UserID:      uuid.New(),
		PartnerType: "owner_driver",
		FullName:    "Dup Test 2",
		Phone:       "+919900002002",
	})
	_, err = s.CreateVehicle(ctx, CreateVehicleInput{
		PartnerID:          p2.ID,
		VehicleType:        "auto",
		RegistrationNumber: "KA02DUP9999",
	})
	if err == nil {
		t.Fatalf("expected unique-violation on duplicate registration")
	}
	if !strings.Contains(err.Error(), "ux_rider_vehicle_registration") && !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected unique-violation error; got %v", err)
	}
}

func TestListVehiclesByPartner(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, _ := s.CreatePartner(ctx, CreatePartnerInput{
		UserID:      uuid.New(),
		PartnerType: "owner_driver",
		FullName:    "List Test",
		Phone:       "+919900002100",
	})
	for _, reg := range []string{"KA03LST1", "KA03LST2"} {
		_, err := s.CreateVehicle(ctx, CreateVehicleInput{
			PartnerID:          p.ID,
			VehicleType:        "auto",
			RegistrationNumber: reg,
		})
		if err != nil {
			t.Fatalf("create %s: %v", reg, err)
		}
	}
	vs, err := s.ListVehiclesByPartner(ctx, p.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(vs) != 2 {
		t.Fatalf("expected 2 vehicles; got %d", len(vs))
	}
}
