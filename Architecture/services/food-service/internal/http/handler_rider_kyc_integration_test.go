package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/atpost/food-service/database"
	"github.com/atpost/food-service/internal/digilocker"
	"github.com/atpost/food-service/internal/foodpii"
	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Wave 1 B4 end to end: router -> service -> real store on TEST_PG_DSN (a
// database whose name ends in _test), with the mock DigiLocker provider and
// the dev authorize route. Run with -p 1. Identifiers are synthetic.

type kycIT struct {
	r      *gin.Engine
	pool   *pgxpool.Pool
	crypto *foodpii.Crypto
	svc    *service.Service
}

func riderKYCIntegration(t *testing.T) kycIT {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping food-service rider KYC integration tests")
	}
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatal("TEST_PG_DSN must parse and name a database ending in _test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	crypto, err := foodpii.New(ctx, []foodpii.VersionedKey{{Version: 1, Key: bytes.Repeat([]byte{4}, 32)}}, []byte("rider-kyc-integration-salt-00001"))
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(postgres.New(pool)).WithPII(crypto).WithDigiLocker(digilocker.Settings{
		Mode: digilocker.ModeMock, Client: digilocker.NewMockClient(), PublicBaseURL: ctPublicBase, AppLinkURL: ctAppLink,
	})
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(svc).WithDigiLockerDevRoutes("dev", true).RegisterRoutes(router)
	return kycIT{r: router, pool: pool, crypto: crypto, svc: svc}
}

func (it kycIT) rider(t *testing.T, vehicle string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	userID := uuid.New()
	p, err := postgres.New(it.pool).UpsertDeliveryPartner(context.Background(), userID, postgres.DeliveryPartnerInput{
		FullName: "IT Rider", Phone: "+9190" + userID.String()[:8], VehicleType: vehicle, City: "Bengaluru",
	})
	if err != nil {
		t.Fatalf("seed rider: %v", err)
	}
	return userID, p.ID
}

func rawGet(r *gin.Engine, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func (it kycIT) start(t *testing.T, user uuid.UUID) string {
	t.Helper()
	rec := doJSON(it.r, http.MethodPost, "/v1/food/delivery/kyc/digilocker/start", `{}`, user, false)
	expectStatus(t, rec, http.StatusOK, "start")
	return dataField(t, rec, "state").(string)
}

// browse follows start -> dev authorize -> public return -> App Link, and
// returns the code and state the app would receive.
func (it kycIT) browse(t *testing.T, user uuid.UUID) (string, string) {
	t.Helper()
	rec := doJSON(it.r, http.MethodPost, "/v1/food/delivery/kyc/digilocker/start", `{}`, user, false)
	expectStatus(t, rec, http.StatusOK, "start")
	state := dataField(t, rec, "state").(string)
	authorize, err := url.Parse(dataField(t, rec, "authorize_url").(string))
	if err != nil || authorize.Path != digilocker.DevAuthorizePath {
		t.Fatalf("authorize url is not the dev route: %v", err)
	}
	rec = rawGet(it.r, authorize.RequestURI())
	expectStatus(t, rec, http.StatusFound, "dev authorize")
	ret, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || ret.Path != digilocker.ReturnPath || ret.Query().Get("state") != state {
		t.Fatalf("dev authorize did not send the browser to the return route")
	}
	rec = rawGet(it.r, ret.RequestURI())
	expectStatus(t, rec, http.StatusFound, "public return")
	app, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || app.Scheme+"://"+app.Host+app.Path != ctAppLink || app.Query().Get("state") != state {
		t.Fatalf("return route did not reach the App Link")
	}
	code := app.Query().Get("code")
	if !strings.HasPrefix(code, "mock-") {
		t.Fatalf("App Link has no mock code")
	}
	return code, state
}

func (it kycIT) callback(user uuid.UUID, code, state string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(map[string]string{"code": code, "state": state})
	return doJSON(it.r, http.MethodPost, "/v1/food/delivery/kyc/digilocker/callback", string(b), user, false)
}

// mockNumbers is what the mock provider issues for code.
func mockNumbers(t *testing.T, code string) (dl, rc string) {
	t.Helper()
	ctx := context.Background()
	m := digilocker.NewMockClient()
	s, err := m.ExchangeCode(ctx, code, "any-verifier")
	if err != nil {
		t.Fatal(err)
	}
	docs, err := m.IssuedDocuments(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		switch d.Kind {
		case digilocker.KindDrivingLicence:
			dl = d.Number
		case digilocker.KindVehicleRC:
			rc = d.Number
		}
	}
	return dl, rc
}

func (it kycIT) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := it.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// partnerRowsText is every KYC-related row of one partner as JSON text.
func (it kycIT) partnerRowsText(t *testing.T, partnerID uuid.UUID) string {
	t.Helper()
	var text string
	if err := it.pool.QueryRow(context.Background(), `
		SELECT COALESCE((SELECT json_agg(d)::text FROM food.delivery_partner_documents d WHERE d.delivery_partner_id = $1), '')
			|| COALESCE((SELECT json_agg(k)::text FROM food.delivery_partner_kyc_checks k WHERE k.partner_id = $1), '')
			|| COALESCE((SELECT json_agg(s)::text FROM food.digilocker_auth_states s WHERE s.partner_id = $1), '')
	`, partnerID).Scan(&text); err != nil {
		t.Fatal(err)
	}
	return text
}

type checkRow struct {
	kind, ref, hash string
	hasValidity     bool
}

// checkRows and documentRows read into slices with the rows closed by defer,
// so a failed assertion can never strand a pooled connection (pool.Close in
// cleanup would then wait forever).
func (it kycIT) checkRows(t *testing.T, partnerID uuid.UUID) []checkRow {
	t.Helper()
	rows, err := it.pool.Query(context.Background(), `
		SELECT kind, assertion_ref, doc_type_hash, valid_until IS NOT NULL
		FROM food.delivery_partner_kyc_checks WHERE partner_id = $1 ORDER BY kind`, partnerID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []checkRow
	for rows.Next() {
		var c checkRow
		if err := rows.Scan(&c.kind, &c.ref, &c.hash, &c.hasValidity); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

type documentRow struct {
	docType, status        string
	number, lookup, masked *string
	sealed                 []byte
}

func (it kycIT) documentRows(t *testing.T, partnerID uuid.UUID, excludeType string) []documentRow {
	t.Helper()
	rows, err := it.pool.Query(context.Background(), `
		SELECT document_type, status::text, document_number, number_lookup, number_masked, number_sealed
		FROM food.delivery_partner_documents
		WHERE delivery_partner_id = $1 AND document_type <> $2
		ORDER BY document_type, created_at`, partnerID, excludeType)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []documentRow
	for rows.Next() {
		var d documentRow
		if err := rows.Scan(&d.docType, &d.status, &d.number, &d.lookup, &d.masked, &d.sealed); err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func missingList(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	env := decodeEnvelope(t, rec)
	var raw any
	if env.Error != nil {
		raw = env.Error.Details["missing"]
	} else {
		raw = dataField(t, rec, "missing")
	}
	items, _ := raw.([]any)
	parts := make([]string, 0, len(items))
	for _, i := range items {
		parts = append(parts, fmt.Sprint(i))
	}
	return strings.Join(parts, ",")
}

func TestDigiLockerMockFlowStoresSealedDocumentsAndAnAadhaarReferenceOnly(t *testing.T) {
	it := riderKYCIntegration(t)
	ctx := context.Background()
	user, partnerID := it.rider(t, "MOTORCYCLE")
	code, state := it.browse(t, user)
	dl, rc := mockNumbers(t, code)

	rec := it.callback(user, code, state)
	expectStatus(t, rec, http.StatusOK, "callback")
	if got := missingList(t, rec); got != "selfie,payout_account" {
		t.Fatalf("missing after DigiLocker = %s", got)
	}
	for _, plain := range []string{dl, rc, state, code} {
		if strings.Contains(rec.Body.String(), plain) {
			t.Fatal("the callback response carries a plaintext value")
		}
	}

	checks := it.checkRows(t, partnerID)
	kinds := []string{}
	for _, c := range checks {
		kinds = append(kinds, c.kind)
		if c.kind == "AADHAAR" && (c.ref == "" || c.hash != digilocker.HashDocumentType("ADHAR") || c.hasValidity) {
			t.Fatal("the Aadhaar check is not a reference plus a hashed type")
		}
	}
	if strings.Join(kinds, ",") != "AADHAAR,DRIVING_LICENCE,VEHICLE_RC" {
		t.Fatalf("checks = %v", kinds)
	}

	seen := map[string]string{}
	for _, d := range it.documentRows(t, partnerID, "") {
		opened, err := it.crypto.OpenPartnerDocumentNumber(ctx, d.sealed)
		if err != nil || d.number != nil || d.lookup == nil || d.status != "APPROVED" || d.masked == nil {
			t.Fatalf("%s is not stored sealed-only and approved", d.docType)
		}
		if *d.masked != "****"+opened[len(opened)-4:] {
			t.Fatalf("%s mask is wrong", d.docType)
		}
		seen[d.docType] = opened
	}
	if seen["DRIVING_LICENCE"] != dl || seen["VEHICLE_RC"] != rc || len(seen) != 2 {
		t.Fatal("stored DL and RC do not open to the issued numbers")
	}
	text := it.partnerRowsText(t, partnerID)
	for _, plain := range []string{dl, rc, state} {
		if strings.Contains(text, plain) {
			t.Fatal("a stored row carries a plaintext DL, RC or state")
		}
	}
	if it.count(t, `SELECT COUNT(*) FROM food.digilocker_auth_states WHERE state_hash = $1 AND partner_id = $2 AND consumed_at IS NOT NULL`, digilocker.HashState(state), partnerID) != 1 {
		t.Fatal("the state was not stored hashed and consumed")
	}
}

func TestDigiLockerStateReuseRefused(t *testing.T) {
	it := riderKYCIntegration(t)
	user, partnerID := it.rider(t, "SCOOTER")
	code, state := it.browse(t, user)
	expectStatus(t, it.callback(user, code, state), http.StatusOK, "first callback")
	before := it.count(t, `SELECT COUNT(*) FROM food.delivery_partner_kyc_checks WHERE partner_id = $1`, partnerID)
	rec := it.callback(user, code, state)
	expectStatus(t, rec, http.StatusConflict, "reused state")
	if errorCode(t, rec) != "FOOD_DIGILOCKER_STATE_USED" {
		t.Fatalf("code = %s", errorCode(t, rec))
	}
	if after := it.count(t, `SELECT COUNT(*) FROM food.delivery_partner_kyc_checks WHERE partner_id = $1`, partnerID); after != before {
		t.Fatal("a reused state wrote checks")
	}
}

func TestDigiLockerExpiredStateRefused(t *testing.T) {
	it := riderKYCIntegration(t)
	user, partnerID := it.rider(t, "MOTORCYCLE")
	state := it.start(t, user)
	if _, err := it.pool.Exec(context.Background(), `UPDATE food.digilocker_auth_states SET expires_at = NOW() - INTERVAL '1 minute' WHERE state_hash = $1`, digilocker.HashState(state)); err != nil {
		t.Fatal(err)
	}
	rec := it.callback(user, "mock-expired-"+uuid.NewString(), state)
	expectStatus(t, rec, http.StatusGone, "expired state")
	if errorCode(t, rec) != "FOOD_DIGILOCKER_STATE_EXPIRED" {
		t.Fatalf("code = %s", errorCode(t, rec))
	}
	if it.count(t, `SELECT COUNT(*) FROM food.delivery_partner_kyc_checks WHERE partner_id = $1`, partnerID)+
		it.count(t, `SELECT COUNT(*) FROM food.delivery_partner_documents WHERE delivery_partner_id = $1`, partnerID) != 0 {
		t.Fatal("an expired state wrote rows")
	}
}

func TestDigiLockerAnotherPartnersStateRefused(t *testing.T) {
	it := riderKYCIntegration(t)
	owner, _ := it.rider(t, "MOTORCYCLE")
	stranger, strangerPartner := it.rider(t, "MOTORCYCLE")
	code, state := it.browse(t, owner)

	for _, who := range []uuid.UUID{stranger, uuid.New()} {
		rec := it.callback(who, code, state)
		expectStatus(t, rec, http.StatusForbidden, "someone else's state")
		if errorCode(t, rec) != "FOOD_DIGILOCKER_STATE_NOT_YOURS" {
			t.Fatalf("code = %s", errorCode(t, rec))
		}
	}
	if it.count(t, `SELECT COUNT(*) FROM food.delivery_partner_kyc_checks WHERE partner_id = $1`, strangerPartner) != 0 {
		t.Fatal("another partner's state wrote checks for the stranger")
	}
	// The stranger's attempt did not burn the owner's state.
	expectStatus(t, it.callback(owner, code, state), http.StatusOK, "owner callback after the stranger")
}

func TestDigiLockerDuplicateDrivingLicenceRefused(t *testing.T) {
	it := riderKYCIntegration(t)
	first, firstPartner := it.rider(t, "MOTORCYCLE")
	second, secondPartner := it.rider(t, "MOTORCYCLE")
	shared := "mock-duplicate-" + uuid.NewString()

	expectStatus(t, it.callback(first, shared, it.start(t, first)), http.StatusOK, "first partner")
	rec := it.callback(second, shared, it.start(t, second))
	expectStatus(t, rec, http.StatusConflict, "second partner with the same DL")
	if errorCode(t, rec) != "FOOD_DOCUMENT_NUMBER_IN_USE" {
		t.Fatalf("code = %s", errorCode(t, rec))
	}
	if it.count(t, `SELECT COUNT(*) FROM food.delivery_partner_kyc_checks WHERE partner_id = $1`, secondPartner)+
		it.count(t, `SELECT COUNT(*) FROM food.delivery_partner_documents WHERE delivery_partner_id = $1`, secondPartner) != 0 {
		t.Fatal("the refused callback left rows behind")
	}
	if it.count(t, `SELECT COUNT(*) FROM food.delivery_partner_documents WHERE delivery_partner_id = $1 AND document_type IN ('DRIVING_LICENCE','VEHICLE_RC')`, firstPartner) != 2 {
		t.Fatal("the first partner lost their documents")
	}
	// The same partner re-verifying their own licence is not a duplicate.
	expectStatus(t, it.callback(first, shared, it.start(t, first)), http.StatusOK, "first partner again")
}

func itUniqueNumbers() (dl, rc string) {
	u := uuid.New()
	n := uint64(u[0])<<24 | uint64(u[1])<<16 | uint64(u[2])<<8 | uint64(u[3])
	dl = fmt.Sprintf("MH%02d2019%07d", 1+uint64(u[4])%99, n%10000000)
	rc = fmt.Sprintf("MH%02d%c%c%04d", 1+uint64(u[5])%99, 'A'+u[6]%26, 'A'+u[7]%26, uint64(u[8])<<8|uint64(u[9])%10000)
	return dl, rc[:4] + rc[4:6] + fmt.Sprintf("%04d", (uint64(u[8])<<8|uint64(u[9]))%10000)
}

func TestDeliveryDocumentsAndBackfillLeaveNoPlaintext(t *testing.T) {
	it := riderKYCIntegration(t)
	ctx := context.Background()
	user, partnerID := it.rider(t, "MOTORCYCLE")
	other, _ := it.rider(t, "MOTORCYCLE")
	dl, _ := itUniqueNumbers()
	policy := "POL-" + uuid.NewString()[:8]

	rec := doJSON(it.r, http.MethodPost, "/v1/food/delivery/documents", `{"document_type":"DRIVING_LICENCE","document_number":"`+dl+`","media_id":"`+uuid.NewString()+`"}`, user, false)
	expectStatus(t, rec, http.StatusCreated, "manual DL")
	if dataField(t, rec, "number_masked") != "****"+dl[len(dl)-4:] || dataField(t, rec, "status") != "PENDING" {
		t.Fatalf("manual DL response = %s", rec.Body.String())
	}
	expectStatus(t, doJSON(it.r, http.MethodPost, "/v1/food/delivery/documents", `{"document_type":"INSURANCE","document_number":"`+policy+`"}`, user, false), http.StatusCreated, "other document")
	rec = doJSON(it.r, http.MethodPost, "/v1/food/delivery/documents", `{"document_type":"DRIVING_LICENCE","document_number":"`+dl+`"}`, other, false)
	expectStatus(t, rec, http.StatusConflict, "another partner's DL")
	before := it.count(t, `SELECT COUNT(*) FROM food.delivery_partner_documents WHERE delivery_partner_id = $1`, partnerID)
	aadhaar := kycSynthAadhaar(t)
	rec = doJSON(it.r, http.MethodPost, "/v1/food/delivery/documents", `{"document_type":"INSURANCE","document_number":"`+aadhaar+`"}`, user, false)
	expectStatus(t, rec, http.StatusUnprocessableEntity, "Aadhaar number")
	if errorCode(t, rec) != "AADHAAR_NOT_ALLOWED" || it.count(t, `SELECT COUNT(*) FROM food.delivery_partner_documents WHERE delivery_partner_id = $1`, partnerID) != before {
		t.Fatal("an Aadhaar number was accepted")
	}
	if it.count(t, `SELECT COUNT(*) FROM food.delivery_partner_documents WHERE delivery_partner_id = $1 AND document_number IS NOT NULL`, partnerID) != 0 {
		t.Fatal("a write stored a plaintext document number")
	}

	// Pre-B4 rows: plaintext numbers written by the old route.
	legacyDL, legacyRC := itUniqueNumbers()
	legacy := map[string]string{"DRIVING_LICENCE": legacyDL, "VEHICLE_RC": legacyRC, "DRIVING_LICENSE": "legacy " + legacyDL, "PAN_CARD": "LEGACY-" + uuid.NewString()[:6]}
	for docType, number := range legacy {
		if _, err := it.pool.Exec(ctx, `INSERT INTO food.delivery_partner_documents (delivery_partner_id, document_type, document_number) VALUES ($1, $2, $3)`, partnerID, docType, number); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := it.pool.Exec(ctx, `INSERT INTO food.delivery_partner_documents (delivery_partner_id, document_type, document_number) VALUES ($1, 'AADHAAR', $2)`, partnerID, aadhaar); err != nil {
		t.Fatal(err)
	}
	res, err := it.svc.BackfillDeliveryDocumentNumbers(ctx)
	if err != nil || res.Sealed < len(legacy) || res.AadhaarRefused < 1 {
		t.Fatalf("backfill = %+v, %v", res, err)
	}
	// Every value this partner's rows were written with, by document type:
	// the manual uploads (sealed on write) and the legacy rows (sealed by
	// the backfill).
	written := map[string][]string{"DRIVING_LICENCE": {dl}, "INSURANCE": {policy}}
	for docType, number := range legacy {
		written[docType] = append(written[docType], number)
	}
	for _, d := range it.documentRows(t, partnerID, "AADHAAR") {
		if d.number != nil {
			t.Fatalf("%s still holds a plaintext number after the backfill", d.docType)
		}
		got, err := it.crypto.OpenPartnerDocumentNumber(ctx, d.sealed)
		if err != nil || d.masked == nil || !strings.HasPrefix(*d.masked, "****") || !containsString(written[d.docType], got) {
			t.Fatalf("%s was not sealed faithfully", d.docType)
		}
	}
	text := it.partnerRowsText(t, partnerID)
	for _, plain := range []string{dl, policy, legacyDL, legacyRC, legacy["PAN_CARD"]} {
		if strings.Contains(text, plain) {
			t.Fatal("a plaintext number survived in a stored row")
		}
	}
	// An Aadhaar-shaped legacy number is neither sealed nor silently deleted.
	if it.count(t, `SELECT COUNT(*) FROM food.delivery_partner_documents WHERE delivery_partner_id = $1 AND document_type = 'AADHAAR' AND document_number IS NOT NULL AND number_sealed IS NULL`, partnerID) != 1 {
		t.Fatal("the backfill sealed or cleared an Aadhaar number")
	}
	again, err := it.svc.BackfillDeliveryDocumentNumbers(ctx)
	if err != nil || again.Sealed != 0 {
		t.Fatalf("second backfill = %+v, %v; want nothing left to seal", again, err)
	}
}

func TestAdminKYCViewIsMasked(t *testing.T) {
	it := riderKYCIntegration(t)
	user, partnerID := it.rider(t, "MOTORCYCLE")
	code, state := it.browse(t, user)
	expectStatus(t, it.callback(user, code, state), http.StatusOK, "callback")
	dl, rc := mockNumbers(t, code)

	rec := doJSON(it.r, http.MethodGet, "/v1/food/admin/delivery-partners/"+partnerID.String()+"/kyc", ``, uuid.New(), true)
	expectStatus(t, rec, http.StatusOK, "admin kyc")
	body := rec.Body.String()
	for _, hidden := range []string{dl, rc, "mock-ref", digilocker.HashDocumentType("ADHAR"), "assertion_ref", "number_lookup", "number_sealed"} {
		if strings.Contains(body, hidden) {
			t.Fatalf("the admin view exposes %q-class data", "a raw identifier")
		}
	}
	if !strings.Contains(body, `"number_masked":"****`+dl[len(dl)-4:]+`"`) || !strings.Contains(body, `"kind":"AADHAAR"`) {
		t.Fatalf("admin view = %s", body)
	}
	expectStatus(t, doJSON(it.r, http.MethodGet, "/v1/food/admin/delivery-partners/"+partnerID.String()+"/kyc", ``, uuid.New(), false), http.StatusForbidden, "non-admin")
}

func (it kycIT) approve(t *testing.T, partnerID uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(it.r, http.MethodPost, "/v1/food/admin/delivery-partners/"+partnerID.String()+"/approve", `{}`, uuid.New(), true)
}

func (it kycIT) approvedSelfie(t *testing.T, user, partnerID uuid.UUID) {
	t.Helper()
	rec := doJSON(it.r, http.MethodPost, "/v1/food/delivery/documents", `{"document_type":"SELFIE","media_id":"`+uuid.NewString()+`"}`, user, false)
	expectStatus(t, rec, http.StatusCreated, "selfie")
	docID := dataField(t, rec, "id").(string)
	expectStatus(t, doJSON(it.r, http.MethodPost, "/v1/food/admin/delivery-partners/"+partnerID.String()+"/documents/"+docID+"/decide", `{"decision":"APPROVED"}`, uuid.New(), true), http.StatusOK, "approve selfie")
}

func TestDeliveryPartnerApprovalGate(t *testing.T) {
	it := riderKYCIntegration(t)
	ctx := context.Background()
	user, partnerID := it.rider(t, "MOTORCYCLE")
	code, state := it.browse(t, user)
	expectStatus(t, it.callback(user, code, state), http.StatusOK, "callback")

	rec := it.approve(t, partnerID)
	expectStatus(t, rec, http.StatusUnprocessableEntity, "approve without selfie or payout")
	if errorCode(t, rec) != "FOOD_DELIVERY_PARTNER_NOT_READY" || missingList(t, rec) != "selfie,payout_account" {
		t.Fatalf("refusal = %s", rec.Body.String())
	}
	if got := missingList(t, doJSON(it.r, http.MethodGet, "/v1/food/delivery/kyc/status", ``, user, false)); got != "selfie,payout_account" {
		t.Fatalf("readiness route missing = %s", got)
	}

	// A pending selfie does not count; a second selfie retires the first.
	expectStatus(t, doJSON(it.r, http.MethodPost, "/v1/food/delivery/documents", `{"document_type":"SELFIE","media_id":"`+uuid.NewString()+`"}`, user, false), http.StatusCreated, "pending selfie")
	expectStatus(t, doJSON(it.r, http.MethodPut, "/v1/food/delivery/payout-account", itPayout, user, false), http.StatusOK, "payout")
	if got := missingList(t, it.approve(t, partnerID)); got != "selfie" {
		t.Fatalf("with a pending selfie missing = %s", got)
	}
	it.approvedSelfie(t, user, partnerID)
	if it.count(t, `SELECT COUNT(*) FROM food.delivery_partner_documents WHERE delivery_partner_id = $1 AND document_type = 'SELFIE' AND status IN ('PENDING','APPROVED')`, partnerID) != 1 {
		t.Fatal("more than one active selfie")
	}

	// An admin rejecting the licence takes it back out of the gate.
	var dlDoc string
	if err := it.pool.QueryRow(ctx, `SELECT id::text FROM food.delivery_partner_documents WHERE delivery_partner_id = $1 AND document_type = 'DRIVING_LICENCE'`, partnerID).Scan(&dlDoc); err != nil {
		t.Fatal(err)
	}
	decide := "/v1/food/admin/delivery-partners/" + partnerID.String() + "/documents/" + dlDoc + "/decide"
	expectStatus(t, doJSON(it.r, http.MethodPost, decide, `{"decision":"REJECTED","reason":"photo does not match"}`, uuid.New(), true), http.StatusOK, "reject DL")
	if got := missingList(t, it.approve(t, partnerID)); got != "driving_licence" {
		t.Fatalf("after rejecting the DL missing = %s", got)
	}
	expectStatus(t, doJSON(it.r, http.MethodPost, decide, `{"decision":"APPROVED"}`, uuid.New(), true), http.StatusOK, "re-approve DL")

	expectStatus(t, it.approve(t, partnerID), http.StatusOK, "approve when complete")
	var status string
	if err := it.pool.QueryRow(ctx, `SELECT status::text FROM food.delivery_partners WHERE id = $1`, partnerID).Scan(&status); err != nil || status != "APPROVED" {
		t.Fatalf("status = %s, %v", status, err)
	}

	// The free-form status setter is not a way around the gate.
	_, pending := it.rider(t, "MOTORCYCLE")
	rec = doJSON(it.r, http.MethodPatch, "/v1/food/admin/delivery-partners/"+pending.String()+"/status", `{"status":"ACTIVE"}`, uuid.New(), true)
	expectStatus(t, rec, http.StatusUnprocessableEntity, "status setter to ACTIVE without verification")
}

func TestBicycleSkipsDrivingDocuments(t *testing.T) {
	it := riderKYCIntegration(t)
	ctx := context.Background()
	user, partnerID := it.rider(t, "BICYCLE")
	// An Aadhaar check only: no DL, no RC.
	if _, err := it.pool.Exec(ctx, `
		INSERT INTO food.delivery_partner_kyc_checks (partner_id, provider, kind, assertion_ref, doc_type_hash, verified_at)
		VALUES ($1, 'DIGILOCKER', 'AADHAAR', $2, $3, NOW())`, partnerID, "it-ref-"+uuid.NewString()[:8], digilocker.HashDocumentType("ADHAR")); err != nil {
		t.Fatal(err)
	}
	it.approvedSelfie(t, user, partnerID)
	expectStatus(t, doJSON(it.r, http.MethodPut, "/v1/food/delivery/payout-account", itPayout, user, false), http.StatusOK, "payout")

	if _, err := it.pool.Exec(ctx, `UPDATE food.delivery_partners SET vehicle_type = 'MOTORCYCLE' WHERE id = $1`, partnerID); err != nil {
		t.Fatal(err)
	}
	if got := missingList(t, it.approve(t, partnerID)); got != "driving_licence,vehicle_rc" {
		t.Fatalf("motorcycle without DL/RC missing = %s", got)
	}
	if _, err := it.pool.Exec(ctx, `UPDATE food.delivery_partners SET vehicle_type = 'BICYCLE' WHERE id = $1`, partnerID); err != nil {
		t.Fatal(err)
	}
	if got := missingList(t, doJSON(it.r, http.MethodGet, "/v1/food/delivery/kyc/status", ``, user, false)); got != "" {
		t.Fatalf("bicycle readiness missing = %s", got)
	}
	expectStatus(t, it.approve(t, partnerID), http.StatusOK, "approve a bicycle rider without DL or RC")
}

func TestRestaurantLocationRequiresAState(t *testing.T) {
	r, _, _ := onboardingIntegrationRouter(t, true)
	owner := uuid.New()
	_, base := createRestaurantViaAPI(t, r, owner)
	noState := `{"latitude":12.9716,"longitude":77.5946,"address_line1":"1 Test Lane","city":"Bengaluru","delivery_radius_km":5}`
	rec := doJSON(r, http.MethodPut, base+"/location", noState, owner, false)
	expectStatus(t, rec, http.StatusUnprocessableEntity, "location without state")
	if errorCode(t, rec) != "FOOD_STATE_REQUIRED" {
		t.Fatalf("code = %s", errorCode(t, rec))
	}
	rec = doJSON(r, http.MethodPut, base+"/location", strings.Replace(noState, `"city":"Bengaluru"`, `"city":"Bengaluru","state":"karnataka"`, 1), owner, false)
	expectStatus(t, rec, http.StatusOK, "location with state")
	if dataField(t, rec, "state") != "Karnataka" {
		t.Fatalf("stored state = %v", dataField(t, rec, "state"))
	}
	rec = doJSON(r, http.MethodPost, base+"/submit", `{}`, owner, false)
	if strings.Contains(missingList(t, rec), "state") {
		t.Fatalf("state still missing after the location route stored it: %s", missingList(t, rec))
	}
}
