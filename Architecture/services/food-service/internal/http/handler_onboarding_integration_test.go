package http

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/food-service/database"
	"github.com/atpost/food-service/internal/foodpii"
	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/kyc"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Full-stack onboarding tests: router -> service -> real store on
// TEST_PG_DSN (a database whose name ends in _test). Identifiers are
// synthetic.

const (
	itPAN     = "ZZZPZ0000Z"
	itAccount = "000123456789"
)

func onboardingIntegrationRouter(t *testing.T, withPII bool) (*gin.Engine, *pgxpool.Pool, *foodpii.Crypto) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping food-service HTTP integration tests")
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
	var crypto *foodpii.Crypto
	if withPII {
		crypto, err = foodpii.New(ctx, []foodpii.VersionedKey{{Version: 1, Key: bytes.Repeat([]byte{3}, 32)}}, []byte("http-integration-lookup-salt-001"))
		if err != nil {
			t.Fatal(err)
		}
	}
	svc := service.New(postgres.New(pool)).WithPII(crypto)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(svc).RegisterRoutes(router)
	return router, pool, crypto
}

type envelope struct {
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) envelope {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("response is not an envelope: %v (%s)", err, rec.Body.String())
	}
	return env
}

func dataField(t *testing.T, rec *httptest.ResponseRecorder, path ...string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(decodeEnvelope(t, rec).Data, &v); err != nil {
		t.Fatalf("data: %v", err)
	}
	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("data has no %s: %s", p, rec.Body.String())
		}
		v = m[p]
	}
	return v
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	env := decodeEnvelope(t, rec)
	if env.Error == nil {
		return ""
	}
	return env.Error.Code
}

func expectStatus(t *testing.T, rec *httptest.ResponseRecorder, status int, what string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("%s: status = %d, want %d (%s)", what, rec.Code, status, rec.Body.String())
	}
}

func createRestaurantViaAPI(t *testing.T, r *gin.Engine, owner uuid.UUID) (uuid.UUID, string) {
	t.Helper()
	rec := doJSON(r, http.MethodPost, "/v1/food/partner/restaurants",
		`{"name":"IT `+owner.String()[:8]+`","slug":"it-`+owner.String()+`","address_line1":"1 Test Lane","city":"Bengaluru"}`, owner, false)
	expectStatus(t, rec, http.StatusCreated, "create restaurant")
	id := uuid.MustParse(dataField(t, rec, "id").(string))
	return id, "/v1/food/partner/restaurants/" + id.String()
}

func itFSSAIBody(licence string) string {
	return `{"licence_number":"` + licence + `","expires_at":"` + time.Now().AddDate(1, 0, 0).Format("2006-01-02") + `","media_id":"` + uuid.NewString() + `"}`
}

func itGSTIN(t *testing.T) string {
	t.Helper()
	d, err := kyc.GSTINCheckDigit("29" + itPAN + "1Z")
	if err != nil {
		t.Fatal(err)
	}
	return "29" + itPAN + "1Z" + string(d)
}

const (
	itComplianceECO = `{"tax_category":"RESTAURANT_STANDALONE","legal_name":"IT Kitchens LLP","pan":"zzzpz0000z"}`
	itLocation      = `{"latitude":12.9716,"longitude":77.5946,"address_line1":"1 Test Lane","city":"Bengaluru","state":"Karnataka","google_place_id":"ChIJit","delivery_radius_km":5}`
	itHours         = `{"windows":[{"day_of_week":1,"opens_at":"10:00","closes_at":"22:00"},{"day_of_week":5,"opens_at":"18:00","closes_at":"02:00"}]}`
	itPayout        = `{"holder_name":"IT Holder","account_number":" ` + itAccount + ` ","ifsc":"hdfc0000053"}`
)

func seedAvailableMenuItem(t *testing.T, pool *pgxpool.Pool, restaurantID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	var categoryID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO food.menu_categories (restaurant_id, name, sort_order) VALUES ($1, 'Mains', 1) RETURNING id`, restaurantID).Scan(&categoryID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO food.menu_items (restaurant_id, category_id, name, base_price, food_type, preparation_minutes, is_available, is_active, tax_percentage)
		VALUES ($1, $2, 'IT Idli', 60, 'VEG', 10, TRUE, TRUE, 5)`, restaurantID, categoryID); err != nil {
		t.Fatal(err)
	}
}

