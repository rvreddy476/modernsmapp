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
	"time"

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
	return New(pool), func() { pool.Close() }
}

// seedDiscoverableProfile sets the columns FetchCandidates' WHERE
// clause requires (active status, approved primary photo, an adult
// birth_date, a non-NULL trust_tier, etc.) on top of the bare row
// inserted by ensureProfileForTest. Returns once the row is in a
// shape the discovery query will accept.
func seedDiscoverableProfile(t *testing.T, s *Store, id uuid.UUID, gender string) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.db.Exec(ctx, `
        UPDATE dating_profiles
        SET profile_status     = 'active',
            deleted_at         = NULL,
            paused             = false,
            visible_to_public  = true,
            birth_date         = $2,
            gender             = $3,
            trust_tier         = 'selfie',
            first_name         = 'tester'
        WHERE user_id = $1`, id,
		time.Date(1995, 1, 1, 0, 0, 0, 0, time.UTC), gender); err != nil {
		t.Fatalf("seed discoverable profile: %v", err)
	}
	// At least one approved primary photo is required by FetchCandidates.
	if _, err := s.db.Exec(ctx, `
        INSERT INTO dating_photos (user_id, media_id, sort_order, is_primary,
                                   visibility, moderation_status)
        VALUES ($1, $2, 0, true, 'public', 'approved')`,
		id, uuid.New()); err != nil {
		t.Fatalf("seed photo: %v", err)
	}
}

// TestFetchCandidates_IncognitoGate covers the brief's required case
// (a): FetchCandidates skips incognito profiles unless viewer liked
// them. We treat "liked" as the viewer having created at least one
// dating_sparks row aimed at the candidate (same predicate used in
// the WHERE clause).
func TestFetchCandidates_IncognitoGate(t *testing.T) {
	s, cleanup := privacyTestStore(t)
	defer cleanup()
	ctx := context.Background()

	viewer := uuid.New()
	incognitoCandidate := uuid.New()
	visibleCandidate := uuid.New()

	ensureProfileForTest(t, s, viewer)
	ensureProfileForTest(t, s, incognitoCandidate)
	ensureProfileForTest(t, s, visibleCandidate)
	seedDiscoverableProfile(t, s, viewer, "female")
	seedDiscoverableProfile(t, s, incognitoCandidate, "male")
	seedDiscoverableProfile(t, s, visibleCandidate, "male")

	// Flip incognitoCandidate to incognito mode.
	if _, err := s.UpdatePrivacy(ctx, incognitoCandidate, PrivacyUpdate{
		Incognito: ptrBool(true),
	}); err != nil {
		t.Fatalf("set incognito: %v", err)
	}

	candidates, err := s.FetchCandidates(ctx, CandidateQuery{
		ViewerID:     viewer,
		GenderFilter: "male",
		Limit:        50,
	})
	if err != nil {
		t.Fatalf("fetch candidates: %v", err)
	}

	if containsCandidate(candidates, incognitoCandidate) {
		t.Fatalf("incognito candidate %s leaked into deck before spark", incognitoCandidate)
	}
	if !containsCandidate(candidates, visibleCandidate) {
		t.Fatalf("visible candidate %s should appear in deck", visibleCandidate)
	}

	// Now the viewer sparks the incognito candidate. After the
	// spark lands the candidate should surface despite incognito.
	if _, err := s.CreateSpark(ctx, viewer, incognitoCandidate, "photo", "0", ""); err != nil {
		t.Fatalf("create spark: %v", err)
	}

	candidates, err = s.FetchCandidates(ctx, CandidateQuery{
		ViewerID:     viewer,
		GenderFilter: "male",
		Limit:        50,
	})
	if err != nil {
		t.Fatalf("fetch candidates post-spark: %v", err)
	}
	if !containsCandidate(candidates, incognitoCandidate) {
		t.Fatalf("incognito candidate %s did not surface after viewer sparked them", incognitoCandidate)
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
