// Package onboarding holds the pure validation rules for restaurant
// onboarding (Wave 1 B1) and payout-account capture (B2). Nothing here
// touches the database, a key or the network, so every rule is unit-tested
// and the store only ever receives values that already passed them.
//
// Error text never echoes an identifier the caller sent: a PAN, GSTIN,
// account number or licence is described, never repeated.
package onboarding

import (
	"errors"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/shared/gst"
	"github.com/atpost/shared/kyc"
	"github.com/google/uuid"
)

// IST is the calendar every onboarding date is read in: licence expiry and
// the specified-premises declaration are Indian calendar dates.
var IST = time.FixedZone("IST", 5*3600+30*60)

// DateLayout is the wire format for every date field.
const DateLayout = "2006-01-02"

// Stable machine-readable codes. The kyc-derived ones reuse the shared
// sentinel text so Android and the other services see the same code.
const (
	CodeTaxCategoryInvalid                   = "FOOD_TAX_CATEGORY_INVALID"
	CodeLegalNameRequired                    = "FOOD_LEGAL_NAME_REQUIRED"
	CodeInvalidPAN                           = "INVALID_PAN"
	CodeInvalidGSTIN                         = "INVALID_GSTIN"
	CodeGSTINRequired                        = "FOOD_GSTIN_REQUIRED"
	CodeGSTINPANMismatch                     = "FOOD_GSTIN_PAN_MISMATCH"
	CodeSpecifiedPremisesDeclarationRequired = "FOOD_SPECIFIED_PREMISES_DECLARATION_REQUIRED"
	CodeInvalidDate                          = "FOOD_INVALID_DATE"

	CodeLocationRequired         = "FOOD_LOCATION_REQUIRED"
	CodeCoordinatesOutOfRange    = "FOOD_COORDINATES_OUT_OF_RANGE"
	CodeDeliveryRadiusOutOfRange = "FOOD_DELIVERY_RADIUS_OUT_OF_RANGE"
	CodeAddressRequired          = "FOOD_ADDRESS_REQUIRED"
	CodeAddressInvalid           = "FOOD_ADDRESS_INVALID"
	CodePlaceIDInvalid           = "FOOD_GOOGLE_PLACE_ID_INVALID"
	CodeStateRequired            = "FOOD_STATE_REQUIRED"
	CodeStateInvalid             = "FOOD_STATE_INVALID"

	CodeOperatingHoursRequired   = "FOOD_OPERATING_HOURS_REQUIRED"
	CodeOperatingHoursDayInvalid = "FOOD_OPERATING_HOURS_DAY_INVALID"
	CodeOperatingHoursTime       = "FOOD_OPERATING_HOURS_TIME_INVALID"
	CodeOperatingHoursTooMany    = "FOOD_OPERATING_HOURS_TOO_MANY"
	CodeOperatingHoursOverlap    = "FOOD_OPERATING_HOURS_OVERLAP"
	CodeOperatingHoursConflict   = "FOOD_OPERATING_HOURS_CONFLICT"

	CodeInvalidFSSAILicence = "INVALID_FSSAI_LICENCE"
	CodeFSSAIExpiryInvalid  = "FOOD_FSSAI_EXPIRY_INVALID"
	CodeMediaRequired       = "FOOD_MEDIA_ID_REQUIRED"
	CodeAadhaarNotAllowed   = "AADHAAR_NOT_ALLOWED"
	// CodeFSSAIUseDedicatedRoute refuses an FSSAI upload through the generic
	// document route, which would skip licence and expiry validation.
	CodeFSSAIUseDedicatedRoute = "FOOD_FSSAI_USE_DEDICATED_ROUTE"

	CodeHolderNameRequired = "FOOD_HOLDER_NAME_REQUIRED"
	CodeInvalidBankAccount = "INVALID_BANK_ACCOUNT"
	CodeInvalidIFSC        = "INVALID_IFSC"

	CodeDocumentDecisionInvalid = "FOOD_DOCUMENT_DECISION_INVALID"
	CodeRejectionReasonRequired = "FOOD_REJECTION_REASON_REQUIRED"
	CodeAcceptingRequired       = "FOOD_IS_ACCEPTING_ORDERS_REQUIRED"
)

