// Contract fixtures (testdata/contracts/*.json): the exact bytes the
// handlers write, produced by full round trips through the real route table
// over an in-memory store whose ids are the dev seed's (devseed.ID). The
// request id is pinned to "fixture" and the clock to 2026-10-04T06:30:00Z.
//
// Web (postbook-ui src/features/doorstep) and Android copy these files
// byte-identical. Regenerate with UPDATE_CONTRACT_FIXTURES=1 after an
// intentional shape change and tell those lanes.
package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atpost/doorstep-service/internal/devseed"
	"github.com/atpost/doorstep-service/internal/service"
	"github.com/atpost/doorstep-service/internal/tax"
	"github.com/atpost/shared/kyc"
	"github.com/atpost/shared/o11y/trace"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	testInternalKey = "doorstep-test-internal-key"
	fixtureReqID    = "fixture"
)

// testGSTIN is a synthetic, checksum-valid Telangana GSTIN (not a real one).
func testGSTIN(t *testing.T) string {
	t.Helper()
	base := "36ZZZCZ0000Z1Z"
	d, err := kyc.GSTINCheckDigit(base)
	if err != nil {
		t.Fatal(err)
	}
	return base + string(d)
}

type rig struct {
	t     *testing.T
	r     *gin.Engine
	store *fakeStore
	admin *servicetoken.Signer // admin-service, registered for AdminPermissions
	other *servicetoken.Signer // a registered caller that is not admin-service
	rogue *servicetoken.Signer // claims admin-service with an unregistered key
	v     *servicetoken.Verifier
	actor uuid.UUID
}

func newRig(t *testing.T) *rig {
	t.Helper()
	aPub, aPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	oPub, oPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	_, rPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"SERVICE_CALLERS":                            "admin-service,notification-service",
		"SERVICE_CALLER_ADMIN_SERVICE_KID":           "a1",
		"SERVICE_CALLER_ADMIN_SERVICE_PUBKEY":        aPub,
		"SERVICE_CALLER_ADMIN_SERVICE_OPS":           strings.Join(AdminPermissions, ","),
		"SERVICE_CALLER_NOTIFICATION_SERVICE_KID":    "n1",
		"SERVICE_CALLER_NOTIFICATION_SERVICE_PUBKEY": oPub,
		// Deliberately over-granted: a caller other than admin-service must
		// still be refused on admin routes.
		"SERVICE_CALLER_NOTIFICATION_SERVICE_OPS": PermCatalogueRead + "," + PermCatalogueWrite,
	}
	v, err := ServiceCallersFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	mk := func(iss, kid, priv string) *servicetoken.Signer {
		s, err := servicetoken.NewSignerFromBase64(iss, kid, priv)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	rg := &rig{t: t, store: newFakeStore(), v: v, actor: uuid.MustParse("9e1f0c3a-6b2d-4a8e-b7c5-1d2e3f4a5b6c"),
		admin: mk(IssuerAdminService, "a1", aPriv), other: mk("notification-service", "n1", oPriv), rogue: mk(IssuerAdminService, "a1", rPriv)}
	rg.r = rg.router(testInternalKey, v)
	return rg
}

func (rg *rig) router(key string, v *servicetoken.Verifier) *gin.Engine {
	tc, err := tax.NewGST(nil, testGSTIN(rg.t))
	if err != nil {
		rg.t.Fatal(err)
	}
	svc := service.New(rg.store, tc, 15*time.Minute).
		WithClock(func() time.Time { return fixtureNow }, func() uuid.UUID { return fixtureQuoteID })
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(trace.WithRequestID(c.Request.Context(), fixtureReqID))
		c.Next()
	})
	r.GET("/healthz", func(c *gin.Context) { c.Status(http.StatusOK) })
	New(svc, key).WithServiceAuth(v).RegisterRoutes(r)
	return r
}

type req struct {
	method, path string
	body         string
	headers      map[string]string
	noKey        bool
}

func (rg *rig) do(q req) *httptest.ResponseRecorder {
	var body *strings.Reader
	if q.body != "" {
		body = strings.NewReader(q.body)
	} else {
		body = strings.NewReader("")
	}
	r := httptest.NewRequest(q.method, q.path, body)
	if q.body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if !q.noKey {
		r.Header.Set("X-Internal-Service-Key", testInternalKey)
	}
	for k, v := range q.headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	rg.r.ServeHTTP(w, r)
	return w
}

