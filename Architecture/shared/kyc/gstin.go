package kyc

import (
	"errors"
	"regexp"
)

// ErrInvalidGSTIN is the sentinel every GSTIN failure matches with
// errors.Is.
var ErrInvalidGSTIN = errors.New("INVALID_GSTIN")

// gstinPattern is the regular (non-OIDAR, non-TDS) GSTIN layout: two-digit
// state code, ten-character PAN, entity number 1-9 or A-Z, a literal Z, and
// a check character. The same shape commerce seller onboarding uses
// (commerce-service/internal/kyc/validator.go:63).
var gstinPattern = regexp.MustCompile(`^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z][1-9A-Z]Z[0-9A-Z]$`)

const gstinAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"

// GSTINPart names which rule a GSTIN failed.
type GSTINPart string

const (
	GSTINPartFormat    GSTINPart = "FORMAT"
	GSTINPartStateCode GSTINPart = "STATE_CODE"
	GSTINPartPAN       GSTINPart = "PAN"
	GSTINPartChecksum  GSTINPart = "CHECKSUM"
)

// GSTINError carries the failed rule. Reason is fixed text and never
// contains the GSTIN, its state code or its embedded PAN, so the error is
// safe to log and to return to a client.
type GSTINError struct {
	Part   GSTINPart
	Reason string
}

func (e *GSTINError) Error() string {
	return ErrInvalidGSTIN.Error() + ": " + string(e.Part) + ": " + e.Reason
}

// Is makes errors.Is(err, ErrInvalidGSTIN) true for every GSTINError.
func (e *GSTINError) Is(target error) bool { return target == ErrInvalidGSTIN }

// GSTIN is a validated, normalised GSTIN and the parts callers need.
type GSTIN struct {
	Normalized    string
	StateCode     string
	PAN           string
	PANHolderType PANHolderType
	EntityCode    byte
	CheckDigit    byte
}

// ValidateGSTIN trims and upper-cases s, then checks in order: layout
// (FORMAT), state code (STATE_CODE), the embedded PAN's holder type (PAN),
// and the mod-36 check character (CHECKSUM).
//
// It is a FORMAT and CHECKSUM check. It does not ask the GST portal whether
// the registration exists, is active, or belongs to the business presenting
// it; that needs the GSTN search API.
func ValidateGSTIN(s string) (GSTIN, error) {
	n, ok := normalizeASCII(s, "")
	if !ok || !gstinPattern.MatchString(n) {
		return GSTIN{}, &GSTINError{Part: GSTINPartFormat, Reason: "must be a 2-digit state code, a 10-character PAN, an entity character, Z and a check character"}
	}
	stateCode := n[:2]
	if !IsValidGSTStateCode(stateCode) {
		return GSTIN{}, &GSTINError{Part: GSTINPartStateCode, Reason: "state code is not an assigned GST state or territory code"}
	}
	holder := PANHolderType(n[5])
	if !holder.Valid() {
		return GSTIN{}, &GSTINError{Part: GSTINPartPAN, Reason: "embedded PAN has an unrecognised holder-type character"}
	}
	want, err := GSTINCheckDigit(n[:14])
	if err != nil {
		// Unreachable after the pattern match, which admits only 0-9 and A-Z.
		return GSTIN{}, err
	}
	if n[14] != want {
		return GSTIN{}, &GSTINError{Part: GSTINPartChecksum, Reason: "check character does not match"}
	}
	return GSTIN{
		Normalized:    n,
		StateCode:     stateCode,
		PAN:           n[2:12],
		PANHolderType: holder,
		EntityCode:    n[12],
		CheckDigit:    n[14],
	}, nil
}

// GSTINCheckDigit computes the fifteenth character for the first fourteen
// characters of a GSTIN, which must be upper-case 0-9 or A-Z.
//
// Each character maps to 0..35. Walking left to right, the character at an
// even index is weighted 1 and at an odd index 2. Each product p contributes
// p/36 + p%36 to the sum, and the check value is (36 - sum%36) % 36.
func GSTINCheckDigit(first14 string) (byte, error) {
	if len(first14) != 14 {
		return 0, &GSTINError{Part: GSTINPartFormat, Reason: "check character needs exactly 14 characters"}
	}
	sum := 0
	for i := 0; i < 14; i++ {
		v := indexByte(gstinAlphabet, first14[i])
		if v < 0 {
			return 0, &GSTINError{Part: GSTINPartFormat, Reason: "characters must be 0-9 or upper-case A-Z"}
		}
		factor := 1
		if i%2 == 1 {
			factor = 2
		}
		p := v * factor
		sum += p/36 + p%36
	}
	return gstinAlphabet[(36-sum%36)%36], nil
}
