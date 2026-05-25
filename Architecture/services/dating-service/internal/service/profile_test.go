// §P1-1 profile-status lifecycle tests.
//
// Skipped when TEST_PG_DSN is unset so `go test ./...` stays green on
// developer laptops without a Postgres at hand. The integration paths
// share the newSvcForTest harness from spark_test.go.
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// TestProfileTransition_DraftToActive walks the §P1-1 state machine on
// the supported transitions:
//
//	draft -> pending_photo  (UpsertProfile populates min onboarding)
//	pending_photo -> pending_selfie / pending_selfie -> active
//	    (store.SetProfileStatus by the photos / verification consumers)
//
// We assert the row lands in pending_photo on the first upsert, then
// SetProfileStatus walks it through the rest. The verification-consumer
// is exercised at the store level (the service helper that flips to
// 'active' lives in verification.go and depends on a Track-1 verifier
// stub — beyond this test's scope; the store call is the contract).
func TestProfileTransition_DraftToActive(t *testing.T) {
	svc, st, cleanup := newSvcForTest(t)
	defer cleanup()
	ctx := context.Background()

	user := uuid.New()

	// Step 1 — seed: bare profile row, no min-onboarding fields. The
	// store helper sidesteps the service's requireAdult so we can
	// inspect the very first state.
	if _, err := st.UpsertProfile(ctx, user, store.UpsertProfileParams{}); err != nil {
		t.Fatalf("seed bare profile: %v", err)
	}
	p, err := st.GetProfile(ctx, user)
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	if p.ProfileStatus != store.ProfileStatusDraft {
		t.Fatalf("expected draft on bare profile, got %s", p.ProfileStatus)
	}

	// Step 2 — UpsertProfile via service with the min onboarding
	// payload. Should graduate to pending_photo per
	// hasMinimumOnboardingFields.
	intent := "casual"
	gender := "female"
	city := "Hyderabad"
	dob := time.Date(1995, 1, 1, 0, 0, 0, 0, time.UTC)
	p, err = svc.UpsertProfile(ctx, user, store.UpsertProfileParams{
		Intent:    &intent,
		Gender:    &gender,
		BirthDate: &dob,
		City:      &city,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if p.ProfileStatus != store.ProfileStatusPendingPhoto {
		t.Fatalf("expected pending_photo after min onboarding, got %s", p.ProfileStatus)
	}

	// Step 3 — photos-consumer step: bump to pending_selfie.
	if _, err := st.SetProfileStatus(ctx, user, store.ProfileStatusPendingSelfie); err != nil {
		t.Fatalf("set pending_selfie: %v", err)
	}
	p, err = st.GetProfile(ctx, user)
	if err != nil {
		t.Fatalf("get after pending_selfie: %v", err)
	}
	if p.ProfileStatus != store.ProfileStatusPendingSelfie {
		t.Fatalf("expected pending_selfie, got %s", p.ProfileStatus)
	}

	// Step 4 — verification-consumer step: bump to active.
	if _, err := st.SetProfileStatus(ctx, user, store.ProfileStatusActive); err != nil {
		t.Fatalf("set active: %v", err)
	}
	p, err = st.GetProfile(ctx, user)
	if err != nil {
		t.Fatalf("get after active: %v", err)
	}
	if p.ProfileStatus != store.ProfileStatusActive {
		t.Fatalf("expected active, got %s", p.ProfileStatus)
	}

	// Step 5 — moderator-flag step (§P1-1 pending_review). After this
	// the user's outbound interactions must be blocked.
	if _, err := st.SetProfileStatus(ctx, user, store.ProfileStatusPendingReview); err != nil {
		t.Fatalf("set pending_review: %v", err)
	}
	if err := svc.requireInteractiveProfile(ctx, user); !errors.Is(err, ErrProfilePendingReview) {
		t.Fatalf("expected pending_review gate, got %v", err)
	}
}
