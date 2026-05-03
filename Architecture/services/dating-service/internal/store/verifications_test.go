// Verifications store tests.
//
// Skipped unless TEST_PG_DSN is exported.
//
// DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8
// These tests deliberately do NOT pass an Aadhaar number anywhere; the
// store API does not accept one. We verify only that:
//   - Selfie attempt rows persist + status transitions are enforced.
//   - Aadhaar verification rows store digilocker_ref + doc_type_hash.
//   - Trust tier promotion is monotonic.
package store

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func verificationTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping verifications store tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return New(pool), func() { pool.Close() }
}

func TestRecordSelfieAttempt_StatusValidation(t *testing.T) {
	s, cleanup := verificationTestStore(t)
	defer cleanup()
	uid := uuid.New()
	ensureProfileForTest(t, s, uid)
	if err := s.RecordSelfieAttempt(context.Background(), uid, 0.8, "passed"); err != nil {
		t.Fatalf("happy path: %v", err)
	}
	if err := s.RecordSelfieAttempt(context.Background(), uid, 0.5, "bogus"); err == nil {
		t.Fatalf("expected validation error for bogus status")
	}
}

func TestRecordAadhaarVerification_StoresOpaqueRefOnly(t *testing.T) {
	s, cleanup := verificationTestStore(t)
	defer cleanup()
	uid := uuid.New()
	ensureProfileForTest(t, s, uid)
	if err := s.RecordAadhaarVerification(context.Background(), uid, "ref-abc-123", "deadbeefhash"); err != nil {
		t.Fatalf("record: %v", err)
	}
	v, err := s.GetVerification(context.Background(), uid)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if v.DigilockerRef == nil || *v.DigilockerRef != "ref-abc-123" {
		t.Fatalf("digilocker_ref not stored: %+v", v.DigilockerRef)
	}
	if v.DocTypeHash == nil || *v.DocTypeHash != "deadbeefhash" {
		t.Fatalf("doc_type_hash not stored: %+v", v.DocTypeHash)
	}
	if v.AadhaarStatus == nil || *v.AadhaarStatus != "verified" {
		t.Fatalf("aadhaar_status not 'verified': %+v", v.AadhaarStatus)
	}
}

func TestRecordAadhaarVerification_RequiresArgs(t *testing.T) {
	s, cleanup := verificationTestStore(t)
	defer cleanup()
	uid := uuid.New()
	ensureProfileForTest(t, s, uid)
	if err := s.RecordAadhaarVerification(context.Background(), uid, "", "h"); err == nil {
		t.Fatalf("expected error for empty digilocker_ref")
	}
	if err := s.RecordAadhaarVerification(context.Background(), uid, "r", ""); err == nil {
		t.Fatalf("expected error for empty doc_type_hash")
	}
}

func TestUpdateTrustTier_MonotonicPromotion(t *testing.T) {
	s, cleanup := verificationTestStore(t)
	defer cleanup()
	uid := uuid.New()
	ensureProfileForTest(t, s, uid)

	// phone -> selfie OK.
	if err := s.UpdateTrustTier(context.Background(), uid, "selfie"); err != nil {
		t.Fatalf("phone->selfie: %v", err)
	}
	// selfie -> aadhaar OK.
	if err := s.UpdateTrustTier(context.Background(), uid, "aadhaar"); err != nil {
		t.Fatalf("selfie->aadhaar: %v", err)
	}
	// aadhaar -> selfie should be a NO-OP (no demotion).
	if err := s.UpdateTrustTier(context.Background(), uid, "selfie"); err != nil {
		t.Fatalf("demotion attempt: %v", err)
	}
	p, err := s.GetProfile(context.Background(), uid)
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	if p.TrustTier != "aadhaar" {
		t.Fatalf("expected tier to remain 'aadhaar', got %q", p.TrustTier)
	}
}

func TestUpdateTrustTier_RejectsBogus(t *testing.T) {
	s, cleanup := verificationTestStore(t)
	defer cleanup()
	uid := uuid.New()
	ensureProfileForTest(t, s, uid)
	if err := s.UpdateTrustTier(context.Background(), uid, "premium"); err == nil {
		t.Fatalf("expected error for bogus tier")
	}
}
