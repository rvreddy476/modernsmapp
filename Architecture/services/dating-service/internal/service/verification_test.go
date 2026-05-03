// Verification service tests.
//
// DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8
// We confirm that:
//   - The service never logs or returns an Aadhaar number (we don't even
//     accept one anywhere — there is no parameter for it).
//   - Mock DigiLocker happy path persists the assertion + bumps trust.
//   - Expired/invalid PKCE state is rejected.
//   - Selfie pass/fail thresholds work and trust tier never demotes.
package service

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/atpost/dating-service/internal/digilocker"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// fakeMediaClient lets the selfie tests inject a known embedding.
type fakeMediaClient struct {
	stored []float64
	err    error
}

func (f *fakeMediaClient) GetEmbedding(_ context.Context, _ uuid.UUID) ([]float64, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.stored, nil
}

func newVerificationSvc(t *testing.T) (*Service, *store.Store, *redis.Client, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping verification service tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	st := store.New(pool)
	// Use a redis client only when REDIS_ADDR is set; otherwise tests that
	// require Redis will skip.
	var rdb *redis.Client
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		rdb = redis.NewClient(&redis.Options{Addr: addr})
	}
	svc := New(st, rdb)
	return svc, st, rdb, func() { pool.Close() }
}

func TestStartAadhaarFlow_GeneratesAuthorizeURL(t *testing.T) {
	svc, st, rdb, cleanup := newVerificationSvc(t)
	defer cleanup()
	if rdb == nil {
		t.Skip("REDIS_ADDR not set; skipping aadhaar start test")
	}
	uid := uuid.New()
	seedProfile(t, st, uid)
	out, err := svc.StartAadhaarFlow(context.Background(), uid)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if out.State == "" {
		t.Fatalf("expected non-empty state")
	}
	if !strings.Contains(out.DigiLockerAuthorizeURL, "state="+out.State) {
		t.Fatalf("authorize URL missing state param: %s", out.DigiLockerAuthorizeURL)
	}
}

func TestCompleteAadhaarFlow_HappyPath_MockClient(t *testing.T) {
	svc, st, rdb, cleanup := newVerificationSvc(t)
	defer cleanup()
	if rdb == nil {
		t.Skip("REDIS_ADDR not set; skipping aadhaar callback test")
	}
	uid := uuid.New()
	seedProfile(t, st, uid)
	svc.SetDigiLockerClient(digilocker.NewMockClient())
	start, err := svc.StartAadhaarFlow(context.Background(), uid)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	res, err := svc.CompleteAadhaarFlow(context.Background(), uid, "abc", start.State)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !res.Verified {
		t.Fatalf("expected verified=true")
	}
	if res.TrustTier != "aadhaar" {
		t.Fatalf("expected trust tier aadhaar, got %s", res.TrustTier)
	}
	v, err := st.GetVerification(context.Background(), uid)
	if err != nil {
		t.Fatalf("get verification: %v", err)
	}
	if v.DigilockerRef == nil || !strings.HasPrefix(*v.DigilockerRef, "mock-ref-") {
		t.Fatalf("digilocker ref not persisted: %+v", v.DigilockerRef)
	}
	// DPDP smoke test: nothing in the assertion should look like a 12-digit
	// Aadhaar. Mock returns "mock-ref-abc" and a doc-type hash, never a number.
	if v.DigilockerRef != nil && hasTwelveDigitRun(*v.DigilockerRef) {
		t.Fatalf("digilocker_ref looks like an Aadhaar number — DPDP violation: %q", *v.DigilockerRef)
	}
}

func TestCompleteAadhaarFlow_RejectsExpiredState(t *testing.T) {
	svc, st, rdb, cleanup := newVerificationSvc(t)
	defer cleanup()
	if rdb == nil {
		t.Skip("REDIS_ADDR not set; skipping aadhaar expired-state test")
	}
	uid := uuid.New()
	seedProfile(t, st, uid)
	svc.SetDigiLockerClient(digilocker.NewMockClient())
	// Don't call Start — so no state is in Redis.
	_, err := svc.CompleteAadhaarFlow(context.Background(), uid, "abc", "missing-state")
	if err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("expected forbidden, got %v", err)
	}
}

