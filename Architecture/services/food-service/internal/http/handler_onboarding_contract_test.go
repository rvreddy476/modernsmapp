package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/foodpii"
	"github.com/atpost/food-service/internal/onboarding"
	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/kyc"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Golden contract fixtures for the Wave 1 B1/B2 routes. Android pins its DTOs
// to testdata/contracts/*.json. Regenerate deliberately with
// UPDATE_CONTRACTS=1, then review the diff.
//
// Every identifier is synthetic: ZZZPZ0000Z is not an issued PAN, the GSTIN
// check digit is computed, and 000123456789 is not a bank account.

var (
	ctOwner       = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0001")
	ctRestaurant  = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0002")
	ctAdmin       = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0003")
	ctDocument    = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0004")
	ctServiceArea = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0005")
	ctPartner     = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0006")
	ctMedia       = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0007")
	ctRider       = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0008")
)

const (
	ctTime    = "2026-09-13T06:30:00Z"
	ctPAN     = "ZZZPZ0000Z"
	ctAccount = "000123456789"
)

func ctGSTIN() string {
	d, err := kyc.GSTINCheckDigit("29" + ctPAN + "1Z")
	if err != nil {
		panic(err)
	}
	return "29" + ctPAN + "1Z" + string(d)
}

type contractStore struct {
	service.Store
	err error
}

func (f *contractStore) GetPartnerRestaurant(_ context.Context, ownerID, restaurantID uuid.UUID) (*postgres.PartnerRestaurant, error) {
	if errors.Is(f.err, pgx.ErrNoRows) {
		return nil, f.err
	}
	return &postgres.PartnerRestaurant{ID: restaurantID, OwnerUserID: ownerID}, nil
}

func (f *contractStore) GetDeliveryPartner(_ context.Context, userID uuid.UUID) (*postgres.DeliveryPartner, error) {
	if errors.Is(f.err, pgx.ErrNoRows) {
		return nil, f.err
	}
	return &postgres.DeliveryPartner{ID: ctPartner, UserID: userID}, nil
}

func (f *contractStore) SetRestaurantCompliance(_ context.Context, _, rid uuid.UUID, rec postgres.ComplianceRecord) (*postgres.RestaurantCompliance, error) {
	if f.err != nil {
		return nil, f.err
	}
	var declared *string
	if rec.SpecifiedPremisesDeclaredAt != nil {
		d := rec.SpecifiedPremisesDeclaredAt.Format(onboarding.DateLayout)
		declared = &d
	}
	return &postgres.RestaurantCompliance{RestaurantID: rid, TaxCategory: rec.TaxCategory, LegalName: rec.LegalName,
		GSTIN: rec.GSTIN, GSTINStateCode: rec.GSTINStateCode, PANMasked: rec.PANMasked, PANHolderType: rec.PANHolderType,
		SpecifiedPremisesDeclaredAt: declared, ComplianceSubmittedAt: ctTime}, nil
}

func (f *contractStore) SetRestaurantLocation(_ context.Context, _, rid uuid.UUID, loc onboarding.ValidatedLocation) (*postgres.RestaurantLocation, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &postgres.RestaurantLocation{RestaurantID: rid, Latitude: loc.Latitude, Longitude: loc.Longitude,
		AddressLine1: loc.AddressLine1, AddressLine2: loc.AddressLine2, City: loc.City, State: loc.State,
		PostalCode: loc.PostalCode, GooglePlaceID: loc.GooglePlaceID, DeliveryRadiusKM: loc.DeliveryRadiusKM, ServiceAreaID: ctServiceArea}, nil
}

func (f *contractStore) ReplaceOperatingHours(_ context.Context, _, rid uuid.UUID, in []postgres.OperatingHoursInput) (*postgres.OperatingHours, error) {
	if _, err := postgres.NormalizeOperatingHours(in); err != nil {
		return nil, err
	}
	if f.err != nil {
		return nil, f.err
	}
	out := &postgres.OperatingHours{RestaurantID: rid, Timezone: "Asia/Kolkata", Windows: []postgres.OperatingHoursWindow{}}
	for _, w := range in {
		if w.IsClosed {
			out.Windows = append(out.Windows, postgres.OperatingHoursWindow{DayOfWeek: *w.DayOfWeek, IsClosed: true})
			continue
		}
		out.Windows = append(out.Windows, postgres.OperatingHoursWindow{DayOfWeek: *w.DayOfWeek, OpensAt: w.OpensAt, ClosesAt: w.ClosesAt, Overnight: w.ClosesAt <= w.OpensAt})
	}
	return out, nil
}