func TestOnboardingRoutesAreOwnerOnly(t *testing.T) {
	r, pool, _ := onboardingIntegrationRouter(t, true)
	owner, stranger := uuid.New(), uuid.New()
	rid, base := createRestaurantViaAPI(t, r, owner)

	routes := []struct {
		method, path, body string
		ownerStatus        int
	}{
		{http.MethodPut, base + "/compliance", itComplianceECO, http.StatusOK},
		{http.MethodPut, base + "/location", itLocation, http.StatusOK},
		{http.MethodPut, base + "/operating-hours", itHours, http.StatusOK},
		{http.MethodPatch, base + "/accepting", `{"is_accepting_orders":false}`, http.StatusOK},
		{http.MethodPut, base + "/fssai", itFSSAIBody("10099999000000"), http.StatusOK},
		{http.MethodPut, base + "/payout-account", itPayout, http.StatusOK},
		{http.MethodGet, base + "/payout-account", ``, http.StatusOK},
		{http.MethodPost, base + "/submit", `{}`, http.StatusUnprocessableEntity},
	}
	for _, rt := range routes {
		rec := doJSON(r, rt.method, rt.path, rt.body, stranger, false)
		if rec.Code != http.StatusNotFound || errorCode(t, rec) != "FOOD_NOT_FOUND" {
			t.Errorf("stranger %s %s: %d %s", rt.method, rt.path, rec.Code, rec.Body.String())
		}
	}
	var touched bool
	if err := pool.QueryRow(context.Background(), `
		SELECT compliance_submitted_at IS NOT NULL OR pan_sealed IS NOT NULL OR latitude IS NOT NULL OR fssai_licence_number IS NOT NULL
			OR EXISTS (SELECT 1 FROM food.restaurant_operating_hours WHERE restaurant_id = $1)
			OR EXISTS (SELECT 1 FROM food.restaurant_documents WHERE restaurant_id = $1)
			OR EXISTS (SELECT 1 FROM food.payout_accounts WHERE owner_id = $1)
		FROM food.restaurants WHERE id = $1`, rid).Scan(&touched); err != nil {
		t.Fatal(err)
	}
	if touched {
		t.Fatalf("a stranger's requests wrote to the restaurant")
	}
	for _, rt := range routes {
		rec := doJSON(r, rt.method, rt.path, rt.body, owner, false)
		expectStatus(t, rec, rt.ownerStatus, "owner "+rt.method+" "+rt.path)
	}

	decide := "/v1/food/admin/restaurants/" + rid.String() + "/documents/" + uuid.NewString() + "/decide"
	expectStatus(t, doJSON(r, http.MethodPost, decide, `{"decision":"APPROVED"}`, owner, false), http.StatusForbidden, "decide without admin scope")
	expectStatus(t, doJSON(r, http.MethodPost, decide, `{"decision":"APPROVED"}`, owner, true), http.StatusNotFound, "decide an unknown document")
}

