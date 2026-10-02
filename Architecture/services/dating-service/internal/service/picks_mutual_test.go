package service

import (
	"testing"

	"github.com/atpost/dating-service/internal/store"
)

// admitsViewer is the "would they see you too" half of mutual picks. Each
// rule gets its own case so neutering one fails exactly that case.
func TestAdmitsViewer(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	near, far := f(17.41), f(17.9) // about 3 km and 57 km from 17.385
	vLat, lon := f(17.385), f(78.4867)
	cases := []struct {
		name     string
		w        store.PickReciprocity
		age      int
		intent   string
		verified bool
		cLat     *float64
		want     bool
	}{
		{"no preferences admit anyone", store.PickReciprocity{}, 30, "casual", false, near, true},
		{"below their age range", store.PickReciprocity{MinAge: 35}, 30, "casual", true, near, false},
		{"above their age range", store.PickReciprocity{MaxAge: 25}, 30, "casual", true, near, false},
		{"inside their age range", store.PickReciprocity{MinAge: 25, MaxAge: 35}, 30, "casual", true, near, true},
		{"unknown viewer age is not held against them", store.PickReciprocity{MinAge: 35}, 0, "casual", true, near, true},
		{"an intent they do not want", store.PickReciprocity{IntentFilter: []string{"marriage"}}, 30, "casual", true, near, false},
		{"an intent they want", store.PickReciprocity{IntentFilter: []string{"serious", "casual"}}, 30, "casual", true, near, true},
		{"they want verified, viewer is not", store.PickReciprocity{VerifiedOnly: true}, 30, "casual", false, near, false},
		{"they want verified, viewer is", store.PickReciprocity{VerifiedOnly: true}, 30, "casual", true, near, true},
		{"outside their distance", store.PickReciprocity{DistanceKm: 10}, 30, "casual", true, far, false},
		{"outside the default distance", store.PickReciprocity{}, 30, "casual", true, far, false},
		{"inside their distance", store.PickReciprocity{DistanceKm: 100}, 30, "casual", true, far, true},
		{"no location is not held against them", store.PickReciprocity{DistanceKm: 10}, 30, "casual", true, nil, true},
	}
	for _, tc := range cases {
		var cLon *float64
		if tc.cLat != nil {
			cLon = lon
		}
		if got := admitsViewer(tc.w, tc.age, tc.intent, tc.verified, vLat, lon, tc.cLat, cLon); got != tc.want {
			t.Errorf("%s: admitsViewer = %v, want %v", tc.name, got, tc.want)
		}
	}
}
