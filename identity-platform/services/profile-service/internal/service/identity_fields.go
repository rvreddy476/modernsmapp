package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/google/uuid"
)

// Identity fields on a profile write: date of birth and first name.
//
// WHAT WAS WRONG
//
// PUT /v1/profiles/me copied `dob` and `first_name` straight into
// profile.profiles. A user could set an under-18 date, a future date, or
// clear it, and the store wrote `dob = $11` unconditionally, so a request
// that simply omitted the field (or a handle change, which never carries it)
// set it to NULL. The internal identity read falls back to this column when
// an account has no registration consent row, so an unvalidated value here is
// an unvalidated value in dating's 18+ gate.
//
// Every rule below returns a *FieldError. Nothing downgrades a failure to a
// silent skip: that is exactly how the registration gate used to be bypassed.
// The one skip is a value sent back unchanged (DOB, or first name under the
// validator's trim): it is not written, so there is nothing to validate.

// Stable field error codes. Clients key on these; never rename one.
const (
	CodeDOBInvalid              = "DOB_INVALID"
	CodeDOBRequired             = "DOB_REQUIRED"
	CodeDOBInFuture             = "DOB_IN_FUTURE"
	CodeDOBTooEarly             = "DOB_TOO_EARLY"
	CodeDOBUnderMinimumAge      = "DOB_UNDER_MINIMUM_AGE"
	CodeDOBMismatchRegistration = "DOB_MISMATCH_REGISTRATION"
	CodeFirstNameInvalid        = "FIRST_NAME_INVALID"
)

// MinimumAgeYears mirrors auth-service's registration floor
// (auth-service/internal/service/eligibility.go). That package is internal to
// another module and cannot be imported under GOWORK=off, so the rule is
// copied, and identity_fields_test.go pins the same boundary cases as
// auth-service's eligibility_test.go.
const MinimumAgeYears = 18

// maxRegistrationDriftYears is how far a profile DOB may move from the DOB
// declared at registration. Enough for a typo in the day, month or year;
// not enough to walk across the 18 boundary from a verified adult date.
const maxRegistrationDriftYears = 1

const maxFirstNameRunes = 50

// earliestDOB is a fixed floor rather than "age above 120". A fixed date is
// deterministic (a stored value never becomes invalid on a later re-save just
// because time passed), it is looser than the Android picker (which stops at
// 120 years back, so no client-produced date is refused), and anything before
// it is older than any living person.
var earliestDOB = time.Date(1900, time.January, 1, 0, 0, 0, 0, time.UTC)

// indiaTime is Asia/Kolkata. A fixed +05:30 zone rather than
// time.LoadLocation: India has not observed DST since 1945, so the offset is
// exact, and the runtime image (alpine, no tzdata) cannot fail to load it.
var indiaTime = time.FixedZone("Asia/Kolkata", 5*60*60+30*60)

// FieldError is a validation failure on one request field. The HTTP layer
// renders it as 422 with Code as the envelope's error code.
type FieldError struct {
	Field   string
	Code    string
	Message string
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Code }

func dobError(code, message string) error {
	return &FieldError{Field: "dob", Code: code, Message: message}
}

// IsFieldError reports whether err is a validation failure and returns it.
func IsFieldError(err error) (*FieldError, bool) {
	var fe *FieldError
	if errors.As(err, &fe) {
		return fe, true
	}
	return nil, false
}

// ParseProfileDOB parses a date of birth as sent on a profile write.
//
// Two spellings of the same calendar date are accepted:
//
//	YYYY-MM-DD
//	YYYY-MM-DDT00:00:00Z   (UTC midnight — what GET /v1/profiles/me returns
//	                        and what the Android edit screen sends back)
//
// Anything else is refused, including any other time of day or offset. A
// value like 1990-03-16T18:30:00Z is IST midnight on the 17th rendered in
// UTC: which day it "means" depends on the reader's zone, so it is not a date.
func ParseProfileDOB(raw string) (time.Time, error) {
	datePart := raw
	switch {
	case len(raw) == len("2006-01-02"):
	case len(raw) == len("2006-01-02T00:00:00Z") && strings.HasSuffix(raw, "T00:00:00Z"):
		datePart = raw[:len("2006-01-02")]
	default:
		return time.Time{}, dobError(CodeDOBInvalid, "date of birth must be a calendar date in YYYY-MM-DD format")
	}
	born, err := time.Parse("2006-01-02", datePart)
	if err != nil || born.Format("2006-01-02") != datePart {
		// time.Parse refuses 2025-02-30 and 1990-13-01; the round-trip check
		// guards against any lenient spelling it might accept.
		return time.Time{}, dobError(CodeDOBInvalid, "date of birth must be a real calendar date in YYYY-MM-DD format")
	}
	return born, nil
}

