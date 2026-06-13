// §P1-1 profile-status discovery gate integration tests.
//
// Skipped when TEST_PG_DSN is unset so a vanilla `go test ./...` still
// passes. The harness reuses ensureProfileForTest +
// seedDiscoverableProfile from sparks_test.go / privacy_test.go.
package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestFetchCandidates_ExcludesNonActive walks the profile_status state
// machine and verifies the discovery query surfaces ONLY rows whose
// status is 'active'. Every other state (draft, pending_photo,
// pending_selfie, pending_review, paused, restricted, suspended,
// deleted) must be filtered out.
//
// The viewer is themselves set up as a discoverable adult so the
// gender / age / photo prerequisites are non-issues. Candidates are
// seeded one per state under test plus a control 'active' candidate.
func TestFetchCandidates_ExcludesNonActive(t *testing.T) {
	s, cleanup := privacyTestStore(t)
	defer cleanup()
	ctx := context.Background()

	viewer := uuid.New()
	ensureProfileForTest(t, s, viewer)
	seedDiscoverableProfile(t, s, viewer, "female")

	// Each candidate goes through the full discoverable seed so the
	// only thing varying is profile_status. Without this, draft rows
	// might be excluded for a different reason (missing photo, etc.)
	// and the test wouldn't actually prove the status filter is the
	// gate.
	active := uuid.New()
	ensureProfileForTest(t, s, active)
	seedDiscoverableProfile(t, s, active, "male")

	nonActiveStates := []string{
		ProfileStatusDraft,
		ProfileStatusPendingPhoto,
		ProfileStatusPendingSelfie,
		ProfileStatusPendingReview,
		ProfileStatusPaused,
		ProfileStatusRestricted,
		ProfileStatusSuspended,
		ProfileStatusDeleted,
	}
	nonActiveCandidates := make(map[string]uuid.UUID, len(nonActiveStates))
	for _, st := range nonActiveStates {
		id := uuid.New()
		ensureProfileForTest(t, s, id)
		seedDiscoverableProfile(t, s, id, "male")
		if _, err := s.SetProfileStatus(ctx, id, st); err != nil {
			t.Fatalf("set status %s: %v", st, err)
		}
		nonActiveCandidates[st] = id
	}

	candidates, err := s.FetchCandidates(ctx, CandidateQuery{
		ViewerID:     viewer,
		GenderFilter: "male",
		Limit:        100,
	})
	if err != nil {
		t.Fatalf("fetch candidates: %v", err)
	}

	if !containsCandidate(candidates, active) {
		t.Fatalf("active candidate %s missing from deck", active)
	}
	for st, id := range nonActiveCandidates {
		if containsCandidate(candidates, id) {
			t.Fatalf("candidate with profile_status=%s (%s) leaked into deck", st, id)
		}
	}
}
