package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestKYC_NotFoundForFreshUser(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	if _, err := s.GetKYC(context.Background(), uuid.New()); !errors.Is(err, ErrKYCNotFound) {
		t.Fatalf("expected not-found; got %v", err)
	}
}

func TestUpsertAadhaarVerified_StoresRefOnly(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	if err := s.UpsertAadhaarVerified(ctx, uid, "DIGILOCKER-REF-OPAQUE-123"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	rec, err := s.GetKYC(ctx, uid)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if rec.DigiLockerRef == nil || *rec.DigiLockerRef != "DIGILOCKER-REF-OPAQUE-123" {
		t.Fatalf("digilocker ref not stored: %+v", rec.DigiLockerRef)
	}
	if rec.AadhaarStatus == nil || *rec.AadhaarStatus != "verified" {
		t.Fatalf("status not verified")
	}
	if rec.Tier != KYCFull {
		t.Fatalf("expected tier=full")
	}
}

func TestSetPANStatus_StoresMaskedOnly(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	if err := s.SetPANStatus(ctx, uid, "XXXXXX234F", "pending"); err != nil {
		t.Fatalf("set pan: %v", err)
	}
	rec, _ := s.GetKYC(ctx, uid)
	if rec.PANMasked == nil || *rec.PANMasked != "XXXXXX234F" {
		t.Fatalf("pan masked not stored: %+v", rec.PANMasked)
	}
	// Critical: anything that looks like an actual PAN (5-letter prefix)
	// would fail this check.
	if rec.PANMasked != nil && len(*rec.PANMasked) >= 1 && (*rec.PANMasked)[0] != 'X' {
		t.Fatalf("expected masked prefix, got %s", *rec.PANMasked)
	}
}

func TestMarkAadhaarPending(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	if err := s.MarkAadhaarPending(ctx, uid); err != nil {
		t.Fatalf("mark: %v", err)
	}
	rec, _ := s.GetKYC(ctx, uid)
	if rec.AadhaarStatus == nil || *rec.AadhaarStatus != "pending" {
		t.Fatalf("expected pending; got %+v", rec.AadhaarStatus)
	}
}

func TestSubmittedRecently(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	if err := s.MarkAadhaarPending(ctx, uid); err != nil {
		t.Fatalf("mark: %v", err)
	}
	recent, err := s.SubmittedRecently(ctx, uid, time.Hour)
	if err != nil {
		t.Fatalf("recently: %v", err)
	}
	if !recent {
		t.Fatalf("expected recent submission")
	}
	old, _ := s.SubmittedRecently(ctx, uid, time.Nanosecond)
	if old {
		t.Fatalf("nanosecond window should not be recent")
	}
}

func TestCurrentTier_DefaultsToMinimal(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	tier, err := s.CurrentTier(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("current tier: %v", err)
	}
	if tier != KYCMinimal {
		t.Fatalf("expected minimal default; got %q", tier)
	}
}