// civilDate strips a time to its calendar date at UTC midnight, read in the
// value's own location. DATE columns scan as UTC midnight already.
func civilDate(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// todayInIndia is the calendar date in Asia/Kolkata at instant now.
func todayInIndia(now time.Time) time.Time {
	return civilDate(now.In(indiaTime))
}

// AgeOn returns the completed years between born and now. Copied exactly from
// auth-service's eligibility.go: an explicit month/day comparison, because a
// duration divided by 365.25 is a day off around leap years, and on a legal
// boundary a wrong day admits someone the platform may not onboard.
func AgeOn(born, now time.Time) int {
	years := now.Year() - born.Year()
	if now.Month() < born.Month() ||
		(now.Month() == born.Month() && now.Day() < born.Day()) {
		years--
	}
	return years
}

// CheckProfileDOB enforces the range and the age floor, with "today" taken in
// Asia/Kolkata: a birthday not yet reached there this year does not count.
func CheckProfileDOB(born, now time.Time) error {
	born = civilDate(born)
	today := todayInIndia(now)
	if born.Before(earliestDOB) {
		return dobError(CodeDOBTooEarly, "date of birth cannot be before 1900-01-01")
	}
	if born.After(today) {
		return dobError(CodeDOBInFuture, "date of birth cannot be in the future")
	}
	if AgeOn(born, today) < MinimumAgeYears {
		return dobError(CodeDOBUnderMinimumAge,
			fmt.Sprintf("you must be at least %d years old", MinimumAgeYears))
	}
	return nil
}

// CheckRegistrationDrift refuses a DOB more than maxRegistrationDriftYears
// away, in either direction, from the DOB declared at registration. Measured
// from the registration value, never from the current profile value, so a
// run of small edits cannot creep across the boundary.
func CheckRegistrationDrift(born, registered time.Time) error {
	born, registered = civilDate(born), civilDate(registered)
	earliest := registered.AddDate(-maxRegistrationDriftYears, 0, 0)
	latest := registered.AddDate(maxRegistrationDriftYears, 0, 0)
	if born.Before(earliest) || born.After(latest) {
		return dobError(CodeDOBMismatchRegistration,
			"date of birth cannot differ by more than a year from the one given at registration")
	}
	return nil
}

// NormalizeFirstName trims and validates a first name.
//
// Allowed: letters in any script with their combining marks, spaces, hyphen,
// apostrophe (ASCII and the typographic ’ that phone keyboards insert),
// period, and ZWNJ/ZWJ (needed to type some Indic conjuncts). At least one
// letter; 1–50 characters after trimming. Control characters are refused
// before trimming, so a tab or newline is never silently stripped into a pass.
func NormalizeFirstName(raw string) (string, error) {
	invalid := func(message string) (string, error) {
		return "", &FieldError{Field: "first_name", Code: CodeFirstNameInvalid, Message: message}
	}
	if !utf8.ValidString(raw) {
		return invalid("first name must be valid text")
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return invalid("first name cannot contain control characters")
		}
	}
	name := strings.TrimSpace(raw)
	if name == "" {
		return invalid("first name cannot be empty")
	}
	if utf8.RuneCountInString(name) > maxFirstNameRunes {
		return invalid(fmt.Sprintf("first name must be %d characters or fewer", maxFirstNameRunes))
	}
	letters := 0
	for _, r := range name {
		switch {
		case unicode.IsLetter(r):
			letters++
		case unicode.IsMark(r), r == ' ', r == '-', r == '\'', r == '.',
			r == 0x2019, // right single quotation mark
			r == 0x200C, // zero-width non-joiner
			r == 0x200D: // zero-width joiner
		default:
			return invalid("first name may contain letters, spaces, hyphens, apostrophes and periods only")
		}
	}
	if letters == 0 {
		return invalid("first name must contain a letter")
	}
	return name, nil
}