func TestCompliancePANIsStoredSealedOnly(t *testing.T) {
	r, pool, crypto := onboardingIntegrationRouter(t, true)
	ctx := context.Background()
	panHex := hex.EncodeToString([]byte(itPAN))

	check := func(t *testing.T, rid uuid.UUID, body string, wantGSTIN bool) {
		var rowText, lookup, masked string
		var sealed []byte
		var gstin *string
		if err := pool.QueryRow(ctx, `
			SELECT row_to_json(r)::text, pan_sealed, pan_lookup, pan_masked, gstin
			FROM food.restaurants r WHERE id = $1`, rid).Scan(&rowText, &sealed, &lookup, &masked, &gstin); err != nil {
			t.Fatal(err)
		}
		if (gstin != nil) != wantGSTIN {
			t.Fatalf("gstin stored = %v, want %v", gstin != nil, wantGSTIN)
		}
		visible := rowText
		if gstin != nil {
			visible = strings.ReplaceAll(visible, *gstin, "<gstin>")
		}
		if strings.Contains(strings.ToUpper(visible), itPAN) || strings.Contains(strings.ToLower(visible), panHex) {
			t.Fatalf("the PAN appears in a restaurants column in clear")
		}
		if bytes.Contains(bytes.ToUpper(sealed), []byte(itPAN)) {
			t.Fatalf("pan_sealed holds the plaintext")
		}
		opened, err := crypto.OpenPAN(ctx, sealed)
		if err != nil || opened != itPAN {
			t.Fatalf("pan_sealed does not open to the PAN (err %v)", err)
		}
		expected, err := crypto.SealPAN(ctx, itPAN)
		if err != nil {
			t.Fatal(err)
		}
		if lookup != expected.Lookup || masked != "****000Z" {
			t.Fatalf("lookup matches = %v, masked = %q", lookup == expected.Lookup, masked)
		}
		if strings.Contains(strings.ToUpper(body), itPAN) {
			t.Fatalf("the response carries the PAN")
		}
	}

	t.Run("eco category without gstin", func(t *testing.T) {
		rid, base := createRestaurantViaAPI(t, r, uuid.New())
		rec := doJSON(r, http.MethodPut, base+"/compliance", itComplianceECO, ownerOf(t, pool, rid), false)
		expectStatus(t, rec, http.StatusOK, "compliance")
		check(t, rid, rec.Body.String(), false)
	})
	t.Run("supplier-liable category with gstin", func(t *testing.T) {
		rid, base := createRestaurantViaAPI(t, r, uuid.New())
		body := `{"tax_category":"OUTDOOR_CATERING","legal_name":"IT Caterers","pan":"` + itPAN + `","gstin":"` + strings.ToLower(itGSTIN(t)) + `"}`
		rec := doJSON(r, http.MethodPut, base+"/compliance", body, ownerOf(t, pool, rid), false)
		expectStatus(t, rec, http.StatusOK, "compliance")
		if got := dataField(t, rec, "gstin"); got != itGSTIN(t) {
			t.Fatalf("gstin not normalised")
		}
		visible := strings.ReplaceAll(rec.Body.String(), itGSTIN(t), "<gstin>")
		check(t, rid, visible, true)
	})
	t.Run("supplier-liable category without gstin writes nothing", func(t *testing.T) {
		rid, base := createRestaurantViaAPI(t, r, uuid.New())
		rec := doJSON(r, http.MethodPut, base+"/compliance", `{"tax_category":"OUTDOOR_CATERING","legal_name":"IT Caterers","pan":"`+itPAN+`"}`, ownerOf(t, pool, rid), false)
		expectStatus(t, rec, http.StatusUnprocessableEntity, "compliance without gstin")
		if errorCode(t, rec) != "FOOD_GSTIN_REQUIRED" {
			t.Fatalf("code = %s", errorCode(t, rec))
		}
		var sealed []byte
		if err := pool.QueryRow(ctx, `SELECT pan_sealed FROM food.restaurants WHERE id = $1`, rid).Scan(&sealed); err != nil {
			t.Fatal(err)
		}
		if sealed != nil {
			t.Fatalf("a refused submission wrote a PAN")
		}
	})
}

