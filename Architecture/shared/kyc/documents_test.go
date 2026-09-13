package kyc

import (
	"errors"
	"testing"
)

// All numbers below are synthetic (zero serials, ZZ letter series).

func TestValidateFSSAILicence(t *testing.T) {
	ok := map[string]string{
		"10099999000000":     "10099999000000",
		" 10099999000000 ":   "10099999000000",
		"1009 9999 0000 00":  "10099999000000",
		"100 999 990 000 00": "10099999000000",
	}
	for in, want := range ok {
		got, err := ValidateFSSAILicence(in)
		if err != nil || got != want {
			t.Errorf("ValidateFSSAILicence(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{
		"", "1009999900000", "100999990000000", "1009999900000A", "10099999-00000",
		"100999990000٠", "ABCDEFGHIJKLMN",
	} {
		if _, err := ValidateFSSAILicence(in); !errors.Is(err, ErrInvalidFSSAILicence) {
			t.Errorf("ValidateFSSAILicence(%q) err = %v", in, err)
		}
	}
}

func TestValidateDrivingLicence(t *testing.T) {
	want := DrivingLicence{Normalized: "KA0120200000001", StateCode: "KA", RTOCode: "01", Year: "2020", Serial: "0000001"}
	for _, in := range []string{
		"KA0120200000001", "KA01 20200000001", "KA-01-2020-0000001", "ka01/2020/0000001", " ka01 2020 0000001 ",
	} {
		got, err := ValidateDrivingLicence(in)
		if err != nil || got != want {
			t.Errorf("ValidateDrivingLicence(%q) = %+v, %v", in, got, err)
		}
	}
	if got, err := ValidateDrivingLicence("OD0219990000009"); err != nil || got.Year != "1999" {
		t.Errorf("19xx year: %+v, %v", got, err)
	}
	for name, in := range map[string]string{
		"unknown state":  "ZZ0120200000001",
		"year 18xx":      "KA0118990000001",
		"year 21xx":      "KA0121000000001",
		"short serial":   "KA012020000001",
		"long serial":    "KA01202000000011",
		"letter serial":  "KA012020000000A",
		"empty":          "",
		"old layout":     "KA01 2020 000001",
		"underscore sep": "KA01_20200000001",
	} {
		if _, err := ValidateDrivingLicence(in); !errors.Is(err, ErrInvalidDrivingLicence) {
			t.Errorf("%s: ValidateDrivingLicence(%q) err = %v", name, in, err)
		}
	}
}

func TestValidateVehicleRegistration(t *testing.T) {
	state := map[string]VehicleRegistration{
		"MH12ZZ0000":    {Normalized: "MH12ZZ0000", Series: VehicleSeriesState, StateCode: "MH", RTOCode: "12", Letters: "ZZ", Number: "0000"},
		"mh 12 zz 0000": {Normalized: "MH12ZZ0000", Series: VehicleSeriesState, StateCode: "MH", RTOCode: "12", Letters: "ZZ", Number: "0000"},
		"MH-12-ZZ-0000": {Normalized: "MH12ZZ0000", Series: VehicleSeriesState, StateCode: "MH", RTOCode: "12", Letters: "ZZ", Number: "0000"},
		"DL3CZZ0000":    {Normalized: "DL3CZZ0000", Series: VehicleSeriesState, StateCode: "DL", RTOCode: "3", Letters: "CZZ", Number: "0000"},
		"KA.01.Z.0001":  {Normalized: "KA01Z0001", Series: VehicleSeriesState, StateCode: "KA", RTOCode: "01", Letters: "Z", Number: "0001"},
	}
	for in, want := range state {
		got, err := ValidateVehicleRegistration(in)
		if err != nil || got != want {
			t.Errorf("ValidateVehicleRegistration(%q) = %+v, %v", in, got, err)
		}
	}
	bh := map[string]VehicleRegistration{
		"22BH0000ZZ":   {Normalized: "22BH0000ZZ", Series: VehicleSeriesBH, Year: "22", Number: "0000", Letters: "ZZ"},
		"22 bh 0000 z": {Normalized: "22BH0000Z", Series: VehicleSeriesBH, Year: "22", Number: "0000", Letters: "Z"},
	}
	for in, want := range bh {
		got, err := ValidateVehicleRegistration(in)
		if err != nil || got != want {
			t.Errorf("ValidateVehicleRegistration(%q) = %+v, %v", in, got, err)
		}
	}
	for name, in := range map[string]string{
		"unknown state":     "ZZ12ZZ0000",
		"five-digit number": "MH12ZZ00000",
		"four letters":      "MH12ZZZZ0000",
		"no number":         "MH12ZZ",
		"BH no letters":     "22BH0000",
		"BH three letters":  "22BH0000ZZZ",
		"BH three-digit":    "22BH000ZZ",
		"BH one-digit year": "2BH0000ZZ",
		"empty":             "",
		"slash separator":   "MH12/ZZ/0000",
	} {
		if _, err := ValidateVehicleRegistration(in); !errors.Is(err, ErrInvalidVehicleRegistration) {
			t.Errorf("%s: ValidateVehicleRegistration(%q) err = %v", name, in, err)
		}
	}
}
