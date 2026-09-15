// §P1-3 privacy store tests.
//
// Two flavours:
//
//   - TestDistanceBucket (unit, no DB): verifies the §P1-3 coarse
//     distance buckets cover every range correctly, including
//     boundary km values and the unbounded "50km+" tail.
//
//   - TestFetchCandidates_IncognitoGate (integration, needs
//     TEST_PG_DSN): verifies the incognito hard-filter excludes a
//     candidate from a viewer's deck until the viewer has sent a
//     spark to that candidate.
//
// The integration test reuses the ensureProfileForTest helper
// defined in sparks_test.go (same package). Skipped without
// TEST_PG_DSN so a vanilla `go test ./...` still passes.
package store

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDistanceBucket(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		km   float64
		want string
	}{
		// 0-5km bucket: inclusive lower (incl. 0), exclusive upper.
		{"zero", 0, "0-5km"},
		{"just-under-5", 4.999, "0-5km"},
		// 5-10km bucket: 5 falls into the next band.
		{"at-5", 5.0, "5-10km"},
		{"mid-5-10", 7.5, "5-10km"},
		{"just-under-10", 9.999, "5-10km"},
		// 10-25km bucket.
		{"at-10", 10.0, "10-25km"},
		{"mid-10-25", 17.0, "10-25km"},
		{"just-under-25", 24.999, "10-25km"},
		// 25-50km bucket.
		{"at-25", 25.0, "25-50km"},
		{"mid-25-50", 37.0, "25-50km"},
		{"just-under-50", 49.999, "25-50km"},
		// 50km+ bucket: inclusive lower, unbounded above.
		{"at-50", 50.0, "50km+"},
		{"far", 12500.0, "50km+"},
		// Defensive: negatives should collapse into the lowest
		// bucket so a malformed haversine never produces an empty
		// label.
		{"negative", -3.0, "0-5km"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := DistanceBucket(tc.km); got != tc.want {
				t.Fatalf("DistanceBucket(%v) = %q; want %q", tc.km, got, tc.want)
			}
		})
	}
}

// privacyTestStore mirrors the matches/sparks helper so the privacy
// test honours the same TEST_PG_DSN convention.
func privacyTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping privacy store integration tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	ensureStoreSchemaForTest(t, pool)
	return New(pool), func() { pool.Close() }
}

// seedDiscoverableProfile gives the bare row from ensureProfileForTest
// everything FetchCandidates' WHERE clause requires (an adult birth_date, a
// non-NULL trust_tier, an approved primary photo) plus the onboarding
// evidence, then walks it to 'active' through TransitionProfileStatus.
func seedDiscoverableProfile(t *testing.T, s *Store, id uuid.UUID, gender string) {
	t.Helper()
	seedOnboardingEvidence(t, s, id, gender)
	driveTo(t, s, id, ProfileStatusActive)
}

// TestFetchCandidates_IncognitoGate pins the schema-doc meaning of
// incognito (lane D3): an incognito profile appears only to people IT has
// sparked. The viewer sparking the incognito profile reveals nothing; the
// incognito profile sparking the viewer does. A unique gender keeps other
// test rows out of the deck.
func TestFetchCandidates_IncognitoGate(t *testing.T) {
	s, cleanup := privacyTestStore(t)
	defer cleanup()
	ctx := context.Background()

	gender := "d3-" + uuid.NewString()[:8]
	viewer := uuid.New()
	incognitoCandidate := uuid.New()
	visibleCandidate := uuid.New()

	ensureProfileForTest(t, s, viewer)
	ensureProfileForTest(t, s, incognitoCandidate)
	ensureProfileForTest(t, s, visibleCandidate)
	seedDiscoverableProfile(t, s, viewer, "female")
	seedDiscoverableProfile(t, s, incognitoCandidate, gender)
	seedDiscoverableProfile(t, s, visibleCandidate, gender)

	// Flip incognitoCandidate to incognito mode.
	if _, err := s.UpdatePrivacy(ctx, incognitoCandidate, PrivacyUpdate{
		Incognito: ptrBool(true),
	}); err != nil {
		t.Fatalf("set incognito: %v", err)
	}

	fetch := func() []CandidateProfile {
		t.Helper()
		candidates, err := s.FetchCandidates(ctx, CandidateQuery{
			ViewerID:     viewer,
			GenderFilter: gender,
			Limit:        50,
		})
		if err != nil {
			t.Fatalf("fetch candidates: %v", err)
		}
		return candidates
	}

	candidates := fetch()
	if containsCandidate(candidates, incognitoCandidate) {
		t.Fatalf("incognito candidate %s leaked into deck before any spark", incognitoCandidate)
	}
	if !containsCandidate(candidates, visibleCandidate) {
		t.Fatalf("visible candidate %s should appear in deck", visibleCandidate)
	}

	// The viewer sparking the incognito profile must not reveal it.
	if _, err := s.CreateSpark(ctx, viewer, incognitoCandidate, "photo", "0", ""); err != nil {
		t.Fatalf("create spark viewer->incognito: %v", err)
	}
	if containsCandidate(fetch(), incognitoCandidate) {
		t.Fatalf("incognito candidate %s surfaced because the VIEWER sparked them", incognitoCandidate)
	}

	// The incognito profile sparking the viewer reveals it to the viewer.
	if _, err := s.CreateSpark(ctx, incognitoCandidate, viewer, "photo", "0", ""); err != nil {
		t.Fatalf("create spark incognito->viewer: %v", err)
	}
	if !containsCandidate(fetch(), incognitoCandidate) {
		t.Fatalf("incognito candidate %s hidden from a viewer they sparked", incognitoCandidate)
	}
}

func containsCandidate(cs []CandidateProfile, id uuid.UUID) bool {
	for _, c := range cs {
		if c.UserID == id {
			return true
		}
	}
	return false
}

func ptrBool(b bool) *bool { return &b }