func (rg *rig) user() map[string]string { return map[string]string{"X-User-Id": fixtureUser.String()} }

// token mints an admin-service token for perm acting as rg.actor.
func (rg *rig) token(s *servicetoken.Signer, aud, perm string, actor string) map[string]string {
	rg.t.Helper()
	opts := []servicetoken.MintOption{}
	if actor != "" {
		opts = append(opts, servicetoken.WithActor(actor))
	}
	tok, err := s.Mint(aud, "admin-console", []string{perm}, nil, time.Minute, opts...)
	if err != nil {
		rg.t.Fatal(err)
	}
	return map[string]string{ServiceAuthHeader: "Bearer " + tok}
}

func (rg *rig) adminHeaders(perm string) map[string]string {
	return rg.token(rg.admin, AudienceDoorstep, perm, rg.actor.String())
}

func assertFixture(t *testing.T, name string, w *httptest.ResponseRecorder, wantStatus int) {
	t.Helper()
	if w.Code != wantStatus {
		t.Fatalf("%s: status %d, want %d; body %s", name, w.Code, wantStatus, w.Body.String())
	}
	got := w.Body.Bytes()
	if !json.Valid(got) {
		t.Fatalf("%s: body is not JSON: %s", name, got)
	}
	path := filepath.Join("testdata", "contracts", name)
	if os.Getenv("UPDATE_CONTRACT_FIXTURES") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v (run with UPDATE_CONTRACT_FIXTURES=1 to create it)", name, err)
	}
	if !bytes.Equal(bytes.TrimRight(want, "\r\n"), bytes.TrimRight(got, "\r\n")) {
		t.Fatalf("%s changed.\nwant: %s\ngot:  %s", name, want, got)
	}
}

func decodeData(t *testing.T, w *httptest.ResponseRecorder, dst any) {
	t.Helper()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(env.Data, dst); err != nil {
		t.Fatal(err)
	}
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) (string, map[string]any) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("not an error envelope: %s", w.Body.String())
	}
	return env.Error.Code, env.Error.Details
}

// ---- catalogue ----

func TestContract_Catalogue(t *testing.T) {
	rg := newRig(t)
	assertFixture(t, "catalogue_get_200.json", rg.do(req{method: "GET", path: "/v1/doorstep/catalogue?city=hyd"}), 200)
	assertFixture(t, "catalogue_get_404_city.json", rg.do(req{method: "GET", path: "/v1/doorstep/catalogue?city=BLR"}), 404)
}

func TestContract_Category(t *testing.T) {
	rg := newRig(t)
	assertFixture(t, "category_get_200.json", rg.do(req{method: "GET", path: "/v1/doorstep/categories/salon-women?city=HYD"}), 200)
	w := rg.do(req{method: "GET", path: "/v1/doorstep/categories/gardening?city=HYD"})
	if code, _ := errorCode(t, w); w.Code != 404 || code != "DOORSTEP_CATEGORY_NOT_FOUND" {
		t.Fatalf("unknown category: %d %s", w.Code, w.Body.String())
	}
}

func TestContract_Service(t *testing.T) {
	rg := newRig(t)
	w := rg.do(req{method: "GET", path: "/v1/doorstep/services/" + devseed.ID("service", "salon-women/facial").String() + "?city=HYD"})
	assertFixture(t, "service_get_200.json", w, 200)
	// The inactive option and the unpriced add-on are never offered.
	if s := w.Body.String(); strings.Contains(s, "O3+") || strings.Contains(s, "Unpriced") {
		t.Fatalf("inactive or unpriced item offered: %s", s)
	}
	w = rg.do(req{method: "GET", path: "/v1/doorstep/services/" + uuid.NewString() + "?city=HYD"})
	if code, _ := errorCode(t, w); w.Code != 404 || code != "DOORSTEP_SERVICE_NOT_FOUND" {
		t.Fatalf("unknown service: %d %s", w.Code, w.Body.String())
	}
}