// FieldError is a 422: the request was understood and refused. Message is
// human text that never contains the submitted value.
type FieldError struct {
	Code    string
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Code + ": " + e.Message }

func fieldErr(code, field, message string) error {
	return &FieldError{Code: code, Field: field, Message: message}
}

// RefuseAadhaarIn is the one wrapper every restaurant document number goes
// through before it is written: the platform does not store Aadhaar numbers.
func RefuseAadhaarIn(field, text string) error {
	if err := kyc.RefuseAadhaar(text); err != nil {
		return fieldErr(CodeAadhaarNotAllowed, field, "an Aadhaar number must not be submitted here")
	}
	return nil
}

func todayIST(now time.Time) time.Time {
	y, m, d := now.In(IST).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, IST)
}

// ParseDate reads a YYYY-MM-DD calendar date in IST.
func ParseDate(field, s string) (time.Time, error) {
	t, err := time.ParseInLocation(DateLayout, strings.TrimSpace(s), IST)
	if err != nil {
		return time.Time{}, fieldErr(CodeInvalidDate, field, field+" must be a date in YYYY-MM-DD form")
	}
	return t, nil
}

// ─── Compliance ─────────────────────────────────────────────────────────────

type ComplianceInput struct {
	TaxCategory                 string
	LegalName                   string
	PAN                         string
	GSTIN                       string
	SpecifiedPremisesDeclaredAt string
}

type ValidatedCompliance struct {
	Category                    gst.Category
	Liability                   gst.Liability
	GSTINRequired               bool
	LegalName                   string
	PAN                         kyc.PAN
	GSTIN                       *kyc.GSTIN
	SpecifiedPremisesDeclaredAt *time.Time
}

// specifiedPremises names the categories whose supply is made in "specified
// premises". shared/gst carries the rate and the s.9(5) flag per row but no
// premises flag, so the membership is declared here against its constants.
var specifiedPremises = map[gst.Category]bool{
	gst.CategoryRestaurantSpecifiedPremises:      true,
	gst.CategoryOutdoorCateringSpecifiedPremises: true,
}

// IsSpecifiedPremises reports whether c needs a specified-premises declaration.
func IsSpecifiedPremises(c gst.Category) bool { return specifiedPremises[c] }