func ownerOf(t *testing.T, pool *pgxpool.Pool, rid uuid.UUID) uuid.UUID {
	t.Helper()
	var owner uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT owner_user_id FROM food.restaurants WHERE id = $1`, rid).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	return owner
}

func TestPayoutAccountIsSealedHashedAndMasked(t *testing.T) {
	r, pool, crypto := onboardingIntegrationRouter(t, true)
	ctx := context.Background()
	owner := uuid.New()
	rid, base := createRestaurantViaAPI(t, r, owner)
	expected, err := crypto.SealAccountNumber(ctx, itAccount)
	if err != nil {
		t.Fatal(err)
	}

	assertRow := func(t *testing.T, ownerType string, ownerID uuid.UUID) {
		var rowText, lookup, last4 string
		var sealed []byte
		if err := pool.QueryRow(ctx, `
			SELECT row_to_json(p)::text, account_sealed, account_lookup, account_last4
			FROM food.payout_accounts p WHERE owner_type = $1 AND owner_id = $2`, ownerType, ownerID).Scan(&rowText, &sealed, &lookup, &last4); err != nil {
			t.Fatal(err)
		}
		for _, leak := range []string{itAccount, itAccount[:8], hex.EncodeToString([]byte(itAccount))} {
			if strings.Contains(rowText, leak) {
				t.Fatalf("payout_accounts row carries the account number in clear")
			}
		}
		if bytes.Contains(sealed, []byte(itAccount)) {
			t.Fatalf("account_sealed holds the plaintext")
		}
		opened, err := crypto.OpenAccountNumber(ctx, sealed)
		if err != nil || opened != itAccount {
			t.Fatalf("account_sealed does not open (err %v)", err)
		}
		if lookup != expected.Lookup || last4 != "6789" {
			t.Fatalf("lookup matches = %v, last4 = %q", lookup == expected.Lookup, last4)
		}
	}
	assertMaskedBody := func(t *testing.T, rec *httptest.ResponseRecorder) {
		if dataField(t, rec, "account_number_masked") != "****6789" {
			t.Fatalf("masked = %v", dataField(t, rec, "account_number_masked"))
		}
		for _, leak := range []string{itAccount, itAccount[:8], "456789"} {
			if strings.Contains(rec.Body.String(), leak) {
				t.Fatalf("response shows more than the last four digits")
			}
		}
		if dataField(t, rec, "verification_status") != "NOT_VERIFIED" || dataField(t, rec, "verification_reason") != "verification_pending_ops" {
			t.Fatalf("verification = %s", rec.Body.String())
		}
	}

	put := doJSON(r, http.MethodPut, base+"/payout-account", itPayout, owner, false)
	expectStatus(t, put, http.StatusOK, "restaurant payout put")
	assertMaskedBody(t, put)
	assertRow(t, "RESTAURANT", rid)
	get := doJSON(r, http.MethodGet, base+"/payout-account", ``, owner, false)
	expectStatus(t, get, http.StatusOK, "restaurant payout get")
	assertMaskedBody(t, get)

	rider, other := uuid.New(), uuid.New()
	expectStatus(t, doJSON(r, http.MethodPut, "/v1/food/delivery/payout-account", itPayout, rider, false), http.StatusNotFound, "rider without a profile")
	expectStatus(t, doJSON(r, http.MethodPost, "/v1/food/delivery/profile", `{"full_name":"IT Rider","phone":"+919000000002"}`, rider, false), http.StatusOK, "rider profile")
	rput := doJSON(r, http.MethodPut, "/v1/food/delivery/payout-account", itPayout, rider, false)
	expectStatus(t, rput, http.StatusOK, "rider payout put")
	assertMaskedBody(t, rput)
	partnerID := uuid.MustParse(dataField(t, rput, "owner_id").(string))
	assertRow(t, "DELIVERY_PARTNER", partnerID)
	rget := doJSON(r, http.MethodGet, "/v1/food/delivery/payout-account", ``, rider, false)
	expectStatus(t, rget, http.StatusOK, "rider payout get")
	assertMaskedBody(t, rget)
	expectStatus(t, doJSON(r, http.MethodGet, "/v1/food/delivery/payout-account", ``, other, false), http.StatusNotFound, "another user's payout account")
}

func TestPIIRoutesAnswer503WithoutKeys(t *testing.T) {
	r, pool, _ := onboardingIntegrationRouter(t, false)
	owner := uuid.New()
	rid, base := createRestaurantViaAPI(t, r, owner)

	for _, rt := range []struct{ method, path, body string }{
		{http.MethodPut, base + "/compliance", itComplianceECO},
		{http.MethodPut, base + "/payout-account", itPayout},
		{http.MethodGet, base + "/payout-account", ``},
		{http.MethodPut, "/v1/food/delivery/payout-account", itPayout},
		{http.MethodGet, "/v1/food/delivery/payout-account", ``},
	} {
		rec := doJSON(r, rt.method, rt.path, rt.body, owner, false)
		if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "PII_NOT_CONFIGURED" {
			t.Errorf("%s %s: %d %s", rt.method, rt.path, rec.Code, rec.Body.String())
		}
	}
	var wrote bool
	if err := pool.QueryRow(context.Background(), `
		SELECT pan_sealed IS NOT NULL OR compliance_submitted_at IS NOT NULL
			OR EXISTS (SELECT 1 FROM food.payout_accounts WHERE owner_id = $1)
		FROM food.restaurants WHERE id = $1`, rid).Scan(&wrote); err != nil {
		t.Fatal(err)
	}
	if wrote {
		t.Fatalf("a PII route wrote while keys were absent")
	}
	// Non-PII onboarding keeps working without keys.
	expectStatus(t, doJSON(r, http.MethodPut, base+"/location", itLocation, owner, false), http.StatusOK, "location without keys")
}

func TestSubmitDecideApproveThroughRoutes(t *testing.T) {
	r, pool, _ := onboardingIntegrationRouter(t, true)
	owner := uuid.New()
	rid, base := createRestaurantViaAPI(t, r, owner)
	admin := uuid.New()

	rec := doJSON(r, http.MethodPost, base+"/submit", `{}`, owner, false)
	expectStatus(t, rec, http.StatusUnprocessableEntity, "empty submit")
	env := decodeEnvelope(t, rec)
	if env.Error.Code != "FOOD_RESTAURANT_NOT_READY" {
		t.Fatalf("code = %s", env.Error.Code)
	}
	missing, _ := env.Error.Details["missing"].([]any)
	if len(missing) != 7 || missing[1] != "state" {
		t.Fatalf("missing = %v", env.Error.Details["missing"])
	}

	expectStatus(t, doJSON(r, http.MethodPut, base+"/location", itLocation, owner, false), http.StatusOK, "location")
	expectStatus(t, doJSON(r, http.MethodPut, base+"/operating-hours", itHours, owner, false), http.StatusOK, "hours")
	expectStatus(t, doJSON(r, http.MethodPut, base+"/compliance", itComplianceECO, owner, false), http.StatusOK, "compliance")
	fs := doJSON(r, http.MethodPut, base+"/fssai", itFSSAIBody("10099999000000"), owner, false)
	expectStatus(t, fs, http.StatusOK, "fssai")
	docID := dataField(t, fs, "document", "id").(string)
	expectStatus(t, doJSON(r, http.MethodPut, base+"/payout-account", itPayout, owner, false), http.StatusOK, "payout")

	rec = doJSON(r, http.MethodPost, base+"/submit", `{}`, owner, false)
	expectStatus(t, rec, http.StatusUnprocessableEntity, "submit without a menu item")
	if m := decodeEnvelope(t, rec).Error.Details["missing"].([]any); len(m) != 1 || m[0] != "menu_item" {
		t.Fatalf("missing = %v", m)
	}
	seedAvailableMenuItem(t, pool, rid)
	rec = doJSON(r, http.MethodPost, base+"/submit", `{}`, owner, false)
	expectStatus(t, rec, http.StatusOK, "submit")
	if dataField(t, rec, "status") != "PENDING_REVIEW" {
		t.Fatalf("status = %v", dataField(t, rec, "status"))
	}
	expectStatus(t, doJSON(r, http.MethodPost, base+"/submit", `{}`, owner, false), http.StatusConflict, "second submit")

	approve := "/v1/food/admin/restaurants/" + rid.String() + "/approve"
	rec = doJSON(r, http.MethodPost, approve, `{}`, admin, true)
	expectStatus(t, rec, http.StatusUnprocessableEntity, "approve with a pending licence")
	if errorCode(t, rec) != "FOOD_FSSAI_REQUIRED" {
		t.Fatalf("code = %s", errorCode(t, rec))
	}
	rec = doJSON(r, http.MethodPost, "/v1/food/admin/restaurants/"+rid.String()+"/documents/"+docID+"/decide", `{"decision":"APPROVED"}`, admin, true)
	expectStatus(t, rec, http.StatusOK, "decide")
	if dataField(t, rec, "status") != "APPROVED" || dataField(t, rec, "verified_by") != admin.String() {
		t.Fatalf("decided = %s", rec.Body.String())
	}
	expectStatus(t, doJSON(r, http.MethodPost, approve, `{}`, admin, true), http.StatusOK, "approve")
	rec = doJSON(r, http.MethodPatch, base+"/accepting", `{"is_accepting_orders":true}`, owner, false)
	expectStatus(t, rec, http.StatusOK, "accepting")
	if dataField(t, rec, "is_accepting_orders") != true {
		t.Fatalf("accepting = %s", rec.Body.String())
	}
}

func TestExpiredFSSAIRefusedOverRoutes(t *testing.T) {
	r, pool, _ := onboardingIntegrationRouter(t, true)
	ctx := context.Background()
	owner, admin := uuid.New(), uuid.New()
	rid, _ := createRestaurantViaAPI(t, r, owner)

	var pendingExpired, approvedExpired uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO food.restaurant_documents (restaurant_id, document_type, document_number, status, expires_at)
		VALUES ($1, 'FSSAI', '10099999000000', 'PENDING', NOW() - INTERVAL '1 day') RETURNING id`, rid).Scan(&pendingExpired); err != nil {
		t.Fatal(err)
	}
	rec := doJSON(r, http.MethodPost, "/v1/food/admin/restaurants/"+rid.String()+"/documents/"+pendingExpired.String()+"/decide", `{"decision":"APPROVED"}`, admin, true)
	expectStatus(t, rec, http.StatusUnprocessableEntity, "approve an expired document")
	if errorCode(t, rec) != "FOOD_DOCUMENT_EXPIRED" {
		t.Fatalf("code = %s", errorCode(t, rec))
	}
	if err := pool.QueryRow(ctx, `INSERT INTO food.restaurant_documents (restaurant_id, document_type, document_number, status, expires_at)
		VALUES ($1, 'FSSAI', '10099999000000', 'APPROVED', NOW() - INTERVAL '1 minute') RETURNING id`, rid).Scan(&approvedExpired); err != nil {
		t.Fatal(err)
	}
	rec = doJSON(r, http.MethodPost, "/v1/food/admin/restaurants/"+rid.String()+"/approve", `{}`, admin, true)
	expectStatus(t, rec, http.StatusUnprocessableEntity, "approve with an expired licence")
	if errorCode(t, rec) != "FOOD_FSSAI_REQUIRED" {
		t.Fatalf("code = %s", errorCode(t, rec))
	}
	rec = doJSON(r, http.MethodPatch, "/v1/food/admin/restaurants/"+rid.String()+"/status", `{"status":"ACTIVE"}`, admin, true)
	expectStatus(t, rec, http.StatusUnprocessableEntity, "status ACTIVE with an expired licence")
}

