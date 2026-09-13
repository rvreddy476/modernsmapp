// Package riderkyc holds the pure rules for delivery-partner verification
// (Wave 1 B4): which steps a partner still owes before approval, how the
// vehicle type decides whether a driving licence and RC are needed, and how a
// manually submitted document is validated. Nothing here touches the
// database, a key or the network.
//
// Error text never echoes a submitted number.
package riderkyc

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/atpost/food-service/internal/onboarding"
	"github.com/atpost/shared/kyc"
	"github.com/google/uuid"
)

// Step names, in the order the rider app presents its checklist. This is the
// missing[] vocabulary of both the approval refusal and the readiness route.
const (
	StepVehicle        = "vehicle"
	StepAadhaar        = "aadhaar_digilocker"
	StepDrivingLicence = "driving_licence"
	StepVehicleRC      = "vehicle_rc"
	StepSelfie         = "selfie"
	StepPayoutAccount  = "payout_account"
)

// Document types this package knows. Anything else is stored as submitted.
const (
	DocumentTypeAadhaar        = "AADHAAR"
	DocumentTypeDrivingLicence = "DRIVING_LICENCE"
	DocumentTypeVehicleRC      = "VEHICLE_RC"
	DocumentTypeSelfie         = "SELFIE"
)

const (
	CodeDocumentTypeRequired       = "FOOD_DOCUMENT_TYPE_REQUIRED"
	CodeDocumentTypeInvalid        = "FOOD_DOCUMENT_TYPE_INVALID"
	CodeAadhaarUseDigiLocker       = "FOOD_AADHAAR_USE_DIGILOCKER"
	CodeSelfieNumberNotAllowed     = "FOOD_SELFIE_NUMBER_NOT_ALLOWED"
	CodeDocumentNumberRequired     = "FOOD_DOCUMENT_NUMBER_REQUIRED"
	CodeDocumentNumberInvalid      = "FOOD_DOCUMENT_NUMBER_INVALID"
	CodeInvalidDrivingLicence      = "INVALID_DRIVING_LICENCE"
	CodeInvalidVehicleRegistration = "INVALID_VEHICLE_REGISTRATION"
	CodeMediaIDInvalid             = "FOOD_MEDIA_ID_INVALID"
	CodeFileURLInvalid             = "FOOD_FILE_URL_INVALID"
)

// ─── Vehicle ────────────────────────────────────────────────────────────────

type VehicleClass string

const (
	VehicleUnknown   VehicleClass = "UNKNOWN"
	VehicleBicycle   VehicleClass = "BICYCLE"
	VehicleMotorised VehicleClass = "MOTORISED"
)

var (
	bicycleTypes   = map[string]bool{"BICYCLE": true, "CYCLE": true, "PEDAL_CYCLE": true}
	motorisedTypes = map[string]bool{
		"MOTORCYCLE": true, "MOTORBIKE": true, "BIKE": true, "SCOOTER": true, "SCOOTY": true, "MOPED": true,
		"EV_SCOOTER": true, "ELECTRIC_SCOOTER": true, "EV_BIKE": true, "ELECTRIC_BIKE": true,
	}
)

// ClassifyVehicle reads the profile's free-text vehicle type. "BIKE" is a
// motorbike (Indian usage); an electric bike is treated as motorised, since
// only a low-speed e-cycle is exempt and the profile cannot tell them apart.
func ClassifyVehicle(vehicleType string) VehicleClass {
	v := strings.ToUpper(strings.TrimSpace(vehicleType))
	v = strings.NewReplacer(" ", "_", "-", "_").Replace(v)
	switch {
	case bicycleTypes[v]:
		return VehicleBicycle
	case motorisedTypes[v]:
		return VehicleMotorised
	}
	return VehicleUnknown
}

// DrivingDocumentsRequired is false only for a bicycle. An unknown vehicle
// needs them: the rule fails closed.
func DrivingDocumentsRequired(vehicleType string) bool {
	return ClassifyVehicle(vehicleType) != VehicleBicycle
}

// ─── Readiness ──────────────────────────────────────────────────────────────

// Facts are what the store knows about one partner.
type Facts struct {
	VehicleType string
	// HasAadhaarCheck: a DigiLocker AADHAAR check (reference only) exists.
	HasAadhaarCheck bool
	// HasValidDrivingLicence / HasValidVehicleRC: a DigiLocker check still in
	// validity AND an APPROVED, unexpired document of that type.
	HasValidDrivingLicence bool
	HasValidVehicleRC      bool
	HasApprovedSelfie      bool
	HasPayoutAccount       bool
}

// MissingSteps returns the unmet steps in presentation order; an empty,
// non-nil slice means the partner may be approved.
func MissingSteps(f Facts) []string {
	missing := []string{}
	class := ClassifyVehicle(f.VehicleType)
	if class == VehicleUnknown {
		missing = append(missing, StepVehicle)
	}
	if !f.HasAadhaarCheck {
		missing = append(missing, StepAadhaar)
	}
	if class != VehicleBicycle {
		if !f.HasValidDrivingLicence {
			missing = append(missing, StepDrivingLicence)
		}
		if !f.HasValidVehicleRC {
			missing = append(missing, StepVehicleRC)
		}
	}
	if !f.HasApprovedSelfie {
		missing = append(missing, StepSelfie)
	}
	if !f.HasPayoutAccount {
		missing = append(missing, StepPayoutAccount)
	}
	return missing
}

// NotReadyError is the 422 an approval gets while steps are missing.
type NotReadyError struct {
	Missing []string
}

func (e *NotReadyError) Error() string {
	return "delivery partner is not ready for approval: missing " + strings.Join(e.Missing, ", ")
}

// ─── Manual documents ───────────────────────────────────────────────────────

