// §P1-2 ExplainCandidate tests.
//
// Two layers:
//   - Pure-function helpers (distance capping, age formatter,
//     interest intersection) — no DB needed.
//   - End-to-end ExplainCandidate test that seeds two profiles +
//     preferences + echo caches, then asserts the returned
//     reasons include "distance" and "shared_interest". Skipped
//     unless TEST_PG_DSN is set, matching the rest of the
//     dating-service test suite.
package service

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCapDistanceKm(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw   float64
		maxKm int
		want  int
	}{
		{-1, 25, 0},        // unknown → 0
		{0.4, 25, 0},       // round down
		{0.7, 25, 1},       // round up
		{12.4, 25, 12},
		{12.6, 25, 13},
		{30, 25, 25},       // cap at max
		{40, 0, 40},        // no cap when max=0
		{0, 25, 0},
	}
	for _, tc := range cases {
		if got := capDistanceKm(tc.raw, tc.maxKm); got != tc.want {
			t.Errorf("capDistanceKm(%.1f, %d) = %d want %d", tc.raw, tc.maxKm, got, tc.want)
		}
	}
}

func TestAgeFromBirthDate(t *testing.T) {
	t.Parallel()
	// Zero time → 0
	if got := ageFromBirthDate(time.Time{}); got != 0 {
		t.Errorf("zero time: got %d want 0", got)
	}
	// Future birth → 0 (clamped, not negative)
	future := time.Now().AddDate(1, 0, 0)
	if got := ageFromBirthDate(future); got != 0 {
		t.Errorf("future: got %d want 0 (clamped)", got)
	}
	// 30 years ago → 30
	thirty := time.Now().AddDate(-30, 0, -1)
	if got := ageFromBirthDate(thirty); got != 30 {
		t.Errorf("30y ago: got %d want 30", got)
	}
}

func TestIntersectStrings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		a, b []string
		want []string
	}{
		{"empty", nil, nil, nil},
		{"emptyA", nil, []string{"x"}, nil},
		{"emptyB", []string{"x"}, nil, nil},
		{"noOverlap", []string{"a"}, []string{"b"}, []string{}},
		{"oneOverlap", []string{"a", "b"}, []string{"b", "c"}, []string{"b"}},
		{"multi", []string{"a", "b", "c"}, []string{"c", "a", "z"}, []string{"a", "c"}},
		{"dedup", []string{"a", "a", "b"}, []string{"a"}, []string{"a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := intersectStrings(tc.a, tc.b)
			// Order in `got` follows b's order — assert via set
			// equality (size + every want member present).
			if len(got) != len(tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			set := make(map[string]bool, len(got))
			for _, s := range got {
				set[s] = true
			}
			for _, w := range tc.want {
				if !set[w] {
					t.Errorf("missing %q from %v (want %v)", w, got, tc.want)
				}
			}
		})
	}
}

func TestFormatSharedInterests(t *testing.T) {
	t.Parallel()
	cases := []struct {
		topics []string
		want   string
	}{
		{nil, "You both engage with shared topics on AtPost."},
		{[]string{"climbing"}, "You both engage with climbing on AtPost."},
		{[]string{"climbing", "coffee"}, "You both engage with climbing and coffee on AtPost."},
		{[]string{"climbing", "coffee", "books", "extra"}, "You both engage with climbing, coffee, and books on AtPost."},
	}
	for _, tc := range cases {
		if got := formatSharedInterests(tc.topics); got != tc.want {
			t.Errorf("topics=%v\n got %q\nwant %q", tc.topics, got, tc.want)
		}
	}
}

// TestExplainCandidate_DistanceAndSharedInterest covers the brief's
// minimum requirement: exercise the distance + shared-interest
// reason paths end-to-end via the real store. Skipped without
// TEST_PG_DSN, like the rest of the dating-service test suite.
func TestExplainCandidate_DistanceAndSharedInterest(t *testing.T) {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping ExplainCandidate DB test")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	st := store.New(pool)
	svc := New(st, nil)

	ctx := context.Background()
	viewer, target := uuid.New(), uuid.New()

	// Seed both profiles with coordinates ~12km apart in Bangalore.
	// Viewer at Cubbon Park, target at Whitefield-ish.
	viewerLat, viewerLon := 12.9762, 77.5993
	targetLat, targetLon := 12.9784, 77.7150

	gender := "female"
	birth := time.Now().AddDate(-28, 0, 0) // 28 years old

	if _, err := st.UpsertProfile(ctx, viewer, store.UpsertProfileParams{
		Latitude:  &viewerLat,
		Longitude: &viewerLon,
	}); err != nil {
		t.Fatalf("seed viewer: %v", err)
	}
	if _, err := st.UpsertProfile(ctx, target, store.UpsertProfileParams{
		Latitude:  &targetLat,
		Longitude: &targetLon,
		Gender:    &gender,
		BirthDate: &birth,
	}); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	// Viewer preferences: 25km radius, want female, ages 22-35.
	minA, maxA, dKm := 22, 35, 25
	wantGender := "female"
	if _, err := st.UpsertPreferences(ctx, viewer, store.UpsertPreferencesParams{
		MinAge:             &minA,
		MaxAge:             &maxA,
		DistanceKm:         &dKm,
		InterestedInGender: &wantGender,
	}); err != nil {
		t.Fatalf("seed prefs: %v", err)
	}

	// Both users surface the same QA topic ("climbing") in their
	// echo caches so the shared-interest reason fires.
	viewerQA := mustJSON(t, []map[string]any{{"topic": "climbing"}, {"topic": "books"}})
	targetQA := mustJSON(t, []map[string]any{{"topic": "climbing"}, {"topic": "coffee"}})
	if err := st.UpsertEchoCache(ctx, viewer, []byte("[]"), viewerQA, []byte("[]"), []byte("[]")); err != nil {
		t.Fatalf("seed viewer echo: %v", err)
	}
	if err := st.UpsertEchoCache(ctx, target, []byte("[]"), targetQA, []byte("[]"), []byte("[]")); err != nil {
		t.Fatalf("seed target echo: %v", err)
	}

	out, err := svc.ExplainCandidate(ctx, viewer, target)
	if err != nil {
		t.Fatalf("ExplainCandidate: %v", err)
	}
	if out == nil {
		t.Fatalf("nil response")
	}

	// Distance: ~12.6 km between the seeded points; rounded → 12 or
	// 13, well inside the 25km cap.
	if out.DistanceKm <= 0 || out.DistanceKm > 25 {
		t.Errorf("distance_km = %d, want 1..25", out.DistanceKm)
	}

	// Assert the expected reason kinds are present.
	kinds := reasonKinds(out.Reasons)
	for _, want := range []string{"distance", "shared_interest", "age_match", "gender_pref"} {
		if !kinds[want] {
			t.Errorf("missing reason kind %q in %v", want, kinds)
		}
	}

	// is_promoted should be false: we never set the boost key + rdb is nil.
	if out.IsPromoted {
		t.Errorf("is_promoted should be false when no boost active")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func reasonKinds(reasons []ExplainReason) map[string]bool {
	m := make(map[string]bool, len(reasons))
	for _, r := range reasons {
		m[r.Kind] = true
	}
	return m
}