func TestCompleteSelfieFlow_PassAndFailThresholds(t *testing.T) {
	svc, st, _, cleanup := newVerificationSvc(t)
	defer cleanup()
	uid := uuid.New()
	seedProfile(t, st, uid)
	// Seed a primary photo so the selfie has something to compare against.
	mediaID := uuid.New()
	if _, err := st.CreatePhoto(context.Background(), uid, store.CreatePhotoParams{MediaID: mediaID, IsPrimary: true}); err != nil {
		t.Fatalf("seed photo: %v", err)
	}

	// Identical embeddings → cosine = 1.0, definitely passes.
	stored := []float64{1, 0, 0, 0}
	svc.SetMediaServiceClient(&fakeMediaClient{stored: stored})
	res, err := svc.CompleteSelfieFlow(context.Background(), uid, []float64{1, 0, 0, 0})
	if err != nil {
		t.Fatalf("happy: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected pass")
	}
	if res.TrustTier != "selfie" {
		t.Fatalf("expected tier selfie, got %s", res.TrustTier)
	}

	// Orthogonal vectors → cosine = 0, fails.
	res, err = svc.CompleteSelfieFlow(context.Background(), uid, []float64{0, 1, 0, 0})
	if err != nil {
		t.Fatalf("fail path: %v", err)
	}
	if res.Passed {
		t.Fatalf("expected fail with orthogonal embedding")
	}
}

func TestCompleteSelfieFlow_DoesNotDemoteFromAadhaar(t *testing.T) {
	svc, st, _, cleanup := newVerificationSvc(t)
	defer cleanup()
	uid := uuid.New()
	seedProfile(t, st, uid)
	if _, err := st.CreatePhoto(context.Background(), uid, store.CreatePhotoParams{MediaID: uuid.New(), IsPrimary: true}); err != nil {
		t.Fatalf("seed photo: %v", err)
	}
	// Pre-promote to aadhaar.
	if err := st.RecordAadhaarVerification(context.Background(), uid, "ref-1", "h"); err != nil {
		t.Fatalf("seed aadhaar: %v", err)
	}
	if err := st.UpdateTrustTier(context.Background(), uid, "aadhaar"); err != nil {
		t.Fatalf("seed tier: %v", err)
	}

	svc.SetMediaServiceClient(&fakeMediaClient{stored: []float64{1, 0, 0}})
	res, err := svc.CompleteSelfieFlow(context.Background(), uid, []float64{1, 0, 0})
	if err != nil {
		t.Fatalf("flow: %v", err)
	}
	// Should still report aadhaar.
	if res.TrustTier != "aadhaar" {
		t.Fatalf("expected aadhaar after selfie pass, got %s", res.TrustTier)
	}
}

func TestCompleteSelfieFlow_NoPrimaryPhoto(t *testing.T) {
	svc, st, _, cleanup := newVerificationSvc(t)
	defer cleanup()
	uid := uuid.New()
	seedProfile(t, st, uid)
	svc.SetMediaServiceClient(&fakeMediaClient{stored: []float64{1, 0, 0}})
	_, err := svc.CompleteSelfieFlow(context.Background(), uid, []float64{1, 0, 0})
	if err == nil || !strings.Contains(err.Error(), "no primary photo") {
		t.Fatalf("expected no-primary error, got %v", err)
	}
}

// hasTwelveDigitRun returns true if s contains 12 consecutive digits — a
// rough proxy for "this might be an Aadhaar number". Used in the DPDP
// smoke test.
func hasTwelveDigitRun(s string) bool {
	run := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			run++
			if run >= 12 {
				return true
			}
		} else {
			run = 0
		}
	}
	return false
}
