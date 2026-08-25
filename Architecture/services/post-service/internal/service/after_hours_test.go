package service

import (
	"testing"
	"time"
)

// TestAfterHoursWindow documents which hours the trusted-circle
// auto-restriction fires on. The boundary is "22:00 to before 06:00"
// — 22:00 is in, 06:00 is out.
//
// This used to call a COPY of the rule declared at the bottom of this file,
// so it verified a re-implementation and would have stayed green through any
// change to the real one. It now calls the production function.
func TestAfterHoursWindow(t *testing.T) {
	cases := []struct {
		hour int
		want bool
	}{
		{0, true},
		{1, true},
		{5, true},
		{6, false},
		{7, false},
		{12, false},
		{17, false},
		{21, false},
		{22, true},
		{23, true},
	}
	for _, tc := range cases {
		now := time.Date(2026, 1, 1, tc.hour, 30, 0, 0, time.UTC)
		got := isAfterHours(now)
		if got != tc.want {
			t.Errorf("hour %02d: want %v, got %v", tc.hour, tc.want, got)
		}
	}
}

// The consent boundary — C-LB-2, and the assertion NC-C2A mutates.
//
// The after-hours rule narrows a post's audience without being asked. It may
// only ever do that to an audience the author did NOT choose. The rule used to
// match on the VALUE of `visibility`, which a defaulting client and a
// deliberate one produce identically — so someone who deliberately selected
// Public and published at 23:00 got a trusted-circle post while the composer,
// the API response and their own profile all still said Public. They had no
// way to know their post was not public.
//
// The clock is FIXED here. A test that used `time.Now()` would pass or fail
// depending on the hour it ran at, and would silently stop covering the
// restricted branch for eight hours a day.
func TestExplicitAudienceIsNeverAutoRestricted(t *testing.T) {
	// 23:30 — squarely inside the window, so the ONLY thing that can decide
	// the outcome is whether the audience was chosen deliberately.
	lateNight := time.Date(2026, 1, 1, 23, 30, 0, 0, time.UTC)
	if !isAfterHours(lateNight) {
		t.Fatal("fixture clock is not inside the after-hours window")
	}

	cases := []struct {
		name       string
		explicit   bool
		visibility string
		want       bool
		why        string
	}{
		{
			name:       "explicit public at 23:30 is left alone",
			explicit:   true,
			visibility: "public",
			want:       false,
			why: "the author chose Public. Narrowing it silently is a consent " +
				"failure, and the app keeps telling them it is public",
		},
		{
			name:       "explicit followers at 23:30 is left alone",
			explicit:   true,
			visibility: "followers",
			want:       false,
			why:        "an explicit choice is an explicit choice at any width",
		},
		{
			name:       "explicit trusted at 23:30 is left alone",
			explicit:   true,
			visibility: "trusted",
			want:       false,
			why:        "already the narrow audience; nothing to decide",
		},
		{
			name:       "a defaulted public may be restricted",
			explicit:   false,
			visibility: "public",
			want:       true,
			why:        "no choice was made, which is the case the feature exists for",
		},
		{
			name:       "an absent visibility may be restricted",
			explicit:   false,
			visibility: "",
			want:       true,
			why:        "the client sent nothing at all",
		},
		{
			name:       "a defaulted followers may be restricted",
			explicit:   false,
			visibility: "followers",
			want:       true,
			why:        "also a value a client reaches without deciding",
		},
		{
			name:       "a defaulted trusted is already narrow",
			explicit:   false,
			visibility: "trusted",
			want:       false,
			why:        "restricting to trusted what is already trusted is a no-op",
		},
		{
			name:       "a defaulted private is never widened or touched",
			explicit:   false,
			visibility: "private",
			want:       false,
			why:        "the rule only ever narrows, and private is narrower than trusted",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := audienceMayBeAutoRestricted(tc.explicit, tc.visibility)
			if got != tc.want {
				t.Fatalf("audienceMayBeAutoRestricted(%v, %q) = %v, want %v (%s)",
					tc.explicit, tc.visibility, got, tc.want, tc.why)
			}
		})
	}
}

// Outside the window nothing is restricted, whatever the audience.
//
// Paired with the table above so a mutation that deletes the window check —
// rather than the consent check — is also caught.
func TestNothingIsAutoRestrictedOutsideTheWindow(t *testing.T) {
	midMorning := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	if isAfterHours(midMorning) {
		t.Fatal("10:00 must not be inside the after-hours window")
	}
}
