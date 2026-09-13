package riderkyc

import (
	"errors"
	"strings"
	"testing"

	"github.com/atpost/food-service/internal/onboarding"
	"github.com/atpost/shared/kyc"
	"github.com/google/uuid"
)

func TestClassifyVehicle(t *testing.T) {
	cases := map[string]VehicleClass{
		"BICYCLE": VehicleBicycle, "bicycle": VehicleBicycle, " Cycle ": VehicleBicycle,
		"MOTORCYCLE": VehicleMotorised, "bike": VehicleMotorised, "SCOOTER": VehicleMotorised, "ev_scooter": VehicleMotorised,
		"": VehicleUnknown, "truck": VehicleUnknown,
	}
	for in, want := range cases {
		if got := ClassifyVehicle(in); got != want {
			t.Errorf("ClassifyVehicle(%q) = %s, want %s", in, got, want)
		}
	}
}

func join(s []string) string { return strings.Join(s, ",") }

// The approval matrix. A bicycle skips DL and RC; an unknown vehicle skips
// nothing (fail closed); the selfie, Aadhaar check and payout account are
// required for everyone.
func TestMissingStepsMatrix(t *testing.T) {
	full := Facts{VehicleType: "MOTORCYCLE", HasAadhaarCheck: true, HasValidDrivingLicence: true, HasValidVehicleRC: true, HasApprovedSelfie: true, HasPayoutAccount: true}
	if got := MissingSteps(full); got == nil || len(got) != 0 {
		t.Fatalf("complete motorised facts: missing = %#v, want empty non-nil", got)
	}
	cases := []struct {
		name  string
		facts Facts
		want  string
	}{
		{"nothing", Facts{}, "vehicle,aadhaar_digilocker,driving_licence,vehicle_rc,selfie,payout_account"},
		{"bicycle with nothing", Facts{VehicleType: "BICYCLE"}, "aadhaar_digilocker,selfie,payout_account"},
		{"bicycle complete without DL or RC", Facts{VehicleType: "bicycle", HasAadhaarCheck: true, HasApprovedSelfie: true, HasPayoutAccount: true}, ""},
		{"motorcycle without DL or RC", Facts{VehicleType: "MOTORCYCLE", HasAadhaarCheck: true, HasApprovedSelfie: true, HasPayoutAccount: true}, "driving_licence,vehicle_rc"},
		{"unknown vehicle otherwise complete", Facts{VehicleType: "truck", HasAadhaarCheck: true, HasValidDrivingLicence: true, HasValidVehicleRC: true, HasApprovedSelfie: true, HasPayoutAccount: true}, "vehicle"},
		{"unknown vehicle still needs DL and RC", Facts{HasAadhaarCheck: true, HasApprovedSelfie: true, HasPayoutAccount: true}, "vehicle,driving_licence,vehicle_rc"},
		{"bicycle without selfie", Facts{VehicleType: "BICYCLE", HasAadhaarCheck: true, HasPayoutAccount: true}, "selfie"},
	}
	for _, tc := range cases {
		if got := join(MissingSteps(tc.facts)); got != tc.want {
			t.Errorf("%s: missing = %q, want %q", tc.name, got, tc.want)
		}
	}
	singles := []struct {
		step  string
		unset func(*Facts)
	}{
		{StepAadhaar, func(f *Facts) { f.HasAadhaarCheck = false }},
		{StepDrivingLicence, func(f *Facts) { f.HasValidDrivingLicence = false }},
		{StepVehicleRC, func(f *Facts) { f.HasValidVehicleRC = false }},
		{StepSelfie, func(f *Facts) { f.HasApprovedSelfie = false }},
		{StepPayoutAccount, func(f *Facts) { f.HasPayoutAccount = false }},
	}
	for _, s := range singles {
		f := full
		s.unset(&f)
		if got := join(MissingSteps(f)); got != s.step {
			t.Errorf("unset %s: missing = %q", s.step, got)
		}
	}
	err := error(&NotReadyError{Missing: []string{StepSelfie}})
	var nr *NotReadyError
	if !errors.As(err, &nr) || nr.Missing[0] != StepSelfie {
		t.Fatal("NotReadyError does not unwrap")
	}
}

func synthAadhaar(t *testing.T) string {
	t.Helper()
	for c := '0'; c <= '9'; c++ {
		if n := "34567890123" + string(c); kyc.LooksLikeAadhaar(n) {
			return n
		}
	}
	t.Fatal("no synthetic Aadhaar-shaped number")
	return ""
}

