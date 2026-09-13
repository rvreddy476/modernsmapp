package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/digilocker"
	"github.com/atpost/food-service/internal/foodpii"
	"github.com/atpost/food-service/internal/onboarding"
	"github.com/atpost/food-service/internal/riderkyc"
	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/kyc"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Golden contract fixtures for the Wave 1 B4 rider-verification routes, pinned
// for :app-rider (plan row A4). Regenerate deliberately with UPDATE_CONTRACTS=1.
// Every identifier is synthetic.

var (
	ctDLDocument = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0009")
	ctRCDocument = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a000a")
)

const (
	ctKYCCode    = "contract-callback-code"
	ctPublicBase = "https://api.example.test"
	ctAppLink    = "https://rider.example.test/kyc/digilocker"
)

type kycContractStore struct {
	service.Store
	crypto    *foodpii.Crypto
	err       error
	recordErr error
	recorded  *postgres.DigiLockerVerification
}

func (f *kycContractStore) CreateDigiLockerAuthState(context.Context, uuid.UUID, string, []byte, uint32, time.Time) (uuid.UUID, error) {
	if f.err != nil {
		return uuid.Nil, f.err
	}
	return ctPartner, nil
}

func (f *kycContractStore) ConsumeDigiLockerAuthState(ctx context.Context, _ uuid.UUID, _ string) (*postgres.DigiLockerAuthState, error) {
	if f.err != nil {
		return nil, f.err
	}
	sealed, _, err := f.crypto.SealCodeVerifier(ctx, "contract-verifier-not-a-real-one-0000000001")
	if err != nil {
		return nil, err
	}
	return &postgres.DigiLockerAuthState{PartnerID: ctPartner, VerifierSealed: sealed}, nil
}

func (f *kycContractStore) RecordDigiLockerVerification(_ context.Context, _ uuid.UUID, v postgres.DigiLockerVerification) error {
	if f.recordErr != nil {
		return f.recordErr
	}
	f.recorded = &v
	return nil
}

func ctKYCView() *postgres.DeliveryPartnerKYC {
	name, dlUntil, rcUntil := "M*** R***", "2040-06-30", "2038-06-30"
	dlMask, rcMask := "****0001", "****1234"
	dlExpires, rcExpires, verified := "2040-06-30T18:30:00Z", "2038-06-30T18:30:00Z", ctTime
	return &postgres.DeliveryPartnerKYC{
		PartnerID: ctPartner, Status: "PENDING_REVIEW", VehicleType: "MOTORCYCLE", DrivingDocumentsRequired: true,
		Missing: []string{riderkyc.StepSelfie, riderkyc.StepPayoutAccount},
		Checks: []postgres.KYCCheck{
			{Kind: "AADHAAR", Provider: "DIGILOCKER", NameOnDocumentMasked: &name, Valid: true, VerifiedAt: ctTime},
			{Kind: "DRIVING_LICENCE", Provider: "DIGILOCKER", NameOnDocumentMasked: &name, ValidUntil: &dlUntil, Valid: true, VerifiedAt: ctTime},
			{Kind: "VEHICLE_RC", Provider: "DIGILOCKER", NameOnDocumentMasked: &name, ValidUntil: &rcUntil, Valid: true, VerifiedAt: ctTime},
		},
		Documents: []postgres.DeliveryDocument{
			{ID: ctDLDocument, DeliveryPartnerID: ctPartner, DocumentType: "DRIVING_LICENCE", NumberMasked: &dlMask, Status: "APPROVED", ExpiresAt: &dlExpires, VerifiedAt: &verified, CreatedAt: ctTime},
			{ID: ctRCDocument, DeliveryPartnerID: ctPartner, DocumentType: "VEHICLE_RC", NumberMasked: &rcMask, Status: "APPROVED", ExpiresAt: &rcExpires, VerifiedAt: &verified, CreatedAt: ctTime},
		},
		HasPayoutAccount: false,
	}
}

func (f *kycContractStore) DeliveryPartnerKYCForUser(context.Context, uuid.UUID) (*postgres.DeliveryPartnerKYC, error) {
	if f.err != nil {
		return nil, f.err
	}
	return ctKYCView(), nil
}

func (f *kycContractStore) AdminDeliveryPartnerKYC(context.Context, uuid.UUID) (*postgres.DeliveryPartnerKYC, error) {
	if f.err != nil {
		return nil, f.err
	}
	return ctKYCView(), nil
}

