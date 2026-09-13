package onboarding

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/pricing"
	"github.com/atpost/shared/gst"
	"github.com/atpost/shared/kyc"
)

// Synthetic identifiers only. ZZZ?Z0000Z is a well-formed PAN shape that no
// real holder is issued; the GSTIN check digit is computed, never copied.
const (
	testPAN      = "ZZZPZ0000Z"
	otherTestPAN = "YYYCY1111Y"
)

func synthGSTIN(t *testing.T, state, pan string) string {
	t.Helper()
	first14 := state + pan + "1Z"
	d, err := kyc.GSTINCheckDigit(first14)
	if err != nil {
		t.Fatalf("check digit: %v", err)
	}
	return first14 + string(d)
}

var testNow = time.Date(2026, 9, 13, 12, 0, 0, 0, IST)

func fieldCode(err error) string {
	var fe *FieldError
	if errors.As(err, &fe) {
		return fe.Code
	}
	if err == nil {
		return ""
	}
	return "NOT_A_FIELD_ERROR:" + err.Error()
}

func TestValidateComplianceMatrix(t *testing.T) {
	table := gst.DefaultRateTable()
	gstin := synthGSTIN(t, "29", testPAN)
	mismatched := synthGSTIN(t, "29", otherTestPAN)

	cases := []struct {
		name string
		in   ComplianceInput
		code string
	}{
		{"eco category without gstin", ComplianceInput{TaxCategory: "RESTAURANT_STANDALONE", LegalName: "Test Kitchens LLP", PAN: testPAN}, ""},
		{"eco category with matching gstin", ComplianceInput{TaxCategory: "CLOUD_KITCHEN_TAKEAWAY", LegalName: "Test", PAN: testPAN, GSTIN: gstin}, ""},
		{"supplier-liable specified premises without gstin", ComplianceInput{TaxCategory: "RESTAURANT_SPECIFIED_PREMISES", LegalName: "Test", PAN: testPAN, SpecifiedPremisesDeclaredAt: "2026-09-01"}, CodeGSTINRequired},
		{"supplier-liable outdoor catering without gstin", ComplianceInput{TaxCategory: "OUTDOOR_CATERING", LegalName: "Test", PAN: testPAN}, CodeGSTINRequired},
		{"supplier-liable outdoor catering with gstin", ComplianceInput{TaxCategory: "OUTDOOR_CATERING", LegalName: "Test", PAN: testPAN, GSTIN: gstin}, ""},
		{"gstin embeds a different pan", ComplianceInput{TaxCategory: "RESTAURANT_STANDALONE", LegalName: "Test", PAN: testPAN, GSTIN: mismatched}, CodeGSTINPANMismatch},
		{"gstin mismatch on supplier-liable", ComplianceInput{TaxCategory: "OUTDOOR_CATERING", LegalName: "Test", PAN: testPAN, GSTIN: mismatched}, CodeGSTINPANMismatch},
		{"invalid pan", ComplianceInput{TaxCategory: "RESTAURANT_STANDALONE", LegalName: "Test", PAN: "ZZZ0Z0000Z"}, CodeInvalidPAN},
		{"missing pan", ComplianceInput{TaxCategory: "RESTAURANT_STANDALONE", LegalName: "Test"}, CodeInvalidPAN},
		{"bad gstin checksum", ComplianceInput{TaxCategory: "RESTAURANT_STANDALONE", LegalName: "Test", PAN: testPAN, GSTIN: gstin[:14] + flipCheck(gstin[14])}, CodeInvalidGSTIN},
		{"unknown category", ComplianceInput{TaxCategory: "FOOD_TRUCK", LegalName: "Test", PAN: testPAN}, CodeTaxCategoryInvalid},
		{"platform category is not a restaurant supply", ComplianceInput{TaxCategory: "PLATFORM_FEE", LegalName: "Test", PAN: testPAN}, CodeTaxCategoryInvalid},
		{"blank legal name", ComplianceInput{TaxCategory: "RESTAURANT_STANDALONE", LegalName: "   ", PAN: testPAN}, CodeLegalNameRequired},
		{"specified premises needs declaration", ComplianceInput{TaxCategory: "RESTAURANT_SPECIFIED_PREMISES", LegalName: "Test", PAN: testPAN, GSTIN: gstin}, CodeSpecifiedPremisesDeclarationRequired},
		{"specified premises catering needs declaration", ComplianceInput{TaxCategory: "OUTDOOR_CATERING_SPECIFIED_PREMISES", LegalName: "Test", PAN: testPAN, GSTIN: gstin}, CodeSpecifiedPremisesDeclarationRequired},
		{"specified premises declared", ComplianceInput{TaxCategory: "RESTAURANT_SPECIFIED_PREMISES", LegalName: "Test", PAN: testPAN, GSTIN: gstin, SpecifiedPremisesDeclaredAt: "2026-09-13"}, ""},
		{"declaration in the future", ComplianceInput{TaxCategory: "RESTAURANT_SPECIFIED_PREMISES", LegalName: "Test", PAN: testPAN, GSTIN: gstin, SpecifiedPremisesDeclaredAt: "2026-09-14"}, CodeInvalidDate},
		{"declaration badly formatted", ComplianceInput{TaxCategory: "RESTAURANT_SPECIFIED_PREMISES", LegalName: "Test", PAN: testPAN, GSTIN: gstin, SpecifiedPremisesDeclaredAt: "13/09/2026"}, CodeInvalidDate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateCompliance(table, tc.in, testNow)
			if got := fieldCode(err); got != tc.code {
				t.Fatalf("code = %q, want %q", got, tc.code)
			}
		})
	}
}