// LookupKind names the lookup-hash domain a document number uses, if any.
type LookupKind int

const (
	LookupNone LookupKind = iota
	LookupDrivingLicence
	LookupVehicleRegistration
)

type DocumentInput struct {
	DocumentType   string
	DocumentNumber string
	MediaID        string
	FileURL        string
}

// ValidatedDocument carries the normalised number only until the service
// seals it.
type ValidatedDocument struct {
	DocumentType string
	Number       string
	LookupKind   LookupKind
	MediaID      *uuid.UUID
	FileURL      string
}

func (v ValidatedDocument) String() string {
	return "ValidatedDocument{" + v.DocumentType + ", redacted}"
}

func (v ValidatedDocument) GoString() string { return v.String() }

var (
	documentTypePattern = regexp.MustCompile(`^[A-Z0-9_]{1,80}$`)
	documentTypeAliases = map[string]string{
		"DRIVING_LICENSE": DocumentTypeDrivingLicence, "DL": DocumentTypeDrivingLicence,
		"RC": DocumentTypeVehicleRC, "VEHICLE_REGISTRATION": DocumentTypeVehicleRC,
		"AADHAR": DocumentTypeAadhaar, "AADHAAR_CARD": DocumentTypeAadhaar,
	}
)

const maxOtherNumberRunes = 100

func fieldErr(code, field, message string) error {
	return &onboarding.FieldError{Code: code, Field: field, Message: message}
}

// ValidateDocument applies the delivery-document rules:
//   - an Aadhaar document is refused here: Aadhaar comes only from DigiLocker,
//     as a reference;
//   - any number that looks like an Aadhaar number is refused, whatever the type;
//   - a SELFIE carries a media_id and no number;
//   - DRIVING_LICENCE and VEHICLE_RC need a number that passes shared/kyc.
func ValidateDocument(in DocumentInput) (ValidatedDocument, error) {
	docType := strings.ToUpper(strings.TrimSpace(in.DocumentType))
	if docType == "" {
		return ValidatedDocument{}, fieldErr(CodeDocumentTypeRequired, "document_type", "document_type is required")
	}
	if !documentTypePattern.MatchString(docType) {
		return ValidatedDocument{}, fieldErr(CodeDocumentTypeInvalid, "document_type", "document_type must be letters, digits and underscores")
	}
	if alias, ok := documentTypeAliases[docType]; ok {
		docType = alias
	}
	if docType == DocumentTypeAadhaar {
		return ValidatedDocument{}, fieldErr(CodeAadhaarUseDigiLocker, "document_type", "Aadhaar is verified through DigiLocker, not uploaded")
	}
	number := strings.TrimSpace(in.DocumentNumber)
	if number != "" {
		if err := onboarding.RefuseAadhaarIn("document_number", number); err != nil {
			return ValidatedDocument{}, err
		}
	}
	out := ValidatedDocument{DocumentType: docType}
	if raw := strings.TrimSpace(in.MediaID); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			code := CodeMediaIDInvalid
			if docType == DocumentTypeSelfie {
				code = onboarding.CodeMediaRequired
			}
			return ValidatedDocument{}, fieldErr(code, "media_id", "media_id must be an uploaded media id")
		}
		out.MediaID = &id
	}
	out.FileURL = strings.TrimSpace(in.FileURL)
	if utf8.RuneCountInString(out.FileURL) > 2048 || strings.ContainsFunc(out.FileURL, unicode.IsSpace) {
		return ValidatedDocument{}, fieldErr(CodeFileURLInvalid, "file_url", "file_url is not a URL")
	}
	switch docType {
	case DocumentTypeSelfie:
		if number != "" {
			return ValidatedDocument{}, fieldErr(CodeSelfieNumberNotAllowed, "document_number", "a selfie carries no document number")
		}
		if out.MediaID == nil {
			return ValidatedDocument{}, fieldErr(onboarding.CodeMediaRequired, "media_id", "media_id must be the uploaded selfie's media id")
		}
	case DocumentTypeDrivingLicence:
		if number == "" {
			return ValidatedDocument{}, fieldErr(CodeDocumentNumberRequired, "document_number", "document_number is required for a driving licence")
		}
		dl, err := kyc.ValidateDrivingLicence(number)
		if err != nil {
			return ValidatedDocument{}, fieldErr(CodeInvalidDrivingLicence, "document_number", "document_number is not a well-formed driving licence number")
		}
		out.Number, out.LookupKind = dl.Normalized, LookupDrivingLicence
	case DocumentTypeVehicleRC:
		if number == "" {
			return ValidatedDocument{}, fieldErr(CodeDocumentNumberRequired, "document_number", "document_number is required for a vehicle registration")
		}
		rc, err := kyc.ValidateVehicleRegistration(number)
		if err != nil {
			return ValidatedDocument{}, fieldErr(CodeInvalidVehicleRegistration, "document_number", "document_number is not a well-formed vehicle registration")
		}
		out.Number, out.LookupKind = rc.Normalized, LookupVehicleRegistration
	default:
		if utf8.RuneCountInString(number) > maxOtherNumberRunes {
			return ValidatedDocument{}, fieldErr(CodeDocumentNumberInvalid, "document_number", "document_number is too long")
		}
		out.Number = number
	}
	return out, nil
}

// MaskName shows the initial of each of the first four words: "M*** R***".
func MaskName(name string) string {
	words := strings.Fields(name)
	if len(words) > 4 {
		words = words[:4]
	}
	out := make([]string, 0, len(words))
	for _, w := range words {
		r, _ := utf8.DecodeRuneInString(w)
		out = append(out, string(unicode.ToUpper(r))+"***")
	}
	return strings.Join(out, " ")
}