func TestContract_Serviceability(t *testing.T) {
	rg := newRig(t)
	// HITEC City (Cyber Towers).
	assertFixture(t, "serviceability_in_200.json",
		rg.do(req{method: "POST", path: "/v1/doorstep/serviceability", body: `{"lat":17.4504,"lng":78.3808}`}), 200)
	// Secunderabad: east of both zones.
	assertFixture(t, "serviceability_out_200.json",
		rg.do(req{method: "POST", path: "/v1/doorstep/serviceability", body: `{"lat":17.4399,"lng":78.4983}`}), 200)
	w := rg.do(req{method: "POST", path: "/v1/doorstep/serviceability", body: `{"lat":95,"lng":78.4}`})
	if code, _ := errorCode(t, w); w.Code != 400 || code != "DOORSTEP_INVALID_REQUEST" {
		t.Fatalf("bad lat: %d %s", w.Code, w.Body.String())
	}
	w = rg.do(req{method: "POST", path: "/v1/doorstep/serviceability", body: `{"lat":17.4,"lng":78.4,"extra":1}`})
	if w.Code != 400 {
		t.Fatalf("unknown field accepted: %d", w.Code)
	}
}

// ---- quotes ----

func quoteBody(service, option string, addons []string, lat, lng float64, qty int) string {
	b := map[string]any{"service_id": service, "option_id": option, "lat": lat, "lng": lng}
	if qty > 0 {
		b["quantity"] = qty
	}
	if addons != nil {
		list := []map[string]string{}
		for _, a := range addons {
			list = append(list, map[string]string{"addon_id": a})
		}
		b["addons"] = list
	}
	raw, _ := json.Marshal(b)
	return string(raw)
}

var (
	facial     = "salon-women/facial"
	kitchen    = "home-cleaning/kitchen-deep-cleaning"
	idOf       = func(kind, key string) string { return devseed.ID(kind, key).String() }
	inZoneLat  = 17.4504
	inZoneLng  = 78.3808
	outZoneLat = 17.4399
	outZoneLng = 78.4983
)

func TestContract_Quote201(t *testing.T) {
	rg := newRig(t)
	// Kitchen deep cleaning, occupied, + chimney: HOME_CLEANING_VIA_ECO at 18%
	// through shared/gst (platform GSTIN configured).
	w := rg.do(req{method: "POST", path: "/v1/doorstep/quotes", headers: rg.user(),
		body: quoteBody(idOf("service", kitchen), idOf("option", kitchen+"/occupied"),
			[]string{idOf("addon", kitchen+"/appliances/chimney")}, inZoneLat, inZoneLng, 0)})
	assertFixture(t, "quote_post_201.json", w, 201)
	var q struct {
		TotalPaise, TaxablePaise, TaxPaise int64
	}
	decodeData(t, w, &struct {
		Total   *int64 `json:"total_paise"`
		Taxable *int64 `json:"taxable_paise"`
		Tax     *int64 `json:"tax_paise"`
	}{&q.TotalPaise, &q.TaxablePaise, &q.TaxPaise})
	if q.TotalPaise != 179900+44900 || q.TaxablePaise+q.TaxPaise != q.TotalPaise {
		t.Fatalf("totals %+v", q)
	}

	// Salon (BEAUTY_SALON_REGISTERED, 5%, estimated): gold facial + charcoal
	// mask + head massage.
	w = rg.do(req{method: "POST", path: "/v1/doorstep/quotes", headers: rg.user(),
		body: quoteBody(idOf("service", facial), idOf("option", facial+"/gold"),
			[]string{idOf("addon", facial+"/mask/charcoal"), idOf("addon", facial+"/add-ons/head-massage")}, inZoneLat, inZoneLng, 1)})
	assertFixture(t, "quote_post_201_salon.json", w, 201)

	// GET returns the stored quote; another user's id is a 404.
	get := rg.do(req{method: "GET", path: "/v1/doorstep/quotes/" + fixtureQuoteID.String(), headers: rg.user()})
	if get.Code != 200 || !strings.Contains(get.Body.String(), `"status":"open"`) {
		t.Fatalf("get quote: %d %s", get.Code, get.Body.String())
	}
}