func flipCheck(b byte) string {
	if b == 'A' {
		return "B"
	}
	return "A"
}

func TestValidateComplianceNormalises(t *testing.T) {
	gstin := synthGSTIN(t, "29", testPAN)
	v, err := ValidateCompliance(gst.DefaultRateTable(), ComplianceInput{
		TaxCategory: " restaurant_standalone ", LegalName: "  Test Kitchens  ",
		PAN: strings.ToLower(testPAN), GSTIN: " " + strings.ToLower(gstin) + " ",
		SpecifiedPremisesDeclaredAt: "2026-09-01",
	}, testNow)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if v.Category != gst.CategoryRestaurantStandalone || v.LegalName != "Test Kitchens" {
		t.Fatalf("category/legal name not normalised: %+v", v)
	}
	if v.PAN.Normalized != testPAN || v.GSTIN == nil || v.GSTIN.Normalized != gstin || v.GSTIN.StateCode != "29" {
		t.Fatalf("identifiers not normalised")
	}
	if v.Liability != gst.LiabilityECOSection95 || v.GSTINRequired {
		t.Fatalf("liability = %s required = %v", v.Liability, v.GSTINRequired)
	}
	if v.SpecifiedPremisesDeclaredAt != nil {
		t.Fatalf("a declaration on a non-specified-premises category must be dropped")
	}
}

// TestGSTINRequirementFollowsRateTable proves the requirement is read from the
// rate row, not from a hard-coded category list: flipping ECOSection95 on a
// copy of the table flips the answer.
func TestGSTINRequirementFollowsRateTable(t *testing.T) {
	def := gst.DefaultRateTable()
	for _, c := range RestaurantTaxCategories(def) {
		row, err := def.Lookup(c, testNow)
		if err != nil {
			t.Fatal(err)
		}
		in := ComplianceInput{TaxCategory: string(c), LegalName: "Test", PAN: testPAN, SpecifiedPremisesDeclaredAt: "2026-09-01"}
		_, err = ValidateCompliance(def, in, testNow)
		wantRequired := !row.ECOSection95
		if gotRequired := fieldCode(err) == CodeGSTINRequired; gotRequired != wantRequired {
			t.Fatalf("%s: gstin required = %v, want %v (err %v)", c, gotRequired, wantRequired, err)
		}
	}

	var rows []gst.RateRow
	for _, r := range def.Rows() {
		if r.Category == gst.CategoryRestaurantStandalone {
			r.ECOSection95 = false
		}
		rows = append(rows, r)
	}
	flipped, err := gst.NewRateTable(rows)
	if err != nil {
		t.Fatalf("flipped table: %v", err)
	}
	_, err = ValidateCompliance(flipped, ComplianceInput{TaxCategory: "RESTAURANT_STANDALONE", LegalName: "Test", PAN: testPAN}, testNow)
	if fieldCode(err) != CodeGSTINRequired {
		t.Fatalf("with the row made supplier-liable, gstin must be required; got %v", err)
	}
}