func codeOf(err error) string {
	var fe *onboarding.FieldError
	if errors.As(err, &fe) {
		return fe.Code
	}
	if err != nil {
		return "unexpected:" + err.Error()
	}
	return ""
}

func TestValidateDocument(t *testing.T) {
	media := uuid.NewString()
	aadhaar := synthAadhaar(t)
	cases := []struct {
		name string
		in   DocumentInput
		code string
		typ  string
		num  string
		kind LookupKind
	}{
		{"selfie", DocumentInput{DocumentType: "selfie", MediaID: media}, "", DocumentTypeSelfie, "", LookupNone},
		{"selfie with number", DocumentInput{DocumentType: "SELFIE", MediaID: media, DocumentNumber: "X1"}, CodeSelfieNumberNotAllowed, "", "", LookupNone},
		{"selfie without media", DocumentInput{DocumentType: "SELFIE"}, onboarding.CodeMediaRequired, "", "", LookupNone},
		{"aadhaar type", DocumentInput{DocumentType: "Aadhaar", DocumentNumber: "x"}, CodeAadhaarUseDigiLocker, "", "", LookupNone},
		{"driving licence", DocumentInput{DocumentType: "DRIVING_LICENCE", DocumentNumber: "ka-01-2020-0000001"}, "", DocumentTypeDrivingLicence, "KA0120200000001", LookupDrivingLicence},
		{"licence alias", DocumentInput{DocumentType: "driving_license", DocumentNumber: "KA0120200000001"}, "", DocumentTypeDrivingLicence, "KA0120200000001", LookupDrivingLicence},
		{"driving licence malformed", DocumentInput{DocumentType: "DRIVING_LICENCE", DocumentNumber: "KA01"}, CodeInvalidDrivingLicence, "", "", LookupNone},
		{"driving licence without number", DocumentInput{DocumentType: "DRIVING_LICENCE"}, CodeDocumentNumberRequired, "", "", LookupNone},
		{"vehicle rc", DocumentInput{DocumentType: "VEHICLE_RC", DocumentNumber: "KA 01 AB 1234"}, "", DocumentTypeVehicleRC, "KA01AB1234", LookupVehicleRegistration},
		{"aadhaar number in a DL field", DocumentInput{DocumentType: "DRIVING_LICENCE", DocumentNumber: aadhaar}, onboarding.CodeAadhaarNotAllowed, "", "", LookupNone},
		{"aadhaar number in another document", DocumentInput{DocumentType: "INSURANCE", DocumentNumber: aadhaar}, onboarding.CodeAadhaarNotAllowed, "", "", LookupNone},
		{"other document with number", DocumentInput{DocumentType: "insurance", DocumentNumber: " POL-778 "}, "", "INSURANCE", "POL-778", LookupNone},
		{"empty type", DocumentInput{}, CodeDocumentTypeRequired, "", "", LookupNone},
		{"type with punctuation", DocumentInput{DocumentType: "DL;DROP"}, CodeDocumentTypeInvalid, "", "", LookupNone},
		{"invalid media id", DocumentInput{DocumentType: "INSURANCE", MediaID: "not-a-uuid"}, CodeMediaIDInvalid, "", "", LookupNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := ValidateDocument(tc.in)
			if got := codeOf(err); got != tc.code {
				t.Fatalf("code = %q, want %q", got, tc.code)
			}
			if err != nil {
				if strings.Contains(err.Error(), aadhaar) || (tc.in.DocumentNumber != "" && strings.Contains(err.Error(), tc.in.DocumentNumber)) {
					t.Fatal("error text echoes the submitted number")
				}
				return
			}
			if v.DocumentType != tc.typ || v.Number != tc.num || v.LookupKind != tc.kind {
				t.Fatalf("validated = %s/%v, want %s/%s/%v", v.DocumentType, v.LookupKind, tc.typ, tc.num, tc.kind)
			}
			if strings.Contains(v.String(), "KA01") {
				t.Fatal("String leaks the number")
			}
		})
	}
}

func TestMaskName(t *testing.T) {
	for in, want := range map[string]string{"Mock Rider": "M*** R***", "  asha  ": "A***", "": "", "a b c d e f": "A*** B*** C*** D***"} {
		if got := MaskName(in); got != want {
			t.Errorf("MaskName(%q) = %q, want %q", in, got, want)
		}
	}
}
