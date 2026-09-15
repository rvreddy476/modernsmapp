package service

import (
	"strings"
	"testing"
	"time"
)

// The profile-write DOB rules. The Mirror_ tests pin the SAME boundary cases,
// with the same reference instant, as auth-service's
// internal/service/eligibility_test.go: registration and profile edits must
// agree on who is 18. If one side moves, the other must move with it.

var refNow = time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

func fieldCode(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	fe, ok := IsFieldError(err)
	if !ok {
		t.Fatalf("got a non-validation error %v, want a *FieldError", err)
	}
	return fe.Code
}

func checkWireDOB(raw string, now time.Time) error {
	born, err := ParseProfileDOB(raw)
	if err != nil {
		return err
	}
	return CheckProfileDOB(born, now)
}

func TestMirror_MalformedOrImpossibleDOBIsRejected(t *testing.T) {
	cases := map[string]string{
		"not-a-date": CodeDOBInvalid,
		"11/08/1990": CodeDOBInvalid,
		"1990-13-01": CodeDOBInvalid,
		"1990-02-30": CodeDOBInvalid,
		"2030-01-01": CodeDOBInFuture,
		"9999-01-01": CodeDOBInFuture,
	}
	for dob, want := range cases {
		if got := fieldCode(t, checkWireDOB(dob, refNow)); got != want {
			t.Errorf("dob %q: got %q, want %q", dob, got, want)
		}
	}
}

func TestMirror_AgeBoundaryIsExactToTheDay(t *testing.T) {
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
		{"born on a leap day, now 18", "2008-02-29", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fieldCode(t, checkWireDOB(tc.dob, refNow))
			if tc.allowed && got != "" {
				t.Fatalf("dob %s should be allowed, got %s", tc.dob, got)
			}
			if !tc.allowed && got != CodeDOBUnderMinimumAge {
				t.Fatalf("dob %s should be refused as under age, got %q", tc.dob, got)
			}
		})
	}
}

func TestMirror_ThirteenToSeventeenAreRefused(t *testing.T) {
	for _, dob := range []string{"2013-08-11", "2010-01-01", "2009-08-12"} {
		if got := fieldCode(t, checkWireDOB(dob, refNow)); got != CodeDOBUnderMinimumAge {
			t.Errorf("dob %s (13–17): got %q, want %s", dob, got, CodeDOBUnderMinimumAge)
		}
	}
}

func TestMirror_MinimumAgeIsEighteen(t *testing.T) {
	if MinimumAgeYears != 18 {
		t.Fatalf("MinimumAgeYears = %d; auth-service registration enforces 18", MinimumAgeYears)
	}
}

func TestMirror_AgeOnHandlesMonthAndDayBoundaries(t *testing.T) {
	born := time.Date(2000, 6, 15, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		now  time.Time
		want int
	}{
		{time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC), 25},
		{time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC), 26},
		{time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC), 26},
		{time.Date(2026, 5, 30, 0, 0, 0, 0, time.UTC), 25},
		{time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), 26},
	}
	for _, tc := range cases {
		if got := AgeOn(born, tc.now); got != tc.want {
			t.Errorf("AgeOn(%s) = %d, want %d", tc.now.Format("2006-01-02"), got, tc.want)
		}
	}
}

// "Today" is the calendar date in Asia/Kolkata, not UTC and not the server's
// local zone. Each case sits on an instant where the two disagree.
func TestAgeIsComputedInAsiaKolkata(t *testing.T) {
	cases := []struct {
		name    string
		now     time.Time
		dob     string
		allowed bool
	}{
		{"00:30 IST on the 18th birthday, still the day before in UTC",
			time.Date(2026, 8, 10, 19, 0, 0, 0, time.UTC), "2008-08-11", true},
		{"23:59:59 IST the day before the 18th birthday",
			time.Date(2026, 8, 11, 18, 29, 59, 0, time.UTC), "2008-08-12", false},
		{"00:00 IST on the 18th birthday",
			time.Date(2026, 8, 11, 18, 30, 0, 0, time.UTC), "2008-08-12", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fieldCode(t, checkWireDOB(tc.dob, tc.now))
			if tc.allowed != (got == "") {
				t.Fatalf("allowed=%v, got code %q", tc.allowed, got)
			}
		})
	}
}

