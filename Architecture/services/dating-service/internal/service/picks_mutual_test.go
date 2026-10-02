package service

import (
	"testing"

	"github.com/atpost/dating-service/internal/store"
)

func fp(v float64) *float64 { return &v }
func ip(v int) *int         { return &v }
func sp(v string) *string   { return &v }

var (
	nearLat, farLat = fp(17.41), fp(17.9) // about 3 km and 57 km from 17.385
	testLon         = fp(78.4867)
)

func testViewer() viewerFacts {
	return viewerFacts{Age: 30, Intent: "casual", Verified: true, Lat: fp(17.385), Lon: testLon,
		HeightCm: ip(170), Languages: []string{"en"}, Drinking: sp("socially"), Smoking: sp("never"),
		Exercise: sp("often"), Diet: sp("vegetarian")}
}

// admitsViewer is the "would they see you too" half of mutual picks. Each
// rule gets its own case so neutering one fails exactly that case.
func TestAdmitsViewer(t *testing.T) {
	cases := []struct {
		name string
		t    store.TheirPreferences
		edit func(*viewerFacts)
		cLat *float64
		want bool
	}{
		{"no preferences admit anyone", store.TheirPreferences{}, func(v *viewerFacts) { v.Verified = false }, nearLat, true},
		{"below their age range", store.TheirPreferences{MinAge: 35}, nil, nearLat, false},
		{"above their age range", store.TheirPreferences{MaxAge: 25}, nil, nearLat, false},
		{"inside their age range", store.TheirPreferences{MinAge: 25, MaxAge: 35}, nil, nearLat, true},
		{"unknown viewer age is not held against them", store.TheirPreferences{MinAge: 35}, func(v *viewerFacts) { v.Age = 0 }, nearLat, true},
		{"an intent they do not want", store.TheirPreferences{IntentFilter: []string{"marriage"}}, nil, nearLat, false},
		{"an intent they want", store.TheirPreferences{IntentFilter: []string{"serious", "casual"}}, nil, nearLat, true},
		{"they want verified, viewer is not", store.TheirPreferences{PrivacyVerifiedOnly: true}, func(v *viewerFacts) { v.Verified = false }, nearLat, false},
		{"they want verified, viewer is", store.TheirPreferences{PrivacyVerifiedOnly: true}, nil, nearLat, true},
		{"outside their distance", store.TheirPreferences{DistanceKm: 10}, nil, farLat, false},
		{"outside the default distance", store.TheirPreferences{}, nil, farLat, false},
		{"inside their distance", store.TheirPreferences{DistanceKm: 100}, nil, farLat, true},
		{"no location is not held against them", store.TheirPreferences{DistanceKm: 10}, nil, nil, true},
	}
	for _, tc := range cases {
		v := testViewer()
		if tc.edit != nil {
			tc.edit(&v)
		}
		var cLon *float64
		if tc.cLat != nil {
			cLon = testLon
		}
		if got := admitsViewer(tc.t, v, tc.cLat, cLon); got != tc.want {
			t.Errorf("%s: admitsViewer = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Dealbreakers (M12): only the marked preferences count, pass ones only
// while the candidate holds a pass, and a missing viewer value fails a pass
// dealbreaker. One case per rule.
func TestPassesTheirDealbreakers(t *testing.T) {
	pass := func(f store.PassFilters, codes ...string) store.TheirPreferences {
		return store.TheirPreferences{Pass: f, HoldsPass: true, Dealbreakers: codes}
	}
	cases := []struct {
		name string
		t    store.TheirPreferences
		edit func(*viewerFacts)
		want bool
	}{
		{"an unmarked preference is not a dealbreaker", store.TheirPreferences{MinAge: 35}, nil, true},
		{"age dealbreaker", store.TheirPreferences{MinAge: 35, Dealbreakers: []string{"age"}}, nil, false},
		{"intent dealbreaker", store.TheirPreferences{IntentFilter: []string{"marriage"}, Dealbreakers: []string{"intent"}}, nil, false},
		{"distance dealbreaker", store.TheirPreferences{DistanceKm: 1, Dealbreakers: []string{"distance"}}, func(v *viewerFacts) { v.Lat = farLat }, false},
		{"verified dealbreaker", pass(store.PassFilters{VerifiedOnly: true}, "verified"), func(v *viewerFacts) { v.Verified = false }, false},
		{"height dealbreaker", pass(store.PassFilters{MinHeightCm: ip(180)}, "height"), nil, false},
		{"height unknown fails", pass(store.PassFilters{MaxHeightCm: ip(190)}, "height"), func(v *viewerFacts) { v.HeightCm = nil }, false},
		{"height inside passes", pass(store.PassFilters{MinHeightCm: ip(160), MaxHeightCm: ip(180)}, "height"), nil, true},
		{"languages dealbreaker", pass(store.PassFilters{Languages: []string{"te"}}, "languages"), nil, false},
		{"a shared language passes", pass(store.PassFilters{Languages: []string{"te", "en"}}, "languages"), nil, true},
		{"drinking dealbreaker", pass(store.PassFilters{Drinking: []string{"never"}}, "drinking"), nil, false},
		{"smoking dealbreaker", pass(store.PassFilters{Smoking: []string{"regularly"}}, "smoking"), nil, false},
		{"exercise dealbreaker", pass(store.PassFilters{Exercise: []string{"never"}}, "exercise"), nil, false},
		{"diet dealbreaker", pass(store.PassFilters{Diet: []string{"vegan"}}, "diet"), nil, false},
		{"diet unknown fails", pass(store.PassFilters{Diet: []string{"vegetarian"}}, "diet"), func(v *viewerFacts) { v.Diet = nil }, false},
		{"a pass dealbreaker without a pass counts for nothing",
			store.TheirPreferences{Pass: store.PassFilters{Diet: []string{"vegan"}}, Dealbreakers: []string{"diet"}}, nil, true},
	}
	for _, tc := range cases {
		v := testViewer()
		if tc.edit != nil {
			tc.edit(&v)
		}
		if got := passesTheirDealbreakers(tc.t, v, nearLat, testLon); got != tc.want {
			t.Errorf("%s: passesTheirDealbreakers = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestValidateDealbreakers(t *testing.T) {
	if needs, err := validateDealbreakers([]string{"age", "distance", "intent"}); err != nil || needs {
		t.Fatalf("free codes: needsPass=%v err=%v", needs, err)
	}
	if needs, err := validateDealbreakers([]string{"age", "diet"}); err != nil || !needs {
		t.Fatalf("a pass code: needsPass=%v err=%v", needs, err)
	}
	for _, bad := range [][]string{{"religion"}, {"age", "age"}} {
		if _, err := validateDealbreakers(bad); err == nil {
			t.Fatalf("%v was accepted", bad)
		}
	}
}
