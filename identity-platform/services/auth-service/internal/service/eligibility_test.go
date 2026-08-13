package service

import (
	"errors"
	"testing"
	"time"
)

// Module 3 M3-P0-3 / SR-6 — the registration age and consent gate.
//
// The old gate was `minimumAgeYears = 13` AND returned nil when no date of
// birth was supplied. Since `dob` was an optional request field, any client
// could bypass the check by omitting it: the gate was advisory. There was no
// consent capture at all.

var refNow = time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

func validConsent() RegistrationConsent {
	return RegistrationConsent{Accepted: true, Version: CurrentTermsVersion}
}

// THE BYPASS. An absent date of birth must be a rejection, never a pass.
func TestMissingDOBIsRejectedNotSkipped(t *testing.T) {
	for _, dob := range []string{"", "   ", "\t"} {
		err := CheckRegistrationEligibility(dob, validConsent(), refNow)
		if !errors.Is(err, ErrDOBRequired) {
			t.Fatalf("dob %q: got %v, want ErrDOBRequired. Omitting the field used to "+
				"skip the age check entirely, which made the gate advisory.", dob, err)
		}
	}
}

func TestMalformedOrImpossibleDOBIsRejected(t *testing.T) {
	cases := map[string]error{
		"not-a-date": ErrDOBMalformed,
		"11/08/1990": ErrDOBMalformed,
		"1990-13-01": ErrDOBMalformed,
		"1990-02-30": ErrDOBMalformed,
		"2030-01-01": ErrDOBInFuture,
		"9999-01-01": ErrDOBInFuture,
	}
	for dob, want := range cases {
		if err := CheckRegistrationEligibility(dob, validConsent(), refNow); !errors.Is(err, want) {
			t.Errorf("dob %q: got %v, want %v", dob, err, want)
		}
	}
}

// The boundary is the part worth testing precisely: an off-by-one day admits
// someone the platform has no lawful basis to onboard.
func TestAgeBoundaryIsExactToTheDay(t *testing.T) {
	cases := []struct {
		name    string
		dob     string
		allowed bool
	}{
		{"turns 18 tomorrow", "2008-08-12", false},
		{"turns 18 today", "2008-08-11", true},
		{"turned 18 yesterday", "2008-08-10", true},
		{"17 years and 364 days", "2008-08-12", false},
		{"clearly a child", "2015-01-01", false},
		{"clearly an adult", "1990-01-01", true},
		// Leap-day birthday: a duration/365.25 approximation gets this wrong.
		{"born on a leap day, now 18", "2008-02-29", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckRegistrationEligibility(tc.dob, validConsent(), refNow)
			if tc.allowed && err != nil {
				t.Fatalf("dob %s should be allowed, got %v", tc.dob, err)
			}
			if !tc.allowed && !errors.Is(err, ErrUnderage) {
				t.Fatalf("dob %s should be refused as underage, got %v", tc.dob, err)
			}
		})
	}
}

// 13 was the old floor. Anyone between 13 and 18 used to be admitted, and the
// platform has no verifiable parental-consent flow for them.
func TestThirteenToSeventeenAreNowRefused(t *testing.T) {
	for _, dob := range []string{"2013-08-11", "2010-01-01", "2009-08-12"} {
		if err := CheckRegistrationEligibility(dob, validConsent(), refNow); !errors.Is(err, ErrUnderage) {
			t.Errorf("dob %s (13–17) was admitted: %v", dob, err)
		}
	}
}

func TestConsentMustBeExplicitAndVersioned(t *testing.T) {
	adult := "1990-01-01"

	// A zero-value consent — what a client that omits the fields sends.
	if err := CheckRegistrationEligibility(adult, RegistrationConsent{}, refNow); !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("a registration with no consent was accepted: %v", err)
	}
	// Accepted but no version: nothing records WHICH text was agreed to.
	if err := CheckRegistrationEligibility(adult, RegistrationConsent{Accepted: true}, refNow); !errors.Is(err, ErrConsentVersionMismatch) {
		t.Errorf("consent without a version was accepted: %v", err)
	}
	// Accepted an older text.
	old := RegistrationConsent{Accepted: true, Version: "2020-01-01"}
	if err := CheckRegistrationEligibility(adult, old, refNow); !errors.Is(err, ErrConsentVersionMismatch) {
		t.Errorf("consent to a superseded version was accepted: %v", err)
	}
	// The happy path.
	if err := CheckRegistrationEligibility(adult, validConsent(), refNow); err != nil {
		t.Errorf("a valid adult registration was refused: %v", err)
	}
}

// The age check must run BEFORE consent: telling a 12-year-old to accept the
// terms first, then refusing them, collects a consent that cannot be relied on.
func TestUnderageIsRefusedBeforeConsentIsConsidered(t *testing.T) {
	err := CheckRegistrationEligibility("2015-01-01", RegistrationConsent{}, refNow)
	if !errors.Is(err, ErrUnderage) {
		t.Fatalf("got %v, want ErrUnderage: a minor was asked about consent instead "+
			"of being refused outright", err)
	}
}

func TestMinimumAgeIsEighteen(t *testing.T) {
	if MinimumAgeYears != 18 {
		t.Fatalf("MinimumAgeYears = %d. India's DPDP Act requires verifiable parental "+
			"consent under 18 and this platform has no such flow, so it cannot "+
			"lawfully onboard a minor.", MinimumAgeYears)
	}
}

func TestAgeOnHandlesMonthAndDayBoundaries(t *testing.T) {
	born := time.Date(2000, 6, 15, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		now  time.Time
		want int
	}{
		{time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC), 25}, // day before birthday
		{time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC), 26}, // on birthday
		{time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC), 26},
		{time.Date(2026, 5, 30, 0, 0, 0, 0, time.UTC), 25}, // earlier month
		{time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), 26}, // later month
	}
	for _, tc := range cases {
		if got := AgeOn(born, tc.now); got != tc.want {
			t.Errorf("AgeOn(%s) = %d, want %d", tc.now.Format("2006-01-02"), got, tc.want)
		}
	}
}