// RestaurantTaxCategories lists, sorted, every category the table supplies as
// the restaurant — the set a restaurant may choose from.
func RestaurantTaxCategories(table *gst.RateTable) []gst.Category {
	seen := map[gst.Category]bool{}
	var out []gst.Category
	for _, r := range table.Rows() {
		if r.Supplier == gst.SupplierRestaurant && !seen[r.Category] {
			seen[r.Category] = true
			out = append(out, r.Category)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// LiabilityOf reads who owes the GST on a rate row.
func LiabilityOf(row gst.RateRow) gst.Liability {
	if row.ECOSection95 {
		return gst.LiabilityECOSection95
	}
	return gst.LiabilitySupplier
}

// ValidateCompliance applies the tax-identity rules:
//   - the category must be a restaurant supply in the rate table on `now`;
//   - PAN is required and must be well formed;
//   - a GSTIN, when given, must be well formed and embed that same PAN;
//   - a GSTIN is REQUIRED when the category's rate row leaves the restaurant
//     liable (not s.9(5)); it is optional when the ECO is liable;
//   - specified-premises categories need a declaration date, not in the future.
func ValidateCompliance(table *gst.RateTable, in ComplianceInput, now time.Time) (ValidatedCompliance, error) {
	if table == nil {
		return ValidatedCompliance{}, errors.New("onboarding: no GST rate table")
	}
	category := gst.Category(strings.ToUpper(strings.TrimSpace(in.TaxCategory)))
	row, err := table.Lookup(category, now)
	if err != nil || row.Supplier != gst.SupplierRestaurant {
		return ValidatedCompliance{}, fieldErr(CodeTaxCategoryInvalid, "tax_category", "tax_category is not a restaurant tax category in effect today")
	}
	legalName := strings.TrimSpace(in.LegalName)
	if legalName == "" || utf8.RuneCountInString(legalName) > 200 {
		return ValidatedCompliance{}, fieldErr(CodeLegalNameRequired, "legal_name", "legal_name is required and at most 200 characters")
	}
	pan, err := kyc.ValidatePAN(in.PAN)
	if err != nil {
		return ValidatedCompliance{}, fieldErr(CodeInvalidPAN, "pan", "pan is not a well-formed PAN")
	}
	var gstin *kyc.GSTIN
	if strings.TrimSpace(in.GSTIN) != "" {
		g, err := kyc.ValidateGSTIN(in.GSTIN)
		if err != nil {
			msg := "gstin is not a well-formed GSTIN"
			var ge *kyc.GSTINError
			if errors.As(err, &ge) {
				msg = "gstin failed its " + strings.ToLower(string(ge.Part)) + " check"
			}
			return ValidatedCompliance{}, fieldErr(CodeInvalidGSTIN, "gstin", msg)
		}
		if g.PAN != pan.Normalized {
			return ValidatedCompliance{}, fieldErr(CodeGSTINPANMismatch, "gstin", "the PAN inside gstin does not match pan")
		}
		gstin = &g
	}
	required := !row.ECOSection95
	if required && gstin == nil {
		return ValidatedCompliance{}, fieldErr(CodeGSTINRequired, "gstin", "gstin is required: this tax category leaves the restaurant liable for GST")
	}
	var declared *time.Time
	if IsSpecifiedPremises(category) {
		if strings.TrimSpace(in.SpecifiedPremisesDeclaredAt) == "" {
			return ValidatedCompliance{}, fieldErr(CodeSpecifiedPremisesDeclarationRequired, "specified_premises_declared_at", "specified_premises_declared_at is required for a specified-premises category")
		}
		d, err := ParseDate("specified_premises_declared_at", in.SpecifiedPremisesDeclaredAt)
		if err != nil {
			return ValidatedCompliance{}, err
		}
		if d.After(todayIST(now)) {
			return ValidatedCompliance{}, fieldErr(CodeInvalidDate, "specified_premises_declared_at", "specified_premises_declared_at cannot be in the future")
		}
		declared = &d
	}
	return ValidatedCompliance{
		Category:                    category,
		Liability:                   LiabilityOf(row),
		GSTINRequired:               required,
		LegalName:                   legalName,
		PAN:                         pan,
		GSTIN:                       gstin,
		SpecifiedPremisesDeclaredAt: declared,
	}, nil
}

// ─── Location ───────────────────────────────────────────────────────────────

const (
	MinDeliveryRadiusKM = 1.0
	MaxDeliveryRadiusKM = 15.0
)

type LocationInput struct {
	Latitude         *float64
	Longitude        *float64
	AddressLine1     string
	AddressLine2     string
	City             string
	State            string
	PostalCode       string
	GooglePlaceID    string
	DeliveryRadiusKM *float64
}

type ValidatedLocation struct {
	Latitude         float64
	Longitude        float64
	AddressLine1     string
	AddressLine2     string
	City             string
	State            string
	PostalCode       string
	GooglePlaceID    string
	DeliveryRadiusKM float64
}

func ValidateLocation(in LocationInput) (ValidatedLocation, error) {
	if in.Latitude == nil || in.Longitude == nil {
		return ValidatedLocation{}, fieldErr(CodeLocationRequired, "latitude", "latitude and longitude are required")
	}
	lat, lng := *in.Latitude, *in.Longitude
	if math.IsNaN(lat) || math.IsNaN(lng) || lat < -90 || lat > 90 || lng < -180 || lng > 180 || (lat == 0 && lng == 0) {
		return ValidatedLocation{}, fieldErr(CodeCoordinatesOutOfRange, "latitude", "latitude must be within -90..90 and longitude within -180..180")
	}
	if in.DeliveryRadiusKM == nil || math.IsNaN(*in.DeliveryRadiusKM) || *in.DeliveryRadiusKM < MinDeliveryRadiusKM || *in.DeliveryRadiusKM > MaxDeliveryRadiusKM {
		return ValidatedLocation{}, fieldErr(CodeDeliveryRadiusOutOfRange, "delivery_radius_km", "delivery_radius_km must be between 1 and 15")
	}
	out := ValidatedLocation{
		Latitude:         lat,
		Longitude:        lng,
		AddressLine1:     strings.TrimSpace(in.AddressLine1),
		AddressLine2:     strings.TrimSpace(in.AddressLine2),
		City:             strings.TrimSpace(in.City),
		State:            strings.TrimSpace(in.State),
		PostalCode:       strings.TrimSpace(in.PostalCode),
		GooglePlaceID:    strings.TrimSpace(in.GooglePlaceID),
		DeliveryRadiusKM: math.Round(*in.DeliveryRadiusKM*100) / 100,
	}
	if out.AddressLine1 == "" || out.City == "" {
		return ValidatedLocation{}, fieldErr(CodeAddressRequired, "address_line1", "address_line1 and city are required")
	}
	// Checkout's place of supply falls back to this state, so a restaurant
	// without one cannot be priced. Stored as the canonical GST state name.
	if out.State == "" {
		return ValidatedLocation{}, fieldErr(CodeStateRequired, "state", "state is required")
	}
	state, ok := ResolveStateName(out.State)
	if !ok {
		return ValidatedLocation{}, fieldErr(CodeStateInvalid, "state", "state must be the name of an Indian state or union territory")
	}
	out.State = state
	limits := []struct {
		field, value string
		max          int
	}{
		{"address_line1", out.AddressLine1, 255}, {"address_line2", out.AddressLine2, 255},
		{"city", out.City, 120}, {"state", out.State, 120}, {"postal_code", out.PostalCode, 20},
	}
	for _, l := range limits {
		if utf8.RuneCountInString(l.value) > l.max {
			return ValidatedLocation{}, fieldErr(CodeAddressInvalid, l.field, l.field+" is too long")
		}
	}
	if len(out.GooglePlaceID) > 255 || strings.ContainsAny(out.GooglePlaceID, " \t\r\n") {
		return ValidatedLocation{}, fieldErr(CodePlaceIDInvalid, "google_place_id", "google_place_id is not a place id")
	}
	return out, nil
}

// stateCodesExcluded are GST codes whose names a restaurant location may not
// use: 97 is "Other Territory" and 28 is Andhra Pradesh before
// reorganisation. Neither is a place a new restaurant is in.
var stateCodesExcluded = map[string]bool{"97": true, "28": true}

var knownStateNames = func() map[string]string {
	out := map[string]string{}
	for i := 1; i <= 99; i++ {
		code := string([]byte{byte('0' + i/10), byte('0' + i%10)})
		if stateCodesExcluded[code] {
			continue
		}
		if name, ok := kyc.GSTStateName(code); ok {
			out[strings.ToLower(name)] = name
		}
	}
	return out
}()

// KnownStateNames lists, sorted, every state name the location route accepts.
func KnownStateNames() []string {
	out := make([]string, 0, len(knownStateNames))
	for _, name := range knownStateNames {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ResolveStateName matches s, ignoring case and repeated spaces, against the
// GST state and union-territory names the pricing code recognises, and
// returns the canonical spelling.
func ResolveStateName(s string) (string, bool) {
	name, ok := knownStateNames[strings.ToLower(strings.Join(strings.Fields(s), " "))]
	return name, ok
}

// ─── FSSAI ──────────────────────────────────────────────────────────────────

type FSSAIInput struct {
	LicenceNumber string
	ExpiresAt     string
	MediaID       string
}

// ValidatedFSSAI.ExpiresOn is the licence's expiry date at 00:00 IST. The
// licence is valid strictly before that instant, which is why the date must
// be after today.
type ValidatedFSSAI struct {
	LicenceNumber string
	ExpiresOn     time.Time
	MediaID       uuid.UUID
}

func ValidateFSSAI(in FSSAIInput, now time.Time) (ValidatedFSSAI, error) {
	if err := RefuseAadhaarIn("licence_number", in.LicenceNumber); err != nil {
		return ValidatedFSSAI{}, err
	}
	licence, err := kyc.ValidateFSSAILicence(in.LicenceNumber)
	if err != nil {
		return ValidatedFSSAI{}, fieldErr(CodeInvalidFSSAILicence, "licence_number", "licence_number must be the 14-digit FSSAI licence number")
	}
	expires, err := ParseDate("expires_at", in.ExpiresAt)
	if err != nil {
		return ValidatedFSSAI{}, err
	}
	if !expires.After(todayIST(now)) {
		return ValidatedFSSAI{}, fieldErr(CodeFSSAIExpiryInvalid, "expires_at", "expires_at must be after today")
	}
	mediaID, err := uuid.Parse(strings.TrimSpace(in.MediaID))
	if err != nil {
		return ValidatedFSSAI{}, fieldErr(CodeMediaRequired, "media_id", "media_id must be the uploaded licence's media id")
	}
	return ValidatedFSSAI{LicenceNumber: licence, ExpiresOn: expires, MediaID: mediaID}, nil
}

// ─── Payout account ─────────────────────────────────────────────────────────

type PayoutAccountInput struct {
	HolderName    string
	AccountNumber string
	IFSC          string
}

type ValidatedPayoutAccount struct {
	HolderName    string
	AccountNumber string
	IFSC          string
}

// String keeps the account number out of any accidental %v.
func (v ValidatedPayoutAccount) String() string { return "ValidatedPayoutAccount{redacted}" }

// GoString keeps the account number out of %#v.
func (v ValidatedPayoutAccount) GoString() string { return v.String() }

func ValidatePayoutAccount(in PayoutAccountInput) (ValidatedPayoutAccount, error) {
	holder := strings.TrimSpace(in.HolderName)
	if holder == "" || utf8.RuneCountInString(holder) > 200 {
		return ValidatedPayoutAccount{}, fieldErr(CodeHolderNameRequired, "holder_name", "holder_name is required and at most 200 characters")
	}
	account, err := kyc.NormalizeBankAccountNumber(in.AccountNumber)
	if err != nil {
		return ValidatedPayoutAccount{}, fieldErr(CodeInvalidBankAccount, "account_number", "account_number must be 9 to 18 digits")
	}
	ifsc, err := kyc.NormalizeIFSC(in.IFSC)
	if err != nil {
		return ValidatedPayoutAccount{}, fieldErr(CodeInvalidIFSC, "ifsc", "ifsc is not a well-formed IFSC")
	}
	return ValidatedPayoutAccount{HolderName: holder, AccountNumber: account, IFSC: ifsc}, nil
}

// ─── Admin document decision ────────────────────────────────────────────────

const (
	DecisionApproved = "APPROVED"
	DecisionRejected = "REJECTED"
)

func ValidateDocumentDecision(decision, reason string) (string, error) {
	d := strings.ToUpper(strings.TrimSpace(decision))
	switch d {
	case DecisionApproved:
		return d, nil
	case DecisionRejected:
		if strings.TrimSpace(reason) == "" {
			return "", fieldErr(CodeRejectionReasonRequired, "reason", "reason is required when rejecting a document")
		}
		return d, nil
	}
	return "", fieldErr(CodeDocumentDecisionInvalid, "decision", "decision must be APPROVED or REJECTED")
}

// ─── Submit readiness ───────────────────────────────────────────────────────

// Step names, in the order a partner app should present them.
const (
	StepLocation = "location"
	// StepState: the restaurant's state resolves to a GST state, without
	// which checkout refuses every order (FOOD_RESTAURANT_STATE_UNKNOWN).
	StepState          = "state"
	StepOperatingHours = "operating_hours"
	StepCompliance     = "compliance"
	StepFSSAI          = "fssai_document"
	StepPayoutAccount  = "payout_account"
	StepMenuItem       = "menu_item"
)

type ReadinessFacts struct {
	HasLocation          bool
	HasState             bool
	HasOperatingHours    bool
	HasCompliance        bool
	HasFSSAIDocument     bool
	HasPayoutAccount     bool
	HasAvailableMenuItem bool
}

// MissingSteps returns the unmet steps in presentation order; an empty,
// non-nil slice means ready.
func MissingSteps(f ReadinessFacts) []string {
	missing := []string{}
	for _, s := range []struct {
		ok   bool
		name string
	}{
		{f.HasLocation, StepLocation},
		{f.HasState, StepState},
		{f.HasOperatingHours, StepOperatingHours},
		{f.HasCompliance, StepCompliance},
		{f.HasFSSAIDocument, StepFSSAI},
		{f.HasPayoutAccount, StepPayoutAccount},
		{f.HasAvailableMenuItem, StepMenuItem},
	} {
		if !s.ok {
			missing = append(missing, s.name)
		}
	}
	return missing
}

// NotReadyError is the 422 a submit gets while steps are missing.
type NotReadyError struct {
	Missing []string
}

func (e *NotReadyError) Error() string {
	return "restaurant is not ready for review: missing " + strings.Join(e.Missing, ", ")
}
