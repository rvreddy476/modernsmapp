package service

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Module 3 M3-P0-3 / SR-6 — registration eligibility: age and consent.
//
// WHAT WAS WRONG
//
// The age gate was `minimumAgeYears = 13`, and it was skipped entirely when no
// date of birth was supplied:
//
//	if strings.TrimSpace(dob) == "" {
//		return nil   // "the registration form may not collect one"
//	}
//
// Since `dob` was an optional field on RegisterRequest, any client could omit
// it and bypass the check completely. The gate was advisory.
//
// There was no consent capture at all — no terms acceptance, no privacy-policy
// version, nothing recording that the user agreed to anything.
//
// WHY 18 AND NOT 13
//
// India's DPDP Act requires verifiable parental consent to process the
// personal data of anyone under 18. This platform has no parental-consent
// flow, so it cannot lawfully onboard a minor. 13 with an optional DOB was not
// a lower standard, it was no standard: it admitted minors the platform has no
// mechanism to serve, and recorded nothing to show otherwise.
//
// 18-only is a launch degradation, and an honest one: it is a real gate the
// platform can actually enforce, rather than a permissive one it cannot.

// MinimumAgeYears is the enforced floor for self-service registration.
const MinimumAgeYears = 18

// CurrentTermsVersion is the terms/privacy version a registration must accept.
// Bumping it does NOT invalidate existing accounts — it changes what new
// registrations record, so a later audit can tell which text each user saw.
const CurrentTermsVersion = "2026-08-01"

var (
	// ErrDOBRequired — an absent date of birth can no longer skip the gate.
	ErrDOBRequired = errors.New(
		"date of birth is required: the age check cannot be skipped by omitting it")
	// ErrDOBMalformed — an unparseable value is a rejection, never a pass.
	ErrDOBMalformed = errors.New("date of birth must be in YYYY-MM-DD format")
	// ErrDOBInFuture — a future date would otherwise compute a negative age.
	ErrDOBInFuture = errors.New("date of birth cannot be in the future")
	// ErrUnderage — below MinimumAgeYears.
	ErrUnderage = fmt.Errorf(
		"you must be at least %d years old to create an account. This platform has no "+
			"verifiable parental-consent flow, so it cannot lawfully process the data "+
			"of a user under 18", MinimumAgeYears)
	// ErrConsentRequired — registration must record an explicit acceptance.
	ErrConsentRequired = errors.New(
		"you must accept the terms of service and privacy policy to create an account")
	// ErrConsentVersionMismatch — the client accepted a different text than the
	// one currently in force.
	ErrConsentVersionMismatch = fmt.Errorf(
		"the terms you accepted are out of date; the current version is %s", CurrentTermsVersion)
)

// RegistrationConsent is what a registration must supply and what is recorded.
type RegistrationConsent struct {
	// Accepted must be explicitly true. A default-false bool means a client
	// that forgets the field is refused, which is the correct direction: a
	// consent that defaults to granted is not consent.
	Accepted bool
	// Version is the terms version the user was shown.
	Version string
}

// ParseDOB validates a date of birth and returns it.
//
// Every failure path is a rejection. There is no branch that lets a missing or
// unparseable value through, because that is exactly how the old gate was
// bypassed.
func ParseDOB(dob string, now time.Time) (time.Time, error) {
	s := strings.TrimSpace(dob)
	if s == "" {
		return time.Time{}, ErrDOBRequired
	}
	born, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, ErrDOBMalformed
	}
	if born.After(now) {
		return time.Time{}, ErrDOBInFuture
	}
	return born, nil
}

// AgeOn returns the completed years between born and now.
//
// Written with an explicit month/day comparison rather than dividing a
// duration by 365.25: the approximation is wrong by a day around leap years,
// and on a legal boundary a wrong day means admitting someone the platform is
// not allowed to onboard.
func AgeOn(born, now time.Time) int {
	years := now.Year() - born.Year()
	if now.Month() < born.Month() ||
		(now.Month() == born.Month() && now.Day() < born.Day()) {
		years--
	}
	return years
}

// CheckRegistrationEligibility enforces the age floor and consent capture.
func CheckRegistrationEligibility(dob string, consent RegistrationConsent, now time.Time) error {
	born, err := ParseDOB(dob, now)
	if err != nil {
		return err
	}
	if AgeOn(born, now) < MinimumAgeYears {
		return ErrUnderage
	}
	if !consent.Accepted {
		return ErrConsentRequired
	}
	if strings.TrimSpace(consent.Version) != CurrentTermsVersion {
		return ErrConsentVersionMismatch
	}
	return nil
}

// ErrEmailNotVerified is returned when a pending account attempts to log in.
//
// LB-5: registration creates the account pending and issues no session, so
// this is the state a user is in between signing up and clicking the code.
// It is a distinct error so the handler can tell the client to resend the
// verification email rather than showing "wrong password".
var ErrEmailNotVerified = errors.New(
	"your email address has not been verified yet — check your inbox for the code, " +
		"or request a new one")