func TestRestaurantTaxCategories(t *testing.T) {
	got := RestaurantTaxCategories(gst.DefaultRateTable())
	want := []gst.Category{
		gst.CategoryCloudKitchenTakeaway, gst.CategoryOutdoorCatering,
		gst.CategoryOutdoorCateringSpecifiedPremises, gst.CategoryRestaurantSpecifiedPremises,
		gst.CategoryRestaurantStandalone,
	}
	if len(got) != len(want) {
		t.Fatalf("categories = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("categories = %v, want %v", got, want)
		}
	}
}

func TestMissingSteps(t *testing.T) {
	all := MissingSteps(ReadinessFacts{})
	want := []string{StepLocation, StepState, StepOperatingHours, StepCompliance, StepFSSAI, StepPayoutAccount, StepMenuItem}
	if strings.Join(all, ",") != strings.Join(want, ",") {
		t.Fatalf("missing = %v, want %v", all, want)
	}
	full := ReadinessFacts{HasLocation: true, HasState: true, HasOperatingHours: true, HasCompliance: true, HasFSSAIDocument: true, HasPayoutAccount: true, HasAvailableMenuItem: true}
	if got := MissingSteps(full); got == nil || len(got) != 0 {
		t.Fatalf("complete facts: missing = %#v, want empty non-nil", got)
	}
	singles := []struct {
		step  string
		unset func(*ReadinessFacts)
	}{
		{StepLocation, func(f *ReadinessFacts) { f.HasLocation = false }},
		{StepState, func(f *ReadinessFacts) { f.HasState = false }},
		{StepOperatingHours, func(f *ReadinessFacts) { f.HasOperatingHours = false }},
		{StepCompliance, func(f *ReadinessFacts) { f.HasCompliance = false }},
		{StepFSSAI, func(f *ReadinessFacts) { f.HasFSSAIDocument = false }},
		{StepPayoutAccount, func(f *ReadinessFacts) { f.HasPayoutAccount = false }},
		{StepMenuItem, func(f *ReadinessFacts) { f.HasAvailableMenuItem = false }},
	}
	for _, s := range singles {
		f := full
		s.unset(&f)
		if got := MissingSteps(f); len(got) != 1 || got[0] != s.step {
			t.Fatalf("unset %s: missing = %v", s.step, got)
		}
	}
	err := error(&NotReadyError{Missing: []string{StepFSSAI}})
	var nr *NotReadyError
	if !errors.As(err, &nr) || nr.Missing[0] != StepFSSAI {
		t.Fatalf("NotReadyError does not unwrap")
	}
}

// Every state the location route accepts must be one checkout can price:
// pricing.Restaurant.PlaceOfSupplyState resolves the stored canonical name.
func TestStateNamesResolveForPricing(t *testing.T) {
	names := KnownStateNames()
	if len(names) < 36 {
		t.Fatalf("known states = %d, want every state and union territory", len(names))
	}
	for _, name := range names {
		got, ok := ResolveStateName(strings.ToLower(name))
		if !ok || got != name {
			t.Fatalf("ResolveStateName(%q) = %q, %v", strings.ToLower(name), got, ok)
		}
		if _, err := (pricing.Restaurant{State: got}).PlaceOfSupplyState(); err != nil {
			t.Fatalf("pricing cannot place %q: %v", got, err)
		}
	}
	if v, err := ValidateLocation(LocationInput{Latitude: ptr(12.9), Longitude: ptr(77.5), AddressLine1: "1 Test Lane", City: "Chennai", State: "tamil nadu", DeliveryRadiusKM: ptr(5)}); err != nil || v.State != "Tamil Nadu" {
		t.Fatalf("stored state = %q, %v; want the canonical name", v.State, err)
	}
}

func ptr(v float64) *float64 { return &v }