func TestContract_Quote422(t *testing.T) {
	rg := newRig(t)
	cases := []struct {
		fixture, code string
		body          string
	}{
		{"quote_post_422_addon_min.json", "DOORSTEP_ADDON_INVALID", // required mask group left empty
			quoteBody(idOf("service", facial), idOf("option", facial+"/gold"), []string{}, inZoneLat, inZoneLng, 0)},
		{"quote_post_422_addon_max.json", "DOORSTEP_ADDON_INVALID", // both masks in a pick-one group
			quoteBody(idOf("service", facial), idOf("option", facial+"/gold"),
				[]string{idOf("addon", facial+"/mask/peel-off"), idOf("addon", facial+"/mask/charcoal")}, inZoneLat, inZoneLng, 0)},
		{"quote_post_422_outside_area.json", "DOORSTEP_OUTSIDE_SERVICE_AREA",
			quoteBody(idOf("service", facial), idOf("option", facial+"/gold"),
				[]string{idOf("addon", facial+"/mask/peel-off")}, outZoneLat, outZoneLng, 0)},
		{"quote_post_422_option_invalid.json", "DOORSTEP_OPTION_INVALID", // kitchen option on the facial
			quoteBody(idOf("service", facial), idOf("option", kitchen+"/occupied"),
				[]string{idOf("addon", facial+"/mask/peel-off")}, inZoneLat, inZoneLng, 0)},
		{"quote_post_422_quantity.json", "DOORSTEP_QUANTITY_INVALID",
			quoteBody(idOf("service", facial), idOf("option", facial+"/gold"),
				[]string{idOf("addon", facial+"/mask/peel-off")}, inZoneLat, inZoneLng, 2)},
	}
	for _, c := range cases {
		w := rg.do(req{method: "POST", path: "/v1/doorstep/quotes", headers: rg.user(), body: c.body})
		assertFixture(t, c.fixture, w, 422)
		if code, _ := errorCode(t, w); code != c.code {
			t.Errorf("%s: code %s, want %s", c.fixture, code, c.code)
		}
	}
}

// ---- admin ----

func TestContract_AdminSamples(t *testing.T) {
	rg := newRig(t)
	assertFixture(t, "admin_cities_list_200.json",
		rg.do(req{method: "GET", path: InternalAdminPrefix + "/cities", headers: rg.adminHeaders(PermCatalogueRead), noKey: true}), 200)
	assertFixture(t, "admin_category_post_201.json",
		rg.do(req{method: "POST", path: InternalAdminPrefix + "/categories", headers: rg.adminHeaders(PermCatalogueWrite), noKey: true,
			body: `{"slug":"laundry","name":"Laundry","description":"Wash and iron at home","family":"HOME_CLEANING","sort_order":110}`}), 201)
	assertFixture(t, "admin_zone_post_201.json",
		rg.do(req{method: "POST", path: InternalAdminPrefix + "/zones", headers: rg.adminHeaders(PermConfigWrite), noKey: true,
			body: `{"city_code":"HYD","name":"Kondapur","slug":"kondapur","boundary":{"type":"Polygon","coordinates":[[[78.35,17.45],[78.37,17.45],[78.37,17.47],[78.35,17.47],[78.35,17.45]]]}}`}), 201)
	assertFixture(t, "admin_zone_post_422.json",
		rg.do(req{method: "POST", path: InternalAdminPrefix + "/zones", headers: rg.adminHeaders(PermConfigWrite), noKey: true,
			body: `{"city_code":"HYD","name":"Open ring","slug":"open-ring","boundary":{"type":"Polygon","coordinates":[[[78.35,17.45],[78.37,17.45],[78.37,17.47],[78.35,17.47]]]}}`}), 422)
	priceBody := `{"city_code":"HYD","item_kind":"option","item_id":"` + idOf("option", facial+"/gold") +
		`","price_paise":134900,"mrp_paise":149900,"effective_from":"2026-10-05T00:00:00Z"}`
	assertFixture(t, "admin_price_post_201.json",
		rg.do(req{method: "POST", path: InternalAdminPrefix + "/prices", headers: rg.adminHeaders(PermCatalogueWrite), noKey: true, body: priceBody}), 201)
	assertFixture(t, "admin_price_post_409.json",
		rg.do(req{method: "POST", path: InternalAdminPrefix + "/prices", headers: rg.adminHeaders(PermCatalogueWrite), noKey: true,
			body: strings.Replace(priceBody, "134900", "77700", 1)}), 409)
	assertFixture(t, "admin_token_403_scope.json",
		rg.do(req{method: "POST", path: InternalAdminPrefix + "/prices", headers: rg.adminHeaders(PermCatalogueRead), noKey: true, body: priceBody}), 403)

	// Every admitted write was audited under the token's actor and the
	// route's permission.
	for _, a := range rg.store.actors {
		if a.UserID != rg.actor || (a.Permission != PermCatalogueWrite && a.Permission != PermConfigWrite) {
			t.Errorf("audited actor %+v", a)
		}
	}
	if len(rg.store.actors) != 4 {
		t.Fatalf("audited writes %d, want 4 (category, zone, two prices)", len(rg.store.actors))
	}
}