func TestParseProfileDOBFormats(t *testing.T) {
	for _, ok := range []string{"1990-03-17", "1990-03-17T00:00:00Z"} {
		born, err := ParseProfileDOB(ok)
		if err != nil {
			t.Errorf("%q refused: %v", ok, err)
			continue
		}
		if born.Format("2006-01-02") != "1990-03-17" || born.Location() != time.UTC {
			t.Errorf("%q parsed to %v, want 1990-03-17 UTC midnight", ok, born)
		}
	}
	refused := []string{
		"2025-02-30", "17/03/1990", "1990-3-17", "19900317", "", " 1990-03-17", "1990-03-17 ",
		"1990-03-17T00:00:00+05:30", // offset: which day depends on the reader
		"1990-03-16T18:30:00Z",      // IST midnight on the 17th, rendered in UTC
		"1990-03-17T00:00:00.000Z",
		"1990-03-17T12:00:00Z",
	}
	for _, raw := range refused {
		if got := fieldCode(t, func() error { _, err := ParseProfileDOB(raw); return err }()); got != CodeDOBInvalid {
			t.Errorf("%q: got %q, want %s", raw, got, CodeDOBInvalid)
		}
	}
}

func TestDOBRangeFloorIs1900(t *testing.T) {
	if got := fieldCode(t, checkWireDOB("1899-12-31", refNow)); got != CodeDOBTooEarly {
		t.Errorf("1899-12-31: got %q, want %s", got, CodeDOBTooEarly)
	}
	if got := fieldCode(t, checkWireDOB("1900-01-01", refNow)); got != "" {
		t.Errorf("1900-01-01: got %q, want accepted", got)
	}
}

func TestRegistrationDriftIsAtMostOneYear(t *testing.T) {
	registered := time.Date(1990, 3, 17, 0, 0, 0, 0, time.UTC)
	cases := map[string]bool{
		"1990-03-17": true,
		"1990-06-17": true, // a three-month correction
		"1991-03-17": true, // exactly a year later
		"1989-03-17": true, // exactly a year earlier
		"1991-03-18": false,
		"1989-03-16": false,
		"2008-01-01": false,
	}
	for dob, allowed := range cases {
		born, _ := time.Parse("2006-01-02", dob)
		got := fieldCode(t, CheckRegistrationDrift(born, registered))
		if allowed && got != "" {
			t.Errorf("%s: refused with %s, want accepted", dob, got)
		}
		if !allowed && got != CodeDOBMismatchRegistration {
			t.Errorf("%s: got %q, want %s", dob, got, CodeDOBMismatchRegistration)
		}
	}
}

func TestNormalizeFirstName(t *testing.T) {
	accepted := map[string]string{
		"Asha":                  "Asha",
		"  Asha K  ":            "Asha K",
		"Mary-Jane":             "Mary-Jane",
		"O'Brien":               "O'Brien",
		"O’Brien":               "O’Brien",
		"J. R.":                 "J. R.",
		"Zoë":                   "Zoë",
		"राम":                   "राम", // Devanagari with a combining vowel sign
		"அருண்":                 "அருண்",
		strings.Repeat("a", 50): strings.Repeat("a", 50),
	}
	for in, want := range accepted {
		got, err := NormalizeFirstName(in)
		if err != nil || got != want {
			t.Errorf("%q: got (%q, %v), want %q", in, got, err, want)
		}
	}
	refused := []string{
		"", "   ", "\t", "Asha\x00", "As\tha", "\nAsha", "Asha\x7f",
		"Asha2", "Asha!", "-", ".",
		"Asha\xe2\x80\x8b", // zero-width space: invisible, not a joiner
		strings.Repeat("a", 51),
	}
	for _, in := range refused {
		_, err := NormalizeFirstName(in)
		if got := fieldCode(t, err); got != CodeFirstNameInvalid {
			t.Errorf("%q: got %q, want %s", in, got, CodeFirstNameInvalid)
		}
	}
}