func (f *contractStore) SetRestaurantAccepting(_ context.Context, _, rid uuid.UUID, accepting bool) (*postgres.RestaurantAccepting, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &postgres.RestaurantAccepting{RestaurantID: rid, Status: "ACTIVE", IsAcceptingOrders: accepting}, nil
}

func (f *contractStore) SubmitRestaurantFSSAI(_ context.Context, _, rid uuid.UUID, v onboarding.ValidatedFSSAI) (*postgres.RestaurantFSSAI, error) {
	if f.err != nil {
		return nil, f.err
	}
	number, media := v.LicenceNumber, v.MediaID
	expires := v.ExpiresOn.UTC().Format(time.RFC3339)
	return &postgres.RestaurantFSSAI{RestaurantID: rid, LicenceNumber: v.LicenceNumber, ExpiresAt: v.ExpiresOn.Format(onboarding.DateLayout),
		Document: postgres.RestaurantDocument{ID: ctDocument, RestaurantID: rid, DocumentType: "FSSAI", DocumentNumber: &number,
			MediaID: &media, Status: "PENDING", ExpiresAt: &expires, CreatedAt: ctTime}}, nil
}

func (f *contractStore) SubmitRestaurantForReview(_ context.Context, _, rid uuid.UUID) (*postgres.RestaurantSubmission, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &postgres.RestaurantSubmission{RestaurantID: rid, Status: "PENDING_REVIEW", Missing: []string{}}, nil
}

func (f *contractStore) AdminDecideRestaurantDocument(_ context.Context, adminID, rid, docID uuid.UUID, decision, reason string) (*postgres.RestaurantDocument, error) {
	if f.err != nil {
		return nil, f.err
	}
	number, expires, verifiedAt := "10099999000000", "2027-03-30T18:30:00Z", ctTime
	media := ctMedia
	doc := &postgres.RestaurantDocument{ID: docID, RestaurantID: rid, DocumentType: "FSSAI", DocumentNumber: &number, MediaID: &media,
		Status: decision, ExpiresAt: &expires, VerifiedBy: &adminID, VerifiedAt: &verifiedAt, CreatedAt: ctTime}
	if decision == "REJECTED" {
		doc.RejectionReason = &reason
	}
	return doc, nil
}

func (f *contractStore) payout(ownerType string, ownerID uuid.UUID, rec postgres.PayoutAccountRecord) *postgres.PayoutAccount {
	reason := rec.VerificationReason
	return &postgres.PayoutAccount{OwnerType: ownerType, OwnerID: ownerID, HolderName: rec.HolderName,
		AccountNumberMasked: "****" + rec.AccountLast4, IFSC: rec.IFSC, VerificationStatus: rec.VerificationStatus,
		VerificationReason: &reason, CreatedAt: ctTime, UpdatedAt: ctTime}
}

var ctStoredPayout = postgres.PayoutAccountRecord{HolderName: "Test Holder", AccountLast4: "6789", IFSC: "HDFC0000053",
	VerificationStatus: "NOT_VERIFIED", VerificationReason: "verification_pending_ops"}

func (f *contractStore) UpsertRestaurantPayoutAccount(_ context.Context, _, rid uuid.UUID, rec postgres.PayoutAccountRecord) (*postgres.PayoutAccount, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.payout("RESTAURANT", rid, rec), nil
}

func (f *contractStore) GetRestaurantPayoutAccount(_ context.Context, _, rid uuid.UUID) (*postgres.PayoutAccount, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.payout("RESTAURANT", rid, ctStoredPayout), nil
}

func (f *contractStore) UpsertDeliveryPartnerPayoutAccount(_ context.Context, _ uuid.UUID, rec postgres.PayoutAccountRecord) (*postgres.PayoutAccount, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.payout("DELIVERY_PARTNER", ctPartner, rec), nil
}