func TestAadhaarShapedNumbersRefusedOverHTTP(t *testing.T) {
	r, pool, _ := onboardingIntegrationRouter(t, true)
	owner := uuid.New()
	rid, base := createRestaurantViaAPI(t, r, owner)
	var synthetic string
	for c := '0'; c <= '9'; c++ {
		if n := "45678901234" + string(c); kyc.LooksLikeAadhaar(n) {
			synthetic = n
		}
	}
	if synthetic == "" {
		t.Fatal("no synthetic check digit")
	}

	rec := doJSON(r, http.MethodPost, base+"/documents", `{"document_type":"TRADE_LICENCE","document_number":"`+synthetic+`"}`, owner, false)
	expectStatus(t, rec, http.StatusUnprocessableEntity, "generic document")
	if errorCode(t, rec) != "AADHAAR_NOT_ALLOWED" || strings.Contains(rec.Body.String(), synthetic) {
		t.Fatalf("generic document response = %s", errorCode(t, rec))
	}
	rec = doJSON(r, http.MethodPut, base+"/fssai", itFSSAIBody(synthetic), owner, false)
	expectStatus(t, rec, http.StatusUnprocessableEntity, "fssai")
	if errorCode(t, rec) != "AADHAAR_NOT_ALLOWED" || strings.Contains(rec.Body.String(), synthetic) {
		t.Fatalf("fssai response = %s", errorCode(t, rec))
	}
	rec = doJSON(r, http.MethodPost, base+"/documents", `{"document_type":"FSSAI","document_number":"10099999000000"}`, owner, false)
	expectStatus(t, rec, http.StatusUnprocessableEntity, "generic FSSAI upload")
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM food.restaurant_documents WHERE restaurant_id = $1`, rid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("refused documents were written")
	}
}
