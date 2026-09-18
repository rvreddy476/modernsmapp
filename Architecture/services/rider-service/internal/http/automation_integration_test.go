// Launch-safety routes over the real store (TEST_PG_DSN on rider_it_test):
// the retired proof route answers 410 pointing at the checkout, the
// onboarding route names the pending documents, and the checkout route
// refuses a bad method before payments.
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/rider-service/internal/digilocker"
	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/rider-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func partnerServe(r *gin.Engine, method, path, body string, userID uuid.UUID) *httptest.ResponseRecorder {
	var rd *strings.Reader
	if body == "" {
		rd = strings.NewReader("")
	} else {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", adminTestInternalKey)
	req.Header.Set("X-User-Id", userID.String())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestLaunchSafetyRoutes(t *testing.T) {
	rg, pool := setupAdminTokenIT(t)
	uid := uuid.New()
	itExec(t, pool, `INSERT INTO rider_partners (user_id, partner_type, full_name, phone, status)
		VALUES ($1, 'individual_driver', 'Route Partner', $2, 'pending_verification')`, uid, "+91"+uuid.NewString()[:10])
	svc := service.New(store.New(pool), nil, service.Config{})

	// The retired proof route: 410 with the pointer, nothing written.
	w := partnerServe(rg.r, http.MethodPost, "/v1/rider/subscriptions/payment-proof", `{"payment_id":"`+uuid.NewString()+`","file_url":"https://x/p.jpg"}`, uid)
	if w.Code != http.StatusGone || errorCode(t, w) != service.CodeSubscriptionProofGone || !strings.Contains(w.Body.String(), "/subscriptions/checkout") {
		t.Fatalf("proof route: %d %s", w.Code, w.Body.String())
	}
	// Checkout: a bad method is refused before payments (400).
	w = partnerServe(rg.r, http.MethodPost, "/v1/rider/subscriptions/checkout", `{"plan_code":"basic_199","method":"cash"}`, uid)
	if w.Code != http.StatusBadRequest || errorCode(t, w) != service.CodePaymentMethodInvalid {
		t.Fatalf("checkout with cash: %d %s", w.Code, w.Body.String())
	}
	// Without a payments client the paid plan answers 503, never activates.
	w = partnerServe(rg.r, http.MethodPost, "/v1/rider/subscriptions/checkout", `{"plan_code":"basic_199","method":"upi"}`, uid)
	if w.Code != http.StatusServiceUnavailable || errorCode(t, w) != service.CodePaymentsUnavailable {
		t.Fatalf("checkout without payments: %d %s", w.Code, w.Body.String())
	}
	w = partnerServe(rg.r, http.MethodGet, "/v1/rider/subscriptions/me/payment", "", uid)
	if w.Code != http.StatusNotFound {
		t.Fatalf("payment status with no checkout: %d %s", w.Code, w.Body.String())
	}
	// The trial activates instantly on the route, once.
	w = partnerServe(rg.r, http.MethodPost, "/v1/rider/subscriptions/checkout", `{"plan_code":"trial_7d"}`, uid)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"trial"`) {
		t.Fatalf("trial checkout: %d %s", w.Code, w.Body.String())
	}
	w = partnerServe(rg.r, http.MethodPost, "/v1/rider/subscriptions/checkout", `{"plan_code":"trial_7d"}`, uid)
	if w.Code != http.StatusConflict || errorCode(t, w) != service.CodeTrialAlreadyUsed {
		t.Fatalf("second trial: %d %s", w.Code, w.Body.String())
	}
	// Onboarding names what is missing.
	w = partnerServe(rg.r, http.MethodGet, "/v1/rider/partners/me/onboarding", "", uid)
	if w.Code != http.StatusOK {
		t.Fatalf("onboarding: %d %s", w.Code, w.Body.String())
	}
	var env struct {
		Data service.OnboardingStatus `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Status != service.OnboardingIncomplete || len(env.Data.Missing) != 4 || len(env.Data.Pending) != 0 {
		t.Fatalf("onboarding = %+v", env.Data)
	}
	_ = svc
}

// The captain app sends {document_type, document_number, file_url:
// "media://<id>", media_id}. A profile_photo (the selfie) without media_id
// is refused with SELFIE_MEDIA_REQUIRED; with it the submission reaches the
// server-side face check (here a mock comparer above the threshold, after
// the DigiLocker mock supplied the licence photo).
func TestPostMyDocument_SelfieMediaID(t *testing.T) {
	_, pool := setupAdminTokenIT(t)
	svc := service.New(store.New(pool), nil, service.Config{})
	svc.SetDigiLockerClient(digilocker.NewMockClient())
	svc.SetFaceComparer(&service.MockFaceComparer{Similarity: 96}, 80)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(svc, adminTestInternalKey).RegisterRoutes(r)

	uid := uuid.New()
	p, err := svc.CreatePartnerProfile(context.Background(), uid, service.CreatePartnerRequest{PartnerType: "individual_driver", FullName: "Selfie Partner", Phone: "+91" + uuid.NewString()[:10]})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteAadhaarFlow(context.Background(), uid, p.ID, "code-selfie", "state"); err != nil {
		t.Fatal(err)
	}

	w := partnerServe(r, http.MethodPost, "/v1/rider/partners/me/documents", `{"document_type":"profile_photo","file_url":"media://none"}`, uid)
	if w.Code != http.StatusBadRequest || errorCode(t, w) != service.CodeSelfieMediaRequired {
		t.Fatalf("selfie without media_id: %d %s", w.Code, w.Body.String())
	}
	if n := itCount(t, pool, `SELECT COUNT(*) FROM rider_partner_documents WHERE partner_id = $1 AND document_type = 'profile_photo'`, p.ID); n != 0 {
		t.Fatalf("a selfie row was written without media_id: %d", n)
	}
	// A DL upload (not the selfie) needs no media_id: it waits for a human.
	w = partnerServe(r, http.MethodPost, "/v1/rider/partners/me/documents", `{"document_type":"pan","document_number":"ABCDE1234F","file_url":"media://pan"}`, uid)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"status":"pending"`) {
		t.Fatalf("pan upload: %d %s", w.Code, w.Body.String())
	}

	media := uuid.New()
	w = partnerServe(r, http.MethodPost, "/v1/rider/partners/me/documents",
		`{"document_type":"profile_photo","file_url":"media://`+media.String()+`","media_id":"`+media.String()+`"}`, uid)
	if w.Code != http.StatusCreated {
		t.Fatalf("selfie with media_id: %d %s", w.Code, w.Body.String())
	}
	var env struct {
		Data store.PartnerDocument `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.MediaID == nil || *env.Data.MediaID != media {
		t.Fatalf("media_id not threaded: %+v", env.Data)
	}
	// The face check ran (mock 96 >= 80): the selfie is verified automatically.
	got, err := svc.Store().GetPartnerDocument(context.Background(), env.Data.ID)
	if err != nil || got.Status != "approved" || got.VerifiedByActor == nil || *got.VerifiedByActor != store.VerifiedByAuto || got.AutoCheckDetail == nil || !strings.Contains(*got.AutoCheckDetail, "face_compare") {
		t.Fatalf("selfie after face check = %+v %v", got, err)
	}
}

func itCount(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", sql, err)
	}
	return n
}
