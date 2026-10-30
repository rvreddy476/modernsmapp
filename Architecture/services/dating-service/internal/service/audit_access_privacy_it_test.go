// Audit 2026-10-30, access & privacy lane — service flows on the test
// database (CI only; skipped without TEST_PG_DSN).
package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/dating-service/internal/digilocker"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// M1: one DigiLocker sign-in cannot badge a second account; the first keeps
// its 'aadhaar' tier, the second stays where it was.
func TestAadhaarFlow_OnePersonOneAccount(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	svc.SetDigiLockerClient(digilocker.NewMockClient())
	ctx := context.Background()
	first, second := uuid.New(), uuid.New()
	seedActiveProfile(t, st, first)
	seedActiveProfile(t, st, second)
	code := "same-person-" + uuid.NewString()[:8] // the mock maps one code to one subject

	out, err := svc.CompleteAadhaarFlow(ctx, first, code, "state-1")
	if err != nil || out.TrustTier != "aadhaar" {
		t.Fatalf("first account: %+v %v", out, err)
	}
	if _, err := svc.CompleteAadhaarFlow(ctx, second, code, "state-2"); !errors.Is(err, store.ErrAadhaarAlreadyLinked) {
		t.Fatalf("second account: err=%v, want ErrAadhaarAlreadyLinked", err)
	}
	if p, err := st.GetProfile(ctx, second); err != nil || p.TrustTier == "aadhaar" {
		t.Fatalf("second account tier = %v (%v); must not be aadhaar", p, err)
	}
}

// H1: rewinding a pass on someone who has since hidden their profile is
// refused, and costs no rewind.
func TestRewind_RefusesACandidateHiddenSincePass(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	svc.SetMechanicsConfig(MechanicsConfig{Rewind: true, RewindDailyLimitFree: 1})
	ctx := context.Background()
	viewer, candidate := uuid.New(), uuid.New()
	seedActiveProfile(t, st, viewer)
	seedActiveProfile(t, st, candidate)
	if _, err := st.RecordPass(ctx, viewer, candidate, ""); err != nil {
		t.Fatal(err)
	}
	hidden := false
	if _, err := st.UpsertProfile(ctx, candidate, store.UpsertProfileParams{VisibleToPublic: &hidden}); err != nil {
		t.Fatalf("hide: %v", err)
	}
	if _, err := svc.RewindLastPass(ctx, viewer); !errors.Is(err, ErrCandidateUnavailable) {
		t.Fatalf("rewind onto a hidden profile: err=%v, want ErrCandidateUnavailable", err)
	}
	if used, _, err := st.RewindUsage(ctx, viewer); err != nil || used != 0 {
		t.Fatalf("refused rewind counted (used=%d err=%v)", used, err)
	}
}