func (f *kycContractStore) AddDeliveryPartnerDocument(_ context.Context, _ uuid.UUID, r postgres.DeliveryDocumentRecord) (*postgres.DeliveryDocument, error) {
	if f.err != nil {
		return nil, f.err
	}
	var masked *string
	if r.NumberMasked != "" {
		m := r.NumberMasked
		masked = &m
	}
	return &postgres.DeliveryDocument{ID: ctDocument, DeliveryPartnerID: ctPartner, DocumentType: r.DocumentType, NumberMasked: masked,
		MediaID: r.MediaID, Status: r.Status, CreatedAt: ctTime}, nil
}

func (f *kycContractStore) AdminDecideDeliveryPartnerDocument(_ context.Context, _, partnerID, documentID uuid.UUID, decision, reason string) (*postgres.DeliveryDocument, error) {
	if f.err != nil {
		return nil, f.err
	}
	media, verified := ctMedia, ctTime
	doc := &postgres.DeliveryDocument{ID: documentID, DeliveryPartnerID: partnerID, DocumentType: "SELFIE", MediaID: &media,
		Status: decision, VerifiedAt: &verified, CreatedAt: ctTime}
	if decision == "REJECTED" {
		doc.RejectionReason = &reason
	}
	return doc, nil
}

func (f *kycContractStore) AdminApproveDeliveryPartner(context.Context, uuid.UUID, uuid.UUID, bool, string) error {
	return f.err
}

func mockSettings() digilocker.Settings {
	return digilocker.Settings{Mode: digilocker.ModeMock, Client: digilocker.NewMockClient(), PublicBaseURL: ctPublicBase, AppLinkURL: ctAppLink}
}

func kycContractRouter(t *testing.T, st *kycContractStore, withPII bool, settings digilocker.Settings) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	st.crypto = contractCrypto(t)
	svc := service.New(st).WithOnboardingClock(func() time.Time { return time.Date(2026, 9, 13, 12, 0, 0, 0, onboarding.IST) }).WithDigiLocker(settings)
	if withPII {
		svc.WithPII(st.crypto)
	}
	router := gin.New()
	New(svc).RegisterRoutes(router)
	return router
}

func kycSynthAadhaar(t *testing.T) string {
	t.Helper()
	for c := '0'; c <= '9'; c++ {
		if n := "34567890123" + string(c); kyc.LooksLikeAadhaar(n) {
			return n
		}
	}
	t.Fatal("no synthetic Aadhaar-shaped number")
	return ""
}

// redact replaces run-time secrets (the fresh state, the S256 challenge) with
// labels, so the fixture pins the shape without ever holding one.
func redact(rec *httptest.ResponseRecorder, secrets map[string]string) *httptest.ResponseRecorder {
	body := rec.Body.String()
	for secret, label := range secrets {
		if secret != "" {
			body = strings.ReplaceAll(body, secret, label)
		}
	}
	out := httptest.NewRecorder()
	out.Code = rec.Code
	out.Body = bytes.NewBufferString(body)
	return out
}

