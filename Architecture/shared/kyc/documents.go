package kyc

import (
	"errors"
	"regexp"
)

// Sentinels for the document-number checks. They carry no detail on
// purpose: nothing about the submitted value reaches the error text.
var (
	ErrInvalidFSSAILicence        = errors.New("INVALID_FSSAI_LICENCE")
	ErrInvalidDrivingLicence      = errors.New("INVALID_DRIVING_LICENCE")
	ErrInvalidVehicleRegistration = errors.New("INVALID_VEHICLE_REGISTRATION")
)

const fssaiLicenceDigits = 14

// ValidateFSSAILicence trims s, drops inner spaces, and returns it if what
// remains is exactly fourteen ASCII digits.
//
// FORMAT ONLY. The digits encode a licence kind, state and year, but that
// encoding is not checked here, and nothing confirms with FoSCoS that the
// licence exists, is current, or covers the premises.
func ValidateFSSAILicence(s string) (string, error) {
	n, ok := normalizeASCII(s, " ")
	if !ok {
		return "", ErrInvalidFSSAILicence
	}
	if len(n) != fssaiLicenceDigits {
		return "", ErrInvalidFSSAILicence
	}
	for i := 0; i < len(n); i++ {
		if !isDigit(n[i]) {
			return "", ErrInvalidFSSAILicence
		}
	}
	return n, nil
}

// DrivingLicence is a validated, normalised driving-licence number.
type DrivingLicence struct {
	// Normalized is the compact 15-character form, e.g. KA0120200000001.
	Normalized string
	StateCode  string
	RTOCode    string
	Year       string
	Serial     string
}

// dlPattern is the 15-character Sarathi layout: state letters, two-digit
// RTO, four-digit year of issue (19xx or 20xx), seven-digit serial.
var dlPattern = regexp.MustCompile(`^([A-Z]{2})([0-9]{2})((?:19|20)[0-9]{2})([0-9]{7})$`)

// ValidateDrivingLicence upper-cases s, removes spaces, hyphens and slashes,
// and checks the Sarathi layout and state prefix.
//
// FORMAT ONLY. Older pre-Sarathi layouts are refused. Nothing here asks
// Sarathi or DigiLocker whether the licence exists, is valid, or covers the
// vehicle class the rider will use.
func ValidateDrivingLicence(s string) (DrivingLicence, error) {
	n, ok := normalizeASCII(s, " -/")
	if !ok {
		return DrivingLicence{}, ErrInvalidDrivingLicence
	}
	m := dlPattern.FindStringSubmatch(n)
	if m == nil {
		return DrivingLicence{}, ErrInvalidDrivingLicence
	}
	if dlState := m[1]; !rtoStateCodes[dlState] {
		return DrivingLicence{}, ErrInvalidDrivingLicence
	}
	return DrivingLicence{Normalized: n, StateCode: m[1], RTOCode: m[2], Year: m[3], Serial: m[4]}, nil
}

// VehicleSeries distinguishes a state-series registration from a Bharat
// (BH) series one.
type VehicleSeries string

const (
	VehicleSeriesState VehicleSeries = "STATE"
	VehicleSeriesBH    VehicleSeries = "BH"
)

// VehicleRegistration is a validated, normalised registration mark.
type VehicleRegistration struct {
	Normalized string
	Series     VehicleSeries
	// StateCode and RTOCode are set for the STATE series.
	StateCode string
	RTOCode   string
	Letters   string
	Number    string
	// Year is the two-digit year of registration, BH series only.
	Year string
}

var (
	vehicleStatePattern = regexp.MustCompile(`^([A-Z]{2})([0-9]{1,2})([A-Z]{0,3})([0-9]{1,4})$`)
	vehicleBHPattern    = regexp.MustCompile(`^([0-9]{2})BH([0-9]{4})([A-Z]{1,2})$`)
)

// ValidateVehicleRegistration upper-cases s, removes spaces, hyphens and
// dots, and accepts either a state-series mark (MH12ZZ0000, DL3CZZ0000) or a
// BH-series mark (22BH0000ZZ).
//
// FORMAT ONLY. It does not ask VAHAN whether the vehicle exists, whom it is
// registered to, or whether its fitness, insurance and permit are current.
func ValidateVehicleRegistration(s string) (VehicleRegistration, error) {
	n, ok := normalizeASCII(s, " -.")
	if !ok {
		return VehicleRegistration{}, ErrInvalidVehicleRegistration
	}
	if m := vehicleBHPattern.FindStringSubmatch(n); m != nil {
		return VehicleRegistration{Normalized: n, Series: VehicleSeriesBH, Year: m[1], Number: m[2], Letters: m[3]}, nil
	}
	m := vehicleStatePattern.FindStringSubmatch(n)
	if m == nil {
		return VehicleRegistration{}, ErrInvalidVehicleRegistration
	}
	if vehicleState := m[1]; !rtoStateCodes[vehicleState] {
		return VehicleRegistration{}, ErrInvalidVehicleRegistration
	}
	return VehicleRegistration{
		Normalized: n,
		Series:     VehicleSeriesState,
		StateCode:  m[1],
		RTOCode:    m[2],
		Letters:    m[3],
		Number:     m[4],
	}, nil
}
