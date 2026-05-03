package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestUpsertPartnerLocation_RoundTrip(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	cs, _ := s.ListActiveCities(ctx)
	if len(cs) == 0 {
		t.Skip("no seeded cities")
	}
	p, err := s.CreatePartner(ctx, CreatePartnerInput{
		UserID: uuid.New(), PartnerType: "individual_driver",
		FullName: "Loc Test", Phone: "+919801000000", CityID: &cs[0].ID,
	})
	if err != nil {
		t.Fatalf("create partner: %v", err)
	}
	if err := s.UpsertPartnerLocation(ctx, UpsertPartnerLocationInput{
		PartnerID: p.ID, LastLat: 12.97, LastLng: 77.59, LastGeohash: "tdr1uy",
		IsOnline: true,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	loc, err := s.GetPartnerLocation(ctx, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if loc.LastGeohash != "tdr1uy" || !loc.IsOnline {
		t.Fatalf("round-trip failed: %+v", loc)
	}
	// Idempotent re-call must update, not insert.
	if err := s.UpsertPartnerLocation(ctx, UpsertPartnerLocationInput{
		PartnerID: p.ID, LastLat: 12.99, LastLng: 77.61, LastGeohash: "tdr1v0",
		IsOnline: true,
	}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	loc, _ = s.GetPartnerLocation(ctx, p.ID)
	if loc.LastGeohash != "tdr1v0" {
		t.Fatalf("re-upsert didn't update geohash: %s", loc.LastGeohash)
	}
}

func TestSetPartnerOnlineFlag_FlipsBoth(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, err := s.CreatePartner(ctx, CreatePartnerInput{
		UserID: uuid.New(), PartnerType: "individual_driver",
		FullName: "Flip Test", Phone: "+919801000001",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.SetPartnerOnlineFlag(ctx, p.ID, true); err != nil {
		t.Fatalf("set online: %v", err)
	}
	loc, err := s.GetPartnerLocation(ctx, p.ID)
	if err != nil {
		t.Fatalf("get loc: %v", err)
	}
	if !loc.IsOnline {
		t.Fatalf("location row should be online")
	}
	got, _ := s.GetPartner(ctx, p.ID)
	if !got.IsOnline {
		t.Fatalf("partner row should be online too")
	}
	// Offline.
	if err := s.SetPartnerOnlineFlag(ctx, p.ID, false); err != nil {
		t.Fatalf("set offline: %v", err)
	}
	got, _ = s.GetPartner(ctx, p.ID)
	if got.IsOnline {
		t.Fatalf("partner row should be offline after flip")
	}
}

func TestGetPartnerLocation_NotFound(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	_, err := s.GetPartnerLocation(context.Background(), uuid.New())
	if !errors.Is(err, ErrPartnerLocationNotFound) {
		t.Fatalf("expected not-found; got %v", err)
	}
}

func TestFindOnlinePartnersByGeohash(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, _ := s.CreatePartner(ctx, CreatePartnerInput{
		UserID: uuid.New(), PartnerType: "individual_driver",
		FullName: "Geo Test", Phone: "+919801000002",
	})
	if err := s.UpsertPartnerLocation(ctx, UpsertPartnerLocationInput{
		PartnerID: p.ID, LastLat: 12.97, LastLng: 77.59, LastGeohash: "tdr1uy",
		IsOnline: true,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	out, err := s.FindOnlinePartnersByGeohash(ctx, []string{"tdr1uy", "tdr1v0"}, 10)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 online partner; got %d", len(out))
	}
	if out[0].PartnerID != p.ID {
		t.Fatalf("partner mismatch")
	}
}