func TestValidateLocation(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	base := func() LocationInput {
		return LocationInput{Latitude: f(12.9716), Longitude: f(77.5946), AddressLine1: "1 Test Lane", City: "Bengaluru", State: "Karnataka", DeliveryRadiusKM: f(5)}
	}
	cases := []struct {
		name string
		mut  func(*LocationInput)
		code string
	}{
		{"valid", func(*LocationInput) {}, ""},
		{"radius lower bound", func(in *LocationInput) { in.DeliveryRadiusKM = f(1) }, ""},
		{"radius upper bound", func(in *LocationInput) { in.DeliveryRadiusKM = f(15) }, ""},
		{"radius below", func(in *LocationInput) { in.DeliveryRadiusKM = f(0.99) }, CodeDeliveryRadiusOutOfRange},
		{"radius above", func(in *LocationInput) { in.DeliveryRadiusKM = f(15.01) }, CodeDeliveryRadiusOutOfRange},
		{"radius missing", func(in *LocationInput) { in.DeliveryRadiusKM = nil }, CodeDeliveryRadiusOutOfRange},
		{"radius NaN", func(in *LocationInput) { in.DeliveryRadiusKM = f(math.NaN()) }, CodeDeliveryRadiusOutOfRange},
		{"latitude missing", func(in *LocationInput) { in.Latitude = nil }, CodeLocationRequired},
		{"longitude missing", func(in *LocationInput) { in.Longitude = nil }, CodeLocationRequired},
		{"latitude out of range", func(in *LocationInput) { in.Latitude = f(90.5) }, CodeCoordinatesOutOfRange},
		{"longitude out of range", func(in *LocationInput) { in.Longitude = f(-180.5) }, CodeCoordinatesOutOfRange},
		{"NaN coordinate", func(in *LocationInput) { in.Latitude = f(math.NaN()) }, CodeCoordinatesOutOfRange},
		{"null island", func(in *LocationInput) { in.Latitude, in.Longitude = f(0), f(0) }, CodeCoordinatesOutOfRange},
		{"address missing", func(in *LocationInput) { in.AddressLine1 = " " }, CodeAddressRequired},
		{"city missing", func(in *LocationInput) { in.City = "" }, CodeAddressRequired},
		{"place id too long", func(in *LocationInput) { in.GooglePlaceID = strings.Repeat("a", 256) }, CodePlaceIDInvalid},
		// B4 follow-up: checkout cannot price an order without the state, so
		// the location route no longer accepts a pin without one.
		{"state missing", func(in *LocationInput) { in.State = "" }, CodeStateRequired},
		{"state blank", func(in *LocationInput) { in.State = "  " }, CodeStateRequired},
		{"state unknown", func(in *LocationInput) { in.State = "Atlantis" }, CodeStateInvalid},
		{"state other territory", func(in *LocationInput) { in.State = "Other Territory" }, CodeStateInvalid},
		{"state as a GST code", func(in *LocationInput) { in.State = "29" }, CodeStateInvalid},
		{"state any case and spacing", func(in *LocationInput) { in.State = "  tamil   NADU " }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base()
			tc.mut(&in)
			_, err := ValidateLocation(in)
			if got := fieldCode(err); got != tc.code {
				t.Fatalf("code = %q, want %q", got, tc.code)
			}
		})
	}
}

// synthAadhaarShaped builds a 12-digit, Verhoeff-valid number from a base
// that starts 2-9 by trying each check digit against the shared detector.
// It is synthetic by construction and never a real Aadhaar number.
func synthAadhaarShaped(t *testing.T, base11 string) string {
	t.Helper()
	for c := '0'; c <= '9'; c++ {
		if candidate := base11 + string(c); kyc.LooksLikeAadhaar(candidate) {
			return candidate
		}
	}
	t.Fatalf("no check digit for base")
	return ""
}