func (f *contractStore) GetDeliveryPartnerPayoutAccount(context.Context, uuid.UUID) (*postgres.PayoutAccount, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.payout("DELIVERY_PARTNER", ctPartner, ctStoredPayout), nil
}

func contractCrypto(t *testing.T) *foodpii.Crypto {
	t.Helper()
	c, err := foodpii.New(context.Background(), []foodpii.VersionedKey{{Version: 1, Key: bytes.Repeat([]byte{5}, 32)}}, []byte("contract-test-lookup-salt-000001"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func contractRouter(t *testing.T, st *contractStore, withPII bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := service.New(st).WithOnboardingClock(func() time.Time { return time.Date(2026, 9, 13, 12, 0, 0, 0, onboarding.IST) })
	if withPII {
		svc.WithPII(contractCrypto(t))
	}
	router := gin.New()
	New(svc).RegisterRoutes(router)
	return router
}

const contractsDir = "testdata/contracts"

func assertContract(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, fixture string) {
	t.Helper()
	path := filepath.Join(contractsDir, fixture+".json")
	if rec.Code != wantStatus {
		t.Fatalf("%s: status = %d, want %d (body %s)", fixture, rec.Code, wantStatus, rec.Body.String())
	}
	if os.Getenv("UPDATE_CONTRACTS") == "1" {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, bytes.TrimSpace(rec.Body.Bytes()), "", "  "); err != nil {
			t.Fatalf("%s: indent: %v", fixture, err)
		}
		pretty.WriteByte('\n')
		if err := os.MkdirAll(contractsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, pretty.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wantRaw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: missing fixture (run with UPDATE_CONTRACTS=1 and review): %v", fixture, err)
	}
	var got, want any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%s: response is not JSON: %v", fixture, err)
	}
	if err := json.Unmarshal(wantRaw, &want); err != nil {
		t.Fatalf("%s: fixture is not JSON: %v", fixture, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: response drifted from %s\n got: %s", fixture, path, rec.Body.String())
	}
}

func TestOnboardingContracts(t *testing.T) {
	restaurant := "/v1/food/partner/restaurants/" + ctRestaurant.String()
	gstin := ctGSTIN()
	cases := []struct {
		fixture string
		method  string
		path    string
		body    string
		user    uuid.UUID
		admin   bool
		noPII   bool
		err     error
		status  int
	}{
		{"compliance_put_200", http.MethodPut, restaurant + "/compliance",
			`{"tax_category":"RESTAURANT_SPECIFIED_PREMISES","legal_name":"Test Kitchens LLP","pan":"zzzpz0000z","gstin":"` + gstin + `","specified_premises_declared_at":"2026-09-01"}`,
			ctOwner, false, false, nil, http.StatusOK},
		{"compliance_put_200_eco_without_gstin", http.MethodPut, restaurant + "/compliance",
			`{"tax_category":"CLOUD_KITCHEN_TAKEAWAY","legal_name":"Test Kitchens LLP","pan":"` + ctPAN + `"}`,
			ctOwner, false, false, nil, http.StatusOK},
		{"compliance_put_422_gstin_required", http.MethodPut, restaurant + "/compliance",
			`{"tax_category":"OUTDOOR_CATERING","legal_name":"Test Caterers","pan":"` + ctPAN + `"}`,
			ctOwner, false, false, nil, http.StatusUnprocessableEntity},
		{"compliance_put_422_gstin_pan_mismatch", http.MethodPut, restaurant + "/compliance",
			`{"tax_category":"RESTAURANT_STANDALONE","legal_name":"Test Kitchens LLP","pan":"YYYCY1111Y","gstin":"` + gstin + `"}`,
			ctOwner, false, false, nil, http.StatusUnprocessableEntity},
		{"compliance_put_503_pii_not_configured", http.MethodPut, restaurant + "/compliance",
			`{"tax_category":"RESTAURANT_STANDALONE","legal_name":"Test Kitchens LLP","pan":"` + ctPAN + `"}`,
			ctOwner, false, true, nil, http.StatusServiceUnavailable},
		{"onboarding_404_not_owner", http.MethodPut, restaurant + "/location",
			`{"latitude":12.9716,"longitude":77.5946,"address_line1":"1 Test Lane","city":"Bengaluru","delivery_radius_km":5}`,
			ctOwner, false, false, pgx.ErrNoRows, http.StatusNotFound},
		{"onboarding_400_invalid_body", http.MethodPut, restaurant + "/compliance", `{"pan":`,
			ctOwner, false, false, nil, http.StatusBadRequest},
		{"location_put_200", http.MethodPut, restaurant + "/location",
			`{"latitude":12.9716,"longitude":77.5946,"address_line1":"1 Test Lane","address_line2":"Near Test Park","city":"Bengaluru","state":"Karnataka","postal_code":"560001","google_place_id":"ChIJtestplace0001","delivery_radius_km":6.5}`,
			ctOwner, false, false, nil, http.StatusOK},
		{"location_put_422_radius", http.MethodPut, restaurant + "/location",
			`{"latitude":12.9716,"longitude":77.5946,"address_line1":"1 Test Lane","city":"Bengaluru","delivery_radius_km":20}`,
			ctOwner, false, false, nil, http.StatusUnprocessableEntity},
		{"operating_hours_put_200", http.MethodPut, restaurant + "/operating-hours",
			`{"windows":[{"day_of_week":0,"is_closed":true},{"day_of_week":1,"opens_at":"11:00","closes_at":"15:00"},{"day_of_week":1,"opens_at":"18:00","closes_at":"23:00"},{"day_of_week":5,"opens_at":"18:00","closes_at":"02:00"}]}`,
			ctOwner, false, false, nil, http.StatusOK},
		{"operating_hours_put_422_day", http.MethodPut, restaurant + "/operating-hours",
			`{"windows":[{"day_of_week":7,"opens_at":"10:00","closes_at":"22:00"}]}`,
			ctOwner, false, false, nil, http.StatusUnprocessableEntity},
		{"accepting_patch_200", http.MethodPatch, restaurant + "/accepting", `{"is_accepting_orders":true}`,
			ctOwner, false, false, nil, http.StatusOK},
		{"accepting_patch_422_fssai_required", http.MethodPatch, restaurant + "/accepting", `{"is_accepting_orders":true}`,
			ctOwner, false, false, postgres.ErrFSSAIRequired, http.StatusUnprocessableEntity},
		{"accepting_patch_422_not_live", http.MethodPatch, restaurant + "/accepting", `{"is_accepting_orders":true}`,
			ctOwner, false, false, postgres.ErrRestaurantNotLive, http.StatusUnprocessableEntity},
		{"fssai_put_200", http.MethodPut, restaurant + "/fssai",
			`{"licence_number":"10099999000000","expires_at":"2027-03-31","media_id":"` + ctMedia.String() + `"}`,
			ctOwner, false, false, nil, http.StatusOK},
		{"fssai_put_422_expiry", http.MethodPut, restaurant + "/fssai",
			`{"licence_number":"10099999000000","expires_at":"2026-09-13","media_id":"` + ctMedia.String() + `"}`,
			ctOwner, false, false, nil, http.StatusUnprocessableEntity},
		{"submit_post_200", http.MethodPost, restaurant + "/submit", `{}`,
			ctOwner, false, false, nil, http.StatusOK},
		{"submit_post_422_not_ready", http.MethodPost, restaurant + "/submit", `{}`,
			ctOwner, false, false, &onboarding.NotReadyError{Missing: []string{onboarding.StepFSSAI, onboarding.StepPayoutAccount}}, http.StatusUnprocessableEntity},
		{"submit_post_409_not_draft", http.MethodPost, restaurant + "/submit", `{}`,
			ctOwner, false, false, postgres.ErrRestaurantNotDraft, http.StatusConflict},
		{"restaurant_payout_account_put_200", http.MethodPut, restaurant + "/payout-account",
			`{"holder_name":"Test Holder","account_number":"` + ctAccount + `","ifsc":"hdfc0000053"}`,
			ctOwner, false, false, nil, http.StatusOK},
		{"restaurant_payout_account_get_200", http.MethodGet, restaurant + "/payout-account", ``,
			ctOwner, false, false, nil, http.StatusOK},
		{"payout_account_put_422_ifsc", http.MethodPut, restaurant + "/payout-account",
			`{"holder_name":"Test Holder","account_number":"` + ctAccount + `","ifsc":"HDFC1000053"}`,
			ctOwner, false, false, nil, http.StatusUnprocessableEntity},
		{"payout_account_put_503_pii_not_configured", http.MethodPut, restaurant + "/payout-account",
			`{"holder_name":"Test Holder","account_number":"` + ctAccount + `","ifsc":"HDFC0000053"}`,
			ctOwner, false, true, nil, http.StatusServiceUnavailable},
		{"payout_account_get_404", http.MethodGet, "/v1/food/delivery/payout-account", ``,
			ctRider, false, false, pgx.ErrNoRows, http.StatusNotFound},
		{"delivery_payout_account_put_200", http.MethodPut, "/v1/food/delivery/payout-account",
			`{"holder_name":"Test Rider","account_number":"` + ctAccount + `","ifsc":"SBIN0001234"}`,
			ctRider, false, false, nil, http.StatusOK},
		{"delivery_payout_account_get_200", http.MethodGet, "/v1/food/delivery/payout-account", ``,
			ctRider, false, false, nil, http.StatusOK},
		{"admin_document_decide_200", http.MethodPost, "/v1/food/admin/restaurants/" + ctRestaurant.String() + "/documents/" + ctDocument.String() + "/decide",
			`{"decision":"APPROVED"}`, ctAdmin, true, false, nil, http.StatusOK},
		{"admin_document_decide_200_rejected", http.MethodPost, "/v1/food/admin/restaurants/" + ctRestaurant.String() + "/documents/" + ctDocument.String() + "/decide",
			`{"decision":"REJECTED","reason":"Licence photo is unreadable"}`, ctAdmin, true, false, nil, http.StatusOK},
		{"admin_document_decide_422_expired", http.MethodPost, "/v1/food/admin/restaurants/" + ctRestaurant.String() + "/documents/" + ctDocument.String() + "/decide",
			`{"decision":"APPROVED"}`, ctAdmin, true, false, postgres.ErrDocumentExpired, http.StatusUnprocessableEntity},
		{"admin_document_decide_422_reason_required", http.MethodPost, "/v1/food/admin/restaurants/" + ctRestaurant.String() + "/documents/" + ctDocument.String() + "/decide",
			`{"decision":"REJECTED"}`, ctAdmin, true, false, nil, http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			router := contractRouter(t, &contractStore{err: tc.err}, !tc.noPII)
			rec := doJSON(router, tc.method, tc.path, tc.body, tc.user, tc.admin)
			assertContract(t, rec, tc.status, tc.fixture)
			// A GSTIN is a public registration number that embeds the PAN by
			// construction; only the gstin value itself may contain it.
			visible := strings.ReplaceAll(rec.Body.String(), gstin, "<gstin>")
			for _, secret := range []string{ctPAN, strings.ToLower(ctPAN), ctAccount, ctAccount[:8]} {
				if strings.Contains(visible, secret) {
					t.Fatalf("%s: response carries a plaintext identifier", tc.fixture)
				}
			}
		})
	}
}

// TestContractFixturesCarryNoPlaintext guards the committed fixtures too.
func TestContractFixturesCarryNoPlaintext(t *testing.T) {
	entries, err := os.ReadDir(contractsDir)
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}
	if len(entries) < 25 {
		t.Fatalf("fixtures = %d, expected the full B1/B2 set", len(entries))
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(contractsDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		// B3 fixtures also carry the synthetic platform GSTIN (invoice only).
		raw = bytes.ReplaceAll(raw, []byte(ctGSTIN()), []byte("<gstin>"))
		raw = bytes.ReplaceAll(raw, []byte(ctPlatformGSTIN()), []byte("<gstin>"))
		for _, secret := range []string{ctPAN, strings.ToLower(ctPAN), ctPlatformPAN, strings.ToLower(ctPlatformPAN), ctAccount, ctAccount[:8]} {
			if bytes.Contains(raw, []byte(secret)) {
				t.Fatalf("%s carries a plaintext identifier", e.Name())
			}
		}
	}
}
