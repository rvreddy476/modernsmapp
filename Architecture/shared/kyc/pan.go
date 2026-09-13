package kyc

import (
	"errors"
	"regexp"
)

// ErrInvalidPAN is the sentinel every PAN failure matches with errors.Is.
var ErrInvalidPAN = errors.New("INVALID_PAN")

// panPattern is the Income Tax Department layout: five letters, four
// digits, one letter. The fourth letter is the holder type.
var panPattern = regexp.MustCompile(`^[A-Z]{5}[0-9]{4}[A-Z]$`)

// PANPart names which rule a PAN failed.
type PANPart string

const (
	PANPartFormat     PANPart = "FORMAT"
	PANPartHolderType PANPart = "HOLDER_TYPE"
)

// PANError carries the failed rule. Reason is fixed text: it never contains
// the value that was checked, so the error is safe to log.
type PANError struct {
	Part   PANPart
	Reason string
}

func (e *PANError) Error() string {
	return ErrInvalidPAN.Error() + ": " + string(e.Part) + ": " + e.Reason
}

// Is makes errors.Is(err, ErrInvalidPAN) true for every PANError.
func (e *PANError) Is(target error) bool { return target == ErrInvalidPAN }

// PANHolderType is the fourth character of a PAN.
type PANHolderType byte

const (
	PANHolderIndividual                PANHolderType = 'P'
	PANHolderCompany                   PANHolderType = 'C'
	PANHolderHUF                       PANHolderType = 'H'
	PANHolderFirm                      PANHolderType = 'F'
	PANHolderAOP                       PANHolderType = 'A'
	PANHolderTrust                     PANHolderType = 'T'
	PANHolderBOI                       PANHolderType = 'B'
	PANHolderLocalAuthority            PANHolderType = 'L'
	PANHolderArtificialJuridicalPerson PANHolderType = 'J'
	PANHolderGovernment                PANHolderType = 'G'
)

// Valid reports whether h is one of the ten holder types above.
func (h PANHolderType) Valid() bool {
	switch h {
	case PANHolderIndividual, PANHolderCompany, PANHolderHUF, PANHolderFirm,
		PANHolderAOP, PANHolderTrust, PANHolderBOI, PANHolderLocalAuthority,
		PANHolderArtificialJuridicalPerson, PANHolderGovernment:
		return true
	}
	return false
}

func (h PANHolderType) String() string {
	switch h {
	case PANHolderIndividual:
		return "INDIVIDUAL"
	case PANHolderCompany:
		return "COMPANY"
	case PANHolderHUF:
		return "HUF"
	case PANHolderFirm:
		return "FIRM"
	case PANHolderAOP:
		return "AOP"
	case PANHolderTrust:
		return "TRUST"
	case PANHolderBOI:
		return "BOI"
	case PANHolderLocalAuthority:
		return "LOCAL_AUTHORITY"
	case PANHolderArtificialJuridicalPerson:
		return "ARTIFICIAL_JURIDICAL_PERSON"
	case PANHolderGovernment:
		return "GOVERNMENT"
	}
	return "UNKNOWN"
}

// PAN is a validated, normalised PAN.
type PAN struct {
	Normalized string
	HolderType PANHolderType
}

// ValidatePAN trims and upper-cases s and checks the layout and the holder
// type. It is a FORMAT check: it does not ask the Income Tax Department
// whether the PAN exists or whom it belongs to.
func ValidatePAN(s string) (PAN, error) {
	n, ok := normalizeASCII(s, "")
	if !ok || !panPattern.MatchString(n) {
		return PAN{}, &PANError{Part: PANPartFormat, Reason: "must be five letters, four digits and a letter"}
	}
	ht := PANHolderType(n[3])
	if !ht.Valid() {
		return PAN{}, &PANError{Part: PANPartHolderType, Reason: "fourth character is not a recognised holder type"}
	}
	return PAN{Normalized: n, HolderType: ht}, nil
}