// firstNameKey is a first name under the validator's trim. NormalizeFirstName
// refuses control characters before it trims, so on anything it accepts the
// trim removes only non-control white space; this removes exactly that, so a
// control-character-padded "\nAsha" never matches a stored "Asha".
func firstNameKey(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) && !unicode.IsControl(r) })
}

// sameFirstName reports whether a submitted first name is the stored one.
// Both sides are trimmed: a stored value that predates validation may carry
// padding the form does not send back, and white space at the ends is not
// a change of name (the validator discards it on a write anyway). An empty
// result never matches, so "" keeps its own exemption below.
func sameFirstName(submitted string, stored *string) bool {
	if stored == nil {
		return false
	}
	key := firstNameKey(submitted)
	return key != "" && key == firstNameKey(*stored)
}

type dobChange struct {
	old *time.Time
	new time.Time
}

// checkIdentityFields validates first_name and dob on a profile write and
// normalises params in place. A nil field is "unchanged" all the way down: the
// store's UPDATE keeps the stored value for a nil first_name or dob.
//
// Returns the DOB change to audit, or nil when the DOB is not being changed.
func (s *Service) checkIdentityFields(ctx context.Context, userID uuid.UUID, params *store.UpdateProfileParams) (*dobChange, error) {
	if params.DoB == nil && params.FirstName == nil {
		return nil, nil
	}
	current, err := s.profiles.GetProfile(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("load profile for identity checks: %w", err)
	}

	if params.FirstName != nil {
		if current != nil && sameFirstName(*params.FirstName, current.FirstName) {
			// The stored name sent back unchanged: Android resends the whole
			// form on every save. Like an unchanged DOB, not a first-name write,
			// so nothing to validate, and an account whose name predates the
			// rule (too long, digits) can still edit its bio. The stored value
			// is kept byte for byte. Logged without the name.
			if _, err := NormalizeFirstName(*current.FirstName); err != nil {
				s.log.Info("profile first name outside the current rule, unchanged and kept",
					"user_id", userID, "reason", "legacy_first_name_kept")
			}
			params.FirstName = nil
		} else {
			name, err := NormalizeFirstName(*params.FirstName)
			switch {
			case err == nil:
				params.FirstName = &name
			case strings.Trim(*params.FirstName, " ") == "" && current != nil &&
				(current.FirstName == nil || strings.TrimSpace(*current.FirstName) == ""):
				// The Android edit form always sends first_name, as "" for an
				// account that never had one (OAuth sign-ups, older accounts).
				// Leaving an empty name empty is not a change, so it is not a
				// refusal; clearing a name that exists still is.
				params.FirstName = nil
			default:
				return nil, err
			}
		}
	}

	if params.DoB == nil {
		return nil, nil
	}
	born := civilDate(*params.DoB)
	if current != nil && current.DoB != nil && civilDate(*current.DoB).Equal(born) {
		// The same date sent back unchanged: Android resends the whole form
		// on every save. Not a DOB write, so nothing to validate or audit, and
		// an account with an older out-of-policy value can still edit its bio.
		params.DoB = nil
		return nil, nil
	}
	if err := CheckProfileDOB(born, s.now()); err != nil {
		return nil, err
	}
	registered, err := s.profiles.GetRegistrationDOB(ctx, userID)
	if err != nil {
		// Fail closed: an unreadable registration record is not "no record".
		return nil, fmt.Errorf("load registration date of birth: %w", err)
	}
	if registered != nil {
		if err := CheckRegistrationDrift(born, *registered); err != nil {
			return nil, err
		}
	}
	params.DoB = &born
	change := &dobChange{new: born}
	if current != nil {
		change.old = current.DoB
	}
	return change, nil
}
