package service

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestGoOnline_RejectsUnapprovedPartner(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	uid := uuid.New()
	_, err := svc.CreatePartnerProfile(context.Background(), uid, CreatePartnerRequest{
		PartnerType: "individual_driver",
		FullName:    "Online Test", Phone: "+919812000000",
	})
	if err != nil {
		t.Fatalf("create partner: %v", err)
	}
	err = svc.GoOnline(context.Background(), uid)
	if err == nil || !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("expected partner-not-approved; got %v", err)
	}
}

func TestGoOnline_HappyPathFlipsFlag(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	p, _ := makeApprovedPartnerWithVehicle(t, svc)
	// makeApprovedPartnerWithVehicle leaves partner online — set offline first
	// so GoOnline has work to do.
	if err := svc.Store().SetPartnerOnlineFlag(context.Background(), p.ID, false); err != nil {
		t.Fatalf("set offline: %v", err)
	}
	if err := svc.GoOnline(context.Background(), p.UserID); err != nil {
		t.Fatalf("go online: %v", err)
	}
	got, _ := svc.Store().GetPartner(context.Background(), p.ID)
	if !got.IsOnline {
		t.Fatalf("partner should be online after GoOnline")
	}
}

func TestGoOffline_FlipsFlag(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	p, _ := makeApprovedPartnerWithVehicle(t, svc)
	if err := svc.GoOffline(context.Background(), p.UserID); err != nil {
		t.Fatalf("go offline: %v", err)
	}
	got, _ := svc.Store().GetPartner(context.Background(), p.ID)
	if got.IsOnline {
		t.Fatalf("partner should be offline")
	}
}

func TestUpdateLocation_PersistsRow(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	p, _ := makeApprovedPartnerWithVehicle(t, svc)
	if err := svc.UpdateLocation(context.Background(), p.UserID, UpdateLocationRequest{
		Lat: 12.95, Lng: 77.60,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	loc, err := svc.Store().GetPartnerLocation(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("get loc: %v", err)
	}
	if loc.LastLat != 12.95 || loc.LastLng != 77.60 {
		t.Fatalf("location not persisted: %+v", loc)
	}
	if loc.LastGeohash == "" {
		t.Fatalf("geohash should be set")
	}
}

func TestUpdateLocation_RejectsInvalidLatLng(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	p, _ := makeApprovedPartnerWithVehicle(t, svc)
	err := svc.UpdateLocation(context.Background(), p.UserID, UpdateLocationRequest{
		Lat: 200, Lng: 200,
	})
	if err == nil || !strings.Contains(err.Error(), "lat/lng") {
		t.Fatalf("expected lat/lng validation; got %v", err)
	}
}
