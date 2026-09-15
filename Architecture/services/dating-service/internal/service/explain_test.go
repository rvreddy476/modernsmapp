// §P1-2 ExplainCandidate tests.
//
// Two layers:
//   - Pure-function helpers (distance reason, age formatter,
//     interest intersection) — no DB needed.
//   - End-to-end ExplainCandidate test that seeds a viewer and a
//     candidate in the viewer's deck + echo caches, then asserts the
//     returned reasons include "distance" (as a bucket) and
//     "shared_interest". Skipped unless TEST_PG_DSN is set. The lane D7
//     rules (deck-only, rate limit, buckets only) are pinned in
//     d7_location_privacy_it_test.go.
package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

func TestFormatDistanceReason(t *testing.T) {
	t.Parallel()
	for km, want := range map[float64]string{
		1:   "Less than 5 km away, inside your distance preference.",
		7:   "5–10 km away, inside your distance preference.",
		12:  "10–25 km away, inside your distance preference.",
		300: "25+ km away, inside your distance preference.",
	} {
		got := formatDistanceReason(store.DistanceBucketFor(km))
		if got != want {
			t.Errorf("%v km: got %q want %q", km, got, want)
		}
		for _, digit := range []string{"1 ", "7 ", "12", "300"} {
			if strings.Contains(got, digit) {
				t.Errorf("%v km: reason %q names the km figure", km, got)
			}
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

// TestExplainCandidate_DistanceAndSharedInterest exercises the distance +
// shared-interest reason paths end-to-end via the real store, for a candidate
// in the viewer's deck. Skipped without TEST_PG_DSN.
func TestExplainCandidate_DistanceAndSharedInterest(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	viewer, target := uuid.New(), uuid.New()
	gender := "d7-" + uuid.NewString()[:8]

	// ~12.6 km apart in Bengaluru: Cubbon Park and Whitefield-ish.
	seedActiveProfile(t, st, viewer)
	seedActiveProfile(t, st, target)
	d7SetLocation(t, st, viewer, 12.9762, 77.5993)
	d7SetLocation(t, st, target, 12.9784, 77.7150)
	if _, err := st.UpsertProfile(ctx, target, store.UpsertProfileParams{Gender: &gender}); err != nil {
		t.Fatalf("seed target gender: %v", err)
	}

	// Viewer preferences: 25km radius, the target's gender, ages 22-40.
	minA, maxA, dKm := 22, 40, 25
	if _, err := st.UpsertPreferences(ctx, viewer, store.UpsertPreferencesParams{
		MinAge:             &minA,
		MaxAge:             &maxA,
		DistanceKm:         &dKm,
		InterestedInGender: &gender,
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
	svc.InvalidatePulseCache(ctx, viewer)

	out, err := svc.ExplainCandidate(ctx, viewer, target)
	if err != nil {
		t.Fatalf("ExplainCandidate: %v", err)
	}
	if out.DistanceBucket != store.DistanceBucket10To25 || out.DistanceLabel != "10–25 km" {
		t.Errorf("distance = %q / %q, want km_10_25 / 10–25 km", out.DistanceBucket, out.DistanceLabel)
	}

	kinds := reasonKinds(out.Reasons)
	for _, want := range []string{"distance", "shared_interest", "age_match", "gender_pref"} {
		if !kinds[want] {
			t.Errorf("missing reason kind %q in %v", want, kinds)
		}
	}

	// is_promoted should be false: no boost key was ever set.
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