func TestValidateFSSAI(t *testing.T) {
	media := "7c1b0f6e-3a0c-4a51-9d7e-4c8f3f7b5a10"
	ok := FSSAIInput{LicenceNumber: "10099999000000", ExpiresAt: "2027-03-31", MediaID: media}
	v, err := ValidateFSSAI(ok, testNow)
	if err != nil {
		t.Fatalf("valid: %v", err)
	}
	if v.LicenceNumber != "10099999000000" || v.ExpiresOn.Format(DateLayout) != "2027-03-31" || v.MediaID.String() != media {
		t.Fatalf("normalised = %+v", v)
	}
	if v.ExpiresOn.Location() != IST {
		t.Fatalf("expiry must be an IST date")
	}
	cases := []struct {
		name string
		in   FSSAIInput
		code string
	}{
		{"spaced licence ok", FSSAIInput{LicenceNumber: "100 999 990 000 00", ExpiresAt: "2026-09-14", MediaID: media}, ""},
		{"thirteen digits", FSSAIInput{LicenceNumber: "1009999900000", ExpiresAt: "2027-03-31", MediaID: media}, CodeInvalidFSSAILicence},
		{"expires today", FSSAIInput{LicenceNumber: "10099999000000", ExpiresAt: "2026-09-13", MediaID: media}, CodeFSSAIExpiryInvalid},
		{"expired", FSSAIInput{LicenceNumber: "10099999000000", ExpiresAt: "2026-01-01", MediaID: media}, CodeFSSAIExpiryInvalid},
		{"bad date", FSSAIInput{LicenceNumber: "10099999000000", ExpiresAt: "31-03-2027", MediaID: media}, CodeInvalidDate},
		{"media missing", FSSAIInput{LicenceNumber: "10099999000000", ExpiresAt: "2027-03-31"}, CodeMediaRequired},
		{"media not a uuid", FSSAIInput{LicenceNumber: "10099999000000", ExpiresAt: "2027-03-31", MediaID: "nope"}, CodeMediaRequired},
		{"aadhaar-shaped number refused", FSSAIInput{LicenceNumber: synthAadhaarShaped(t, "23456789012"), ExpiresAt: "2027-03-31", MediaID: media}, CodeAadhaarNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateFSSAI(tc.in, testNow)
			if got := fieldCode(err); got != tc.code {
				t.Fatalf("code = %q, want %q", got, tc.code)
			}
		})
	}
}

func TestValidatePayoutAccount(t *testing.T) {
	const acct = "000123456789"
	v, err := ValidatePayoutAccount(PayoutAccountInput{HolderName: "  Test Holder ", AccountNumber: " " + acct + " ", IFSC: "hdfc0000053"})
	if err != nil {
		t.Fatalf("valid: %v", err)
	}
	if v.HolderName != "Test Holder" || v.AccountNumber != acct || v.IFSC != "HDFC0000053" {
		t.Fatalf("normalised = holder %q ifsc %q", v.HolderName, v.IFSC)
	}
	cases := []struct {
		name string
		in   PayoutAccountInput
		code string
	}{
		{"holder missing", PayoutAccountInput{AccountNumber: acct, IFSC: "HDFC0000053"}, CodeHolderNameRequired},
		{"account too short", PayoutAccountInput{HolderName: "T", AccountNumber: "12345678", IFSC: "HDFC0000053"}, CodeInvalidBankAccount},
		{"account with letters", PayoutAccountInput{HolderName: "T", AccountNumber: "12345678901A", IFSC: "HDFC0000053"}, CodeInvalidBankAccount},
		{"bad ifsc", PayoutAccountInput{HolderName: "T", AccountNumber: acct, IFSC: "HDFC1000053"}, CodeInvalidIFSC},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidatePayoutAccount(tc.in)
			if got := fieldCode(err); got != tc.code {
				t.Fatalf("code = %q, want %q", got, tc.code)
			}
			if err != nil && strings.Contains(err.Error(), tc.in.AccountNumber) && tc.in.AccountNumber != "" {
				t.Fatalf("error text echoes the account number")
			}
		})
	}
}

func TestValidateDocumentDecision(t *testing.T) {
	cases := []struct {
		decision, reason, want, code string
	}{
		{"APPROVED", "", "APPROVED", ""},
		{"approved", "", "APPROVED", ""},
		{"REJECTED", "blurry scan", "REJECTED", ""},
		{"REJECTED", "  ", "", CodeRejectionReasonRequired},
		{"MAYBE", "", "", CodeDocumentDecisionInvalid},
		{"", "", "", CodeDocumentDecisionInvalid},
	}
	for _, tc := range cases {
		got, err := ValidateDocumentDecision(tc.decision, tc.reason)
		if fieldCode(err) != tc.code || (tc.code == "" && got != tc.want) {
			t.Fatalf("%q/%q: got %q err %v", tc.decision, tc.reason, got, err)
		}
	}
}
