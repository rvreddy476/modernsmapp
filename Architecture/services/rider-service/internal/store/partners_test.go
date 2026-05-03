package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestCreateAndGetPartner_RoundTrip(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	cs, _ := s.ListActiveCities(ctx)
	if len(cs) == 0 {
		t.Skip("no seeded cities — schema not bootstrapped")
	}
	cityID := cs[0].ID
	uid := uuid.New()
	in := CreatePartnerInput{
		UserID:      uid,
		PartnerType: "individual_driver",
		FullName:    "Anil Kumar",
		Phone:       "+919900000001",
		CityID:      &cityID,
	}
	p, err := s.CreatePartner(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.UserID != uid {
		t.Fatalf("user id round-trip failed")
	}
	if p.Status != "draft" || p.KYCStatus != "draft" {
		t.Fatalf("expected status=draft, kyc=draft; got %s / %s", p.Status, p.KYCStatus)
	}
	got, err := s.GetPartner(ctx, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.FullName != "Anil Kumar" {
		t.Fatalf("full name round-trip: %q", got.FullName)
	}
	byUser, err := s.GetPartnerByUserID(ctx, uid)
	if err != nil {
		t.Fatalf("by user: %v", err)
	}
	if byUser.ID != p.ID {
		t.Fatalf("by user mismatch")
	}
}

func TestGetPartner_NotFound(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	_, err := s.GetPartner(context.Background(), uuid.New())
	if !errors.Is(err, ErrPartnerNotFound) {
		t.Fatalf("expected ErrPartnerNotFound; got %v", err)
	}
}

func TestUpdatePartnerProfile_Patch(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, err := s.CreatePartner(ctx, CreatePartnerInput{
		UserID:      uuid.New(),
		PartnerType: "owner_driver",
		FullName:    "Initial Name",
		Phone:       "+919900000002",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	newName := "Updated Name"
	updated, err := s.UpdatePartnerProfile(ctx, p.ID, UpdatePartnerProfileInput{FullName: &newName})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.FullName != "Updated Name" {
		t.Fatalf("name not updated: %q", updated.FullName)
	}
}

func TestUpdatePartnerStatus_Transitions(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, err := s.CreatePartner(ctx, CreatePartnerInput{
		UserID:      uuid.New(),
		PartnerType: "individual_driver",
		FullName:    "Tx Test",
		Phone:       "+919900000003",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.UpdatePartnerStatus(ctx, p.ID, "pending_verification"); err != nil {
		t.Fatalf("transition draft->pending: %v", err)
	}
	got, _ := s.GetPartner(ctx, p.ID)
	if got.Status != "pending_verification" {
		t.Fatalf("status not transitioned: %s", got.Status)
	}
	if err := s.UpdatePartnerStatus(ctx, p.ID, "approved"); err != nil {
		t.Fatalf("transition pending->approved: %v", err)
	}
}

func TestRecordAadhaarVerification_NoRawNumber(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, err := s.CreatePartner(ctx, CreatePartnerInput{
		UserID:      uuid.New(),
		PartnerType: "individual_driver",
		FullName:    "Aadhaar Test",
		Phone:       "+919900000004",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// DPDP audit: only the opaque ref + hash flow through. Raw number is
	// never accepted by the method signature.
	if err := s.RecordAadhaarVerification(ctx, p.ID, "mock-ref-abc", "deadbeef", 1714435200); err != nil {
		t.Fatalf("record: %v", err)
	}
	v, err := s.GetAadhaarVerification(ctx, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if v.DigiLockerRef != "mock-ref-abc" || v.DocTypeHash != "deadbeef" {
		t.Fatalf("round-trip failed: %+v", v)
	}
}