func startSecrets(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var env struct {
		Data struct {
			AuthorizeURL string `json:"authorize_url"`
			State        string `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || !digilocker.ValidState(env.Data.State) {
		t.Fatalf("start response has no state: %s", rec.Body.String())
	}
	u, err := url.Parse(env.Data.AuthorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{env.Data.State: "<state>", u.Query().Get("code_challenge"): "<code_challenge>"}
}

var riderKYCFixtures = []string{
	"kyc_digilocker_start_200", "kyc_digilocker_start_200_http", "kyc_digilocker_start_503_not_configured",
	"kyc_digilocker_start_503_pii_not_configured", "kyc_digilocker_start_404_no_profile",
	"kyc_digilocker_callback_200", "kyc_digilocker_callback_422_state_invalid", "kyc_digilocker_callback_409_state_used",
	"kyc_digilocker_callback_410_state_expired", "kyc_digilocker_callback_403_state_not_yours",
	"kyc_digilocker_callback_502_provider_failed", "kyc_digilocker_callback_409_document_in_use",
	"kyc_digilocker_callback_422_code_required", "kyc_status_get_200", "kyc_status_get_404_no_profile",
	"admin_delivery_kyc_get_200", "admin_delivery_document_decide_200", "admin_delivery_document_decide_422_reason_required",
	"admin_delivery_document_decide_422_expired", "admin_delivery_partner_approve_422_not_ready",
	"delivery_document_post_201_selfie", "delivery_document_post_201_driving_licence",
	"delivery_document_post_422_aadhaar", "delivery_document_post_422_aadhaar_use_digilocker",
	"delivery_document_post_422_selfie_number", "digilocker_return_503_not_configured",
	"location_put_422_state_required", "location_put_422_state_invalid",
}

func TestRiderKYCContracts(t *testing.T) {
	aadhaar := kycSynthAadhaar(t)
	state, err := digilocker.NewState()
	if err != nil {
		t.Fatal(err)
	}
	callback := func(code string) string {
		b, _ := json.Marshal(map[string]string{"code": code, "state": state})
		return string(b)
	}
	httpMode := mockSettings()
	httpMode.Mode, httpMode.AuthorizeURL, httpMode.ClientID = digilocker.ModeHTTP, "https://digilocker.example.test/public/oauth2/1/authorize", "food-client"
	admin := "/v1/food/admin/delivery-partners/" + ctPartner.String()
	cases := []struct {
		fixture   string
		method    string
		path      string
		body      string
		user      uuid.UUID
		admin     bool
		noPII     bool
		settings  digilocker.Settings
		err       error
		recordErr error
		status    int
	}{
		{"kyc_digilocker_start_200", http.MethodPost, "/v1/food/delivery/kyc/digilocker/start", `{}`, ctRider, false, false, mockSettings(), nil, nil, http.StatusOK},
		{"kyc_digilocker_start_200_http", http.MethodPost, "/v1/food/delivery/kyc/digilocker/start", `{}`, ctRider, false, false, httpMode, nil, nil, http.StatusOK},
		{"kyc_digilocker_start_503_not_configured", http.MethodPost, "/v1/food/delivery/kyc/digilocker/start", `{}`, ctRider, false, false, digilocker.Settings{}, nil, nil, http.StatusServiceUnavailable},
		{"kyc_digilocker_start_503_pii_not_configured", http.MethodPost, "/v1/food/delivery/kyc/digilocker/start", `{}`, ctRider, false, true, mockSettings(), nil, nil, http.StatusServiceUnavailable},
		{"kyc_digilocker_start_404_no_profile", http.MethodPost, "/v1/food/delivery/kyc/digilocker/start", `{}`, ctRider, false, false, mockSettings(), pgx.ErrNoRows, nil, http.StatusNotFound},
		{"kyc_digilocker_callback_200", http.MethodPost, "/v1/food/delivery/kyc/digilocker/callback", callback(ctKYCCode), ctRider, false, false, mockSettings(), nil, nil, http.StatusOK},
		{"kyc_digilocker_callback_422_state_invalid", http.MethodPost, "/v1/food/delivery/kyc/digilocker/callback", callback(ctKYCCode), ctRider, false, false, mockSettings(), postgres.ErrDigiLockerStateNotFound, nil, http.StatusUnprocessableEntity},
		{"kyc_digilocker_callback_409_state_used", http.MethodPost, "/v1/food/delivery/kyc/digilocker/callback", callback(ctKYCCode), ctRider, false, false, mockSettings(), postgres.ErrDigiLockerStateUsed, nil, http.StatusConflict},
		{"kyc_digilocker_callback_410_state_expired", http.MethodPost, "/v1/food/delivery/kyc/digilocker/callback", callback(ctKYCCode), ctRider, false, false, mockSettings(), postgres.ErrDigiLockerStateExpired, nil, http.StatusGone},
		{"kyc_digilocker_callback_403_state_not_yours", http.MethodPost, "/v1/food/delivery/kyc/digilocker/callback", callback(ctKYCCode), ctRider, false, false, mockSettings(), postgres.ErrDigiLockerStateNotYours, nil, http.StatusForbidden},
		{"kyc_digilocker_callback_502_provider_failed", http.MethodPost, "/v1/food/delivery/kyc/digilocker/callback", callback("fail"), ctRider, false, false, mockSettings(), nil, nil, http.StatusBadGateway},
		{"kyc_digilocker_callback_409_document_in_use", http.MethodPost, "/v1/food/delivery/kyc/digilocker/callback", callback(ctKYCCode), ctRider, false, false, mockSettings(), nil, postgres.ErrDocumentNumberInUse, http.StatusConflict},
		{"kyc_digilocker_callback_422_code_required", http.MethodPost, "/v1/food/delivery/kyc/digilocker/callback", callback(""), ctRider, false, false, mockSettings(), nil, nil, http.StatusUnprocessableEntity},
		{"kyc_status_get_200", http.MethodGet, "/v1/food/delivery/kyc/status", ``, ctRider, false, false, mockSettings(), nil, nil, http.StatusOK},
		{"kyc_status_get_404_no_profile", http.MethodGet, "/v1/food/delivery/kyc/status", ``, ctRider, false, false, mockSettings(), pgx.ErrNoRows, nil, http.StatusNotFound},
		{"admin_delivery_kyc_get_200", http.MethodGet, admin + "/kyc", ``, ctAdmin, true, false, mockSettings(), nil, nil, http.StatusOK},
		{"admin_delivery_document_decide_200", http.MethodPost, admin + "/documents/" + ctDocument.String() + "/decide", `{"decision":"APPROVED"}`, ctAdmin, true, false, mockSettings(), nil, nil, http.StatusOK},
		{"admin_delivery_document_decide_422_reason_required", http.MethodPost, admin + "/documents/" + ctDocument.String() + "/decide", `{"decision":"REJECTED"}`, ctAdmin, true, false, mockSettings(), nil, nil, http.StatusUnprocessableEntity},
		{"admin_delivery_document_decide_422_expired", http.MethodPost, admin + "/documents/" + ctDocument.String() + "/decide", `{"decision":"APPROVED"}`, ctAdmin, true, false, mockSettings(), postgres.ErrDocumentExpired, nil, http.StatusUnprocessableEntity},
		{"admin_delivery_partner_approve_422_not_ready", http.MethodPost, admin + "/approve", `{}`, ctAdmin, true, false, mockSettings(),
			&riderkyc.NotReadyError{Missing: []string{riderkyc.StepDrivingLicence, riderkyc.StepVehicleRC, riderkyc.StepSelfie}}, nil, http.StatusUnprocessableEntity},
		{"delivery_document_post_201_selfie", http.MethodPost, "/v1/food/delivery/documents", `{"document_type":"SELFIE","media_id":"` + ctMedia.String() + `"}`, ctRider, false, false, mockSettings(), nil, nil, http.StatusCreated},
		{"delivery_document_post_201_driving_licence", http.MethodPost, "/v1/food/delivery/documents", `{"document_type":"DRIVING_LICENCE","document_number":"KA-01-2020-0000001","media_id":"` + ctMedia.String() + `"}`, ctRider, false, false, mockSettings(), nil, nil, http.StatusCreated},
		{"delivery_document_post_422_aadhaar", http.MethodPost, "/v1/food/delivery/documents", `{"document_type":"INSURANCE","document_number":"` + aadhaar + `"}`, ctRider, false, false, mockSettings(), nil, nil, http.StatusUnprocessableEntity},
		{"delivery_document_post_422_aadhaar_use_digilocker", http.MethodPost, "/v1/food/delivery/documents", `{"document_type":"AADHAAR","media_id":"` + ctMedia.String() + `"}`, ctRider, false, false, mockSettings(), nil, nil, http.StatusUnprocessableEntity},
		{"delivery_document_post_422_selfie_number", http.MethodPost, "/v1/food/delivery/documents", `{"document_type":"SELFIE","document_number":"X1","media_id":"` + ctMedia.String() + `"}`, ctRider, false, false, mockSettings(), nil, nil, http.StatusUnprocessableEntity},
		{"digilocker_return_503_not_configured", http.MethodGet, digilocker.ReturnPath + "?code=abc&state=def", ``, ctRider, false, false, digilocker.Settings{}, nil, nil, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			st := &kycContractStore{err: tc.err, recordErr: tc.recordErr}
			router := kycContractRouter(t, st, !tc.noPII, tc.settings)
			rec := doJSON(router, tc.method, tc.path, tc.body, tc.user, tc.admin)
			visible := rec
			if strings.HasPrefix(tc.fixture, "kyc_digilocker_start_200") {
				visible = redact(rec, startSecrets(t, rec))
			}
			assertContract(t, visible, tc.status, tc.fixture)
			for _, secret := range []string{aadhaar, "KA0120200000001", "KA-01-2020-0000001", state, ctKYCCode, "contract-verifier"} {
				if strings.Contains(rec.Body.String(), secret) {
					t.Fatalf("%s: the response carries a value it must not", tc.fixture)
				}
			}
			if tc.fixture == "kyc_digilocker_callback_200" {
				if st.recorded == nil || len(st.recorded.Checks) != 3 || len(st.recorded.Documents) != 2 {
					t.Fatalf("callback persisted %+v", st.recorded)
				}
			}
		})
	}
}

// Fixtures an Android DTO test consumes must never hold a DL, RC or Aadhaar
// number, a state, a verifier, a challenge or a code — including the fixtures
// of earlier lanes.
func TestNoContractFixtureCarriesKYCSecrets(t *testing.T) {
	for _, name := range riderKYCFixtures {
		if _, err := os.Stat(filepath.Join(contractsDir, name+".json")); err != nil {
			t.Fatalf("missing B4 fixture %s", name)
		}
	}
	entries, err := os.ReadDir(contractsDir)
	if err != nil {
		t.Fatal(err)
	}
	alnum := regexp.MustCompile(`[A-Za-z0-9]+`)
	oauthToken := regexp.MustCompile(`[A-Za-z0-9_-]{43,}`)
	oauthParam := regexp.MustCompile(`(state|code|code_challenge|code_verifier)=[A-Za-z0-9_-]`)
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(contractsDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if kyc.LooksLikeAadhaar(text) {
			t.Fatalf("%s carries an Aadhaar-shaped number", e.Name())
		}
		for _, tok := range alnum.FindAllString(text, -1) {
			if len(tok) < 8 || !strings.ContainsAny(tok, "0123456789") {
				continue
			}
			if _, err := kyc.ValidateDrivingLicence(tok); err == nil {
				t.Fatalf("%s carries a driving-licence-shaped number", e.Name())
			}
			if _, err := kyc.ValidateVehicleRegistration(tok); err == nil {
				t.Fatalf("%s carries a vehicle-registration-shaped number", e.Name())
			}
		}
		if oauthToken.MatchString(text) || oauthParam.MatchString(text) {
			t.Fatalf("%s carries an OAuth state, code, challenge or verifier", e.Name())
		}
		for _, literal := range []string{"mock-", ctKYCCode, "contract-verifier", "assertion_ref", "doc_type_hash", "number_lookup", "number_sealed"} {
			if strings.Contains(text, literal) {
				t.Fatalf("%s carries %q", e.Name(), literal)
			}
		}
	}
}

func TestDigiLockerReturnRedirectsToTheAppLinkWithoutAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := mockSettings()
	settings.AppLinkURL = ctAppLink + "?src=digilocker"
	svc := service.New(&kycContractStore{}).WithDigiLocker(settings)
	router := gin.New()
	New(svc).RegisterRoutes(router)
	state, _ := digilocker.NewState()
	req := httptest.NewRequest(http.MethodGet, digilocker.ReturnPath+"?code=provider-code-1&state="+state+"&error=", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("return = %d %v", rec.Code, rec.Header())
	}
	u, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Scheme+"://"+u.Host+u.Path != ctAppLink || q.Get("code") != "provider-code-1" || q.Get("state") != state || q.Get("src") != "digilocker" {
		t.Fatal("the return route did not pass code and state to the App Link")
	}
}

// The mock authorize page must not exist outside local/dev, whatever the
// DIGILOCKER_MODE: it hands out verification codes to anyone.
func TestDevDigiLockerAuthorizeRouteOnlyInLocalDevWithTheMock(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		env  string
		mock bool
		want int
	}{
		{"production", true, http.StatusNotFound},
		{"staging", true, http.StatusNotFound},
		{"", true, http.StatusNotFound},
		{"dev", false, http.StatusNotFound},
		{"local", true, http.StatusFound},
		{"development", true, http.StatusFound},
		{"DEV", true, http.StatusFound},
	}
	for _, tc := range cases {
		svc := service.New(&kycContractStore{}).WithDigiLocker(mockSettings())
		router := gin.New()
		New(svc).WithDigiLockerDevRoutes(tc.env, tc.mock).RegisterRoutes(router)
		state, _ := digilocker.NewState()
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, digilocker.DevAuthorizePath+"?state="+state, nil))
		if rec.Code != tc.want {
			t.Fatalf("ENV=%q mock=%v: status = %d, want %d", tc.env, tc.mock, rec.Code, tc.want)
		}
		if rec.Code != http.StatusFound {
			continue
		}
		u, err := url.Parse(rec.Header().Get("Location"))
		if err != nil || u.Scheme+"://"+u.Host+u.Path != ctPublicBase+digilocker.ReturnPath ||
			u.Query().Get("state") != state || !strings.HasPrefix(u.Query().Get("code"), "mock-") {
			t.Fatalf("ENV=%q: dev authorize location is wrong", tc.env)
		}
	}
}
