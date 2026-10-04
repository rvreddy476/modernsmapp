// Package itest is doorstep-service's integration suite: the real store,
// PostGIS and the real route table on Postgres. It is the ONLY package that
// touches a database, so `go test ./...` never runs two suites against one
// database at once.
//
// Requires TEST_PG_DSN naming exactly doorstep_it_test (database.
// RequireTestDatabase refuses anything else); skipped when unset. Setup drops
// and recreates the doorstep schema, applies the migrations twice through the
// runner, executes every migration once more by hand (each must be
// re-runnable), and seeds
// Hyderabad twice (idempotent).
package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/doorstep-service/database"
	"github.com/atpost/doorstep-service/internal/devseed"
	doorstephttp "github.com/atpost/doorstep-service/internal/http"
	"github.com/atpost/doorstep-service/internal/service"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/doorstep-service/internal/tax"
	"github.com/atpost/shared/kyc"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	setupOnce sync.Once
	itPool    *pgxpool.Pool
	setupErr  error
)

func pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping doorstep-service integration tests")
	}
	if err := database.RequireTestDatabase(dsn); err != nil {
		t.Fatal(err)
	}
	setupOnce.Do(func() { itPool, setupErr = setup(dsn) })
	if setupErr != nil {
		t.Fatalf("setup: %v", setupErr)
	}
	return itPool
}

func setup(dsn string) (*pgxpool.Pool, error) {
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = "public" // as main does
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if _, err := p.Exec(ctx, `DROP SCHEMA IF EXISTS doorstep CASCADE`); err != nil {
		return nil, err
	}
	if _, err := p.Exec(ctx, `DO $$ BEGIN
		IF to_regclass('public.schema_migrations') IS NOT NULL THEN
			DELETE FROM public.schema_migrations WHERE service = 'doorstep-service';
		END IF; END $$`); err != nil {
		return nil, err
	}
	// Twice through the runner (second is a recorded no-op) ...
	for i := 0; i < 2; i++ {
		if err := database.BootstrapSchema(ctx, p); err != nil {
			return nil, err
		}
	}
	// ... and every file executed again by hand: each must be re-runnable.
	files, err := fs.Glob(database.Migrations, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		body, err := fs.ReadFile(database.Migrations, f)
		if err != nil {
			return nil, err
		}
		if _, err := p.Exec(ctx, string(body)); err != nil {
			return nil, fmt.Errorf("re-run %s: %w", f, err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := devseed.Seed(ctx, p); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// ---- HTTP rig over the real store ----

type itRig struct {
	t     *testing.T
	r     *gin.Engine
	admin *servicetoken.Signer
	actor uuid.UUID
	now   time.Time
}

const itKey = "doorstep-it-internal-key"

func gstin(t *testing.T) string {
	base := "36ZZZCZ0000Z1Z"
	d, err := kyc.GSTINCheckDigit(base)
	if err != nil {
		t.Fatal(err)
	}
	return base + string(d)
}

func newRig(t *testing.T, now time.Time) *itRig {
	t.Helper()
	p := pool(t)
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"SERVICE_CALLERS":                     "admin-service",
		"SERVICE_CALLER_ADMIN_SERVICE_KID":    "a1",
		"SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": pub,
		"SERVICE_CALLER_ADMIN_SERVICE_OPS":    strings.Join(doorstephttp.AdminPermissions, ","),
	}
	v, err := doorstephttp.ServiceCallersFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	signer, err := servicetoken.NewSignerFromBase64(doorstephttp.IssuerAdminService, "a1", priv)
	if err != nil {
		t.Fatal(err)
	}
	tc, err := tax.NewGST(nil, gstin(t))
	if err != nil {
		t.Fatal(err)
	}
	rg := &itRig{t: t, admin: signer, actor: uuid.New(), now: now}
	svc := service.New(store.New(p), tc, 15*time.Minute).WithClock(func() time.Time { return rg.now }, uuid.New)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	doorstephttp.New(svc, itKey).WithServiceAuth(v).RegisterRoutes(r)
	rg.r = r
	return rg
}

func (rg *itRig) call(method, path, body string, hdr map[string]string) (int, []byte) {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Internal-Service-Key", itKey)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	rg.r.ServeHTTP(w, r)
	return w.Code, w.Body.Bytes()
}

func (rg *itRig) customer(method, path, body string) (int, []byte) {
	return rg.call(method, path, body, map[string]string{"X-User-Id": "2d598287-eee7-40b4-a7f5-b46b9412e4e7"})
}

func (rg *itRig) adminCall(method, path, perm, body string) (int, []byte) {
	tok, err := rg.admin.Mint(doorstephttp.AudienceDoorstep, "admin-console", []string{perm}, nil, time.Minute,
		servicetoken.WithActor(rg.actor.String()))
	if err != nil {
		rg.t.Fatal(err)
	}
	return rg.call(method, doorstephttp.InternalAdminPrefix+path, body, map[string]string{doorstephttp.ServiceAuthHeader: "Bearer " + tok})
}

func data(t *testing.T, body []byte, dst any) {
	t.Helper()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil || len(env.Data) == 0 {
		t.Fatalf("no data in %s", body)
	}
	if err := json.Unmarshal(env.Data, dst); err != nil {
		t.Fatalf("decode %s: %v", env.Data, err)
	}
}

func errCode(body []byte) string {
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	return env.Error.Code
}

func id(kind, key string) string { return devseed.ID(kind, key).String() }

// ---- schema ----

func TestSchemaAndExclusionConstraint(t *testing.T) {
	p := pool(t)
	ctx := context.Background()
	var tables int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'doorstep'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables < 45 {
		t.Fatalf("doorstep has %d tables, want the full data model (>= 45)", tables)
	}
	var applied int
	files, _ := fs.Glob(database.Migrations, "migrations/*.sql")
	if err := p.QueryRow(ctx, `SELECT count(*) FROM public.schema_migrations WHERE service = 'doorstep-service'`).Scan(&applied); err != nil || applied != len(files) {
		t.Fatalf("schema_migrations rows %d err %v, want one per migration file (%d) after two runs", applied, err, len(files))
	}

	// Calendar: one professional, two overlapping ACTIVE blocks are impossible.
	proID := uuid.New()
	if _, err := p.Exec(ctx, `INSERT INTO doorstep.professionals (id, user_id, display_name, city_code) VALUES ($1, $2, 'IT Pro', 'HYD')`, proID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	block := func(from, to string, active bool) error {
		_, err := p.Exec(ctx, `INSERT INTO doorstep.pro_calendar_blocks (pro_id, kind, during, active, expires_at)
			VALUES ($1, 'hold', tstzrange($2::timestamptz, $3::timestamptz, '[)'), $4, NOW() + interval '10 minutes')`, proID, from, to, active)
		return err
	}
	if err := block("2026-10-10T10:00:00Z", "2026-10-10T11:00:00Z", true); err != nil {
		t.Fatal(err)
	}
	err := block("2026-10-10T10:30:00Z", "2026-10-10T11:30:00Z", true)
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23P01" || pg.ConstraintName != "ex_doorstep_pro_calendar_no_overlap" {
		t.Fatalf("overlapping active block: %v, want exclusion violation", err)
	}
	if err := block("2026-10-10T11:00:00Z", "2026-10-10T12:00:00Z", true); err != nil {
		t.Fatalf("adjacent [) block refused: %v", err)
	}
	if err := block("2026-10-10T10:15:00Z", "2026-10-10T10:45:00Z", false); err != nil {
		t.Fatalf("inactive (released) overlapping block refused: %v", err)
	}
	// Professional statuses are the ones identityrolebackfill reads.
	if _, err := p.Exec(ctx, `UPDATE doorstep.professionals SET status = 'pending_verification' WHERE id = $1`, proID); err != nil {
		t.Fatalf("pending_verification refused: %v", err)
	}
	if _, err := p.Exec(ctx, `UPDATE doorstep.professionals SET status = 'pending_review' WHERE id = $1`, proID); err == nil {
		t.Fatal("unknown professional status accepted")
	}
	// Salon categories may only take catalogue add-ons as extras.
	if _, err := p.Exec(ctx, `UPDATE doorstep.categories SET extras_policy = 'rate_card' WHERE slug = 'salon-men'`); err == nil {
		t.Fatal("salon with a rate card accepted")
	}
}

// ---- serviceability (point in zone, ST_Covers) ----

func TestServiceabilityPointInZone(t *testing.T) {
	rg := newRig(t, time.Now())
	cases := []struct {
		name     string
		lat, lng float64
		zone     string
	}{
		{"HITEC City", 17.4504, 78.3808, "Gachibowli - HITEC City"},
		{"Gachibowli", 17.4401, 78.3489, "Gachibowli - HITEC City"},
		{"Banjara Hills", 17.4156, 78.4347, "Banjara - Jubilee Hills"},
		{"shared edge goes to the lowest name", 17.42, 78.40, "Banjara - Jubilee Hills"},
		{"west boundary is covered", 17.44, 78.33, "Gachibowli - HITEC City"},
		{"Secunderabad (east)", 17.4399, 78.4983, ""},
		{"Charminar (south)", 17.3616, 78.4747, ""},
		{"Bengaluru", 12.9716, 77.5946, ""},
	}
	for _, c := range cases {
		status, body := rg.call("POST", "/v1/doorstep/serviceability", `{"lat":`+ftoa(c.lat)+`,"lng":`+ftoa(c.lng)+`}`, nil)
		var s struct {
			Serviceable bool `json:"serviceable"`
			Zone        *struct {
				Name string `json:"name"`
			} `json:"zone"`
			Reason *string `json:"reason"`
		}
		if status != 200 {
			t.Fatalf("%s: %d %s", c.name, status, body)
		}
		data(t, body, &s)
		got := ""
		if s.Zone != nil {
			got = s.Zone.Name
		}
		if got != c.zone || s.Serviceable != (c.zone != "") || (c.zone == "" && (s.Reason == nil || *s.Reason != "OUTSIDE_SERVICE_AREA")) {
			t.Errorf("%s: zone %q serviceable %v reason %v, want %q", c.name, got, s.Serviceable, s.Reason, c.zone)
		}
	}
	// An inactive zone serves nobody.
	p := pool(t)
	ctx := context.Background()
	if _, err := p.Exec(ctx, `UPDATE doorstep.zones SET active = FALSE WHERE slug = 'west-hitec-gachibowli'`); err != nil {
		t.Fatal(err)
	}
	defer p.Exec(ctx, `UPDATE doorstep.zones SET active = TRUE WHERE slug = 'west-hitec-gachibowli'`)
	_, body := rg.call("POST", "/v1/doorstep/serviceability", `{"lat":17.4504,"lng":78.3808}`, nil)
	if !strings.Contains(string(body), `"serviceable":false`) {
		t.Fatalf("inactive zone still serves: %s", body)
	}
}

func ftoa(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

// ---- catalogue from the seed ----

func TestCatalogueFromSeed(t *testing.T) {
	rg := newRig(t, time.Now())
	status, body := rg.call("GET", "/v1/doorstep/catalogue?city=HYD", "", nil)
	var cat struct {
		Categories []struct {
			Slug         string `json:"slug"`
			GenderRule   string `json:"gender_rule"`
			ServiceCount int    `json:"service_count"`
			Starting     int64  `json:"starting_price_paise"`
		} `json:"categories"`
	}
	if status != 200 {
		t.Fatalf("%d %s", status, body)
	}
	data(t, body, &cat)
	wantCats, wantSvcs, _, _, _ := devseed.Counts()
	total := 0
	for _, c := range cat.Categories {
		total += c.ServiceCount
		if c.Starting <= 0 {
			t.Errorf("%s starting price %d", c.Slug, c.Starting)
		}
		if (c.Slug == "salon-women" && c.GenderRule != "female_pros_only") || (c.Slug == "salon-men" && c.GenderRule != "male_pros_only") {
			t.Errorf("%s gender rule %s", c.Slug, c.GenderRule)
		}
	}
	if len(cat.Categories) != wantCats || total != wantSvcs {
		t.Fatalf("categories %d services %d, want %d and %d", len(cat.Categories), total, wantCats, wantSvcs)
	}
	if status, body := rg.call("GET", "/v1/doorstep/categories/salon-women?city=HYD", "", nil); status != 200 || !strings.Contains(string(body), `"slug":"facial"`) {
		t.Fatalf("category page: %d %s", status, body)
	}
	status, body = rg.call("GET", "/v1/doorstep/services/"+id("service", "salon-women/facial")+"?city=HYD", "", nil)
	if status != 200 || !strings.Contains(string(body), `"name":"Choose a mask"`) || !strings.Contains(string(body), `"mrp_paise":149900`) {
		t.Fatalf("service page: %d %s", status, body)
	}
	if status, body := rg.call("GET", "/v1/doorstep/catalogue?city=BLR", "", nil); status != 404 || errCode(body) != "DOORSTEP_CITY_NOT_FOUND" {
		t.Fatalf("unknown city: %d %s", status, body)
	}
}

// ---- quotes ----

func quote(service, option string, addons []string, lat, lng float64) string {
	b := map[string]any{"service_id": service, "option_id": option, "lat": lat, "lng": lng}
	list := []map[string]string{}
	for _, a := range addons {
		list = append(list, map[string]string{"addon_id": a})
	}
	b["addons"] = list
	raw, _ := json.Marshal(b)
	return string(raw)
}

func TestQuotesOnTheDatabase(t *testing.T) {
	rg := newRig(t, time.Now())
	f := "salon-women/facial"
	status, body := rg.customer("POST", "/v1/doorstep/quotes",
		quote(id("service", f), id("option", f+"/gold"), []string{id("addon", f+"/mask/charcoal")}, 17.4504, 78.3808))
	if status != 201 {
		t.Fatalf("salon quote: %d %s", status, body)
	}
	var q struct {
		ID    string `json:"id"`
		Total int64  `json:"total_paise"`
		Lines []struct {
			PriceID string `json:"price_id"`
		} `json:"lines"`
	}
	data(t, body, &q)
	if q.Total != 129900+19900 || len(q.Lines) != 2 || q.Lines[0].PriceID != id("price", f+"/gold") {
		t.Fatalf("quote %+v", q)
	}
	status, got := rg.customer("GET", "/v1/doorstep/quotes/"+q.ID, "")
	if status != 200 || !bytes.Contains(got, []byte(`"total_paise":149800`)) || !bytes.Contains(got, []byte(`"status":"open"`)) {
		t.Fatalf("read back: %d %s", status, got)
	}
	// Another customer cannot read it.
	if status, _ := rg.call("GET", "/v1/doorstep/quotes/"+q.ID, "", map[string]string{"X-User-Id": uuid.NewString()}); status != 404 {
		t.Fatalf("foreign quote: %d", status)
	}
	// Expired once the clock passes expires_at.
	rg.now = time.Now().Add(16 * time.Minute)
	if _, got := rg.customer("GET", "/v1/doorstep/quotes/"+q.ID, ""); !bytes.Contains(got, []byte(`"status":"expired"`)) {
		t.Fatalf("expiry: %s", got)
	}
	rg.now = time.Now()

	for name, c := range map[string]struct {
		body string
		code string
	}{
		"required mask missing": {quote(id("service", f), id("option", f+"/gold"), nil, 17.4504, 78.3808), "DOORSTEP_ADDON_INVALID"},
		"two masks": {quote(id("service", f), id("option", f+"/gold"),
			[]string{id("addon", f+"/mask/charcoal"), id("addon", f+"/mask/peel-off")}, 17.4504, 78.3808), "DOORSTEP_ADDON_INVALID"},
		"add-on of another service": {quote(id("service", f), id("option", f+"/gold"),
			[]string{id("addon", f+"/mask/charcoal"), id("addon", "salon-men/haircut/add-ons/head-massage")}, 17.4504, 78.3808), "DOORSTEP_ADDON_INVALID"},
		"outside the zones": {quote(id("service", f), id("option", f+"/gold"),
			[]string{id("addon", f+"/mask/charcoal")}, 17.4399, 78.4983), "DOORSTEP_OUTSIDE_SERVICE_AREA"},
		"option of another service": {quote(id("service", f), id("option", "salon-men/haircut/haircut"),
			[]string{id("addon", f+"/mask/charcoal")}, 17.4504, 78.3808), "DOORSTEP_OPTION_INVALID"},
	} {
		status, body := rg.customer("POST", "/v1/doorstep/quotes", c.body)
		if status != 422 || errCode(body) != c.code {
			t.Errorf("%s: %d %s, want 422 %s", name, status, body, c.code)
		}
	}

	// HOME_CLEANING through shared/gst at 18%.
	k := "home-cleaning/kitchen-deep-cleaning"
	status, body = rg.customer("POST", "/v1/doorstep/quotes",
		quote(id("service", k), id("option", k+"/occupied"), []string{id("addon", k+"/appliances/chimney")}, 17.4156, 78.4347))
	if status != 201 || !bytes.Contains(body, []byte(`"gst_category":"HOME_CLEANING_VIA_ECO"`)) || !bytes.Contains(body, []byte(`"tax_paise":34292`)) {
		t.Fatalf("kitchen quote: %d %s", status, body)
	}
}

// ---- prices are effective-dated ----

func TestPriceEffectiveDating(t *testing.T) {
	rg := newRig(t, time.Now())
	f := "salon-men/haircut"
	opt := id("option", f+"/haircut")
	from := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	status, body := rg.adminCall("POST", "/prices", doorstephttp.PermCatalogueWrite,
		`{"city_code":"HYD","item_kind":"option","item_id":"`+opt+`","price_paise":34900,"effective_from":"`+from.Format(time.RFC3339)+`"}`)
	if status != 201 {
		t.Fatalf("new price: %d %s", status, body)
	}
	// The seeded row is now closed at `from`.
	_, body = rg.adminCall("GET", "/prices?city=HYD&item_id="+opt, doorstephttp.PermCatalogueRead, "")
	var list struct {
		Items []struct {
			Price int64      `json:"price_paise"`
			To    *time.Time `json:"effective_to"`
		} `json:"items"`
	}
	data(t, body, &list)
	if len(list.Items) != 2 || list.Items[0].Price != 34900 || list.Items[0].To != nil || list.Items[1].To == nil || !list.Items[1].To.Equal(from) {
		t.Fatalf("price history %+v", list.Items)
	}
	body2 := quote(id("service", f), opt, nil, 17.4504, 78.3808)
	_, now := rg.customer("POST", "/v1/doorstep/quotes", body2)
	rg.now = from.Add(time.Minute)
	_, later := rg.customer("POST", "/v1/doorstep/quotes", body2)
	rg.now = time.Now()
	if !bytes.Contains(now, []byte(`"total_paise":29900`)) || !bytes.Contains(later, []byte(`"total_paise":34900`)) {
		t.Fatalf("effective dating: now %s later %s", now, later)
	}
	// Starting at or before the open row's start overlaps.
	status, body = rg.adminCall("POST", "/prices", doorstephttp.PermCatalogueWrite,
		`{"city_code":"HYD","item_kind":"option","item_id":"`+opt+`","price_paise":35900,"effective_from":"`+from.Format(time.RFC3339)+`"}`)
	if status != 409 || errCode(body) != "DOORSTEP_PRICE_OVERLAP" {
		t.Fatalf("overlap: %d %s", status, body)
	}
	// Backdating is refused.
	status, _ = rg.adminCall("POST", "/prices", doorstephttp.PermCatalogueWrite,
		`{"city_code":"HYD","item_kind":"option","item_id":"`+opt+`","price_paise":35900,"effective_from":"2026-01-01T00:00:00Z"}`)
	if status != 400 {
		t.Fatalf("backdated price: %d", status)
	}
}

// ---- admin CRUD round trip, audited ----

func TestAdminCRUDRoundTrip(t *testing.T) {
	rg := newRig(t, time.Now())
	ctx := context.Background()
	w := doorstephttp.PermCatalogueWrite
	c := doorstephttp.PermConfigWrite
	r := doorstephttp.PermCatalogueRead
	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	ac := func(want int, what, method, path, perm, body string) []byte {
		t.Helper()
		status, out := rg.adminCall(method, path, perm, body)
		if status != want {
			t.Fatalf("%s: %d %s, want %d", what, status, out, want)
		}
		return out
	}
	idOf := func(body []byte) string {
		var v struct {
			ID string `json:"id"`
		}
		data(t, body, &v)
		return v.ID
	}

	// City (unique code per run: three letters from the suffix).
	code := "Q" + strings.ToUpper(string(rune('A'+int(suffix[0])%26))+string(rune('A'+int(suffix[1])%26)))
	if s, b := rg.adminCall("POST", "/cities", c, `{"code":"`+code+`","name":"Test city","state_code":"29"}`); s != 201 && errCode(b) != "DOORSTEP_CONFLICT" {
		t.Fatalf("create city: %d %s", s, b)
	}
	ac(200, "patch city", "PATCH", "/cities/"+code, c, `{"active":true,"max_jobs_per_day":5}`)
	ac(409, "duplicate city", "POST", "/cities", c, `{"code":"HYD","name":"Dup","state_code":"36"}`)
	ac(400, "bad state code", "POST", "/cities", c, `{"code":"ZZZ","name":"Bad","state_code":"99"}`)

	// Zones: a valid square, a self-intersecting bow-tie (PostGIS refuses).
	zone := idOf(ac(201, "zone", "POST", "/zones", c, `{"city_code":"`+code+`","name":"Z `+suffix+`","slug":"z-`+suffix+`",
		"boundary":{"type":"Polygon","coordinates":[[[77.0,12.0],[77.1,12.0],[77.1,12.1],[77.0,12.1],[77.0,12.0]]]}}`))
	ac(422, "bow-tie zone", "POST", "/zones", c, `{"city_code":"`+code+`","name":"Bow","slug":"bow-`+suffix+`",
		"boundary":{"type":"Polygon","coordinates":[[[77.0,12.0],[77.1,12.1],[77.1,12.0],[77.0,12.1],[77.0,12.0]]]}}`)
	ac(200, "patch zone", "PATCH", "/zones/"+zone, c, `{"travel_buffer_minutes":45}`)
	_, zb := rg.adminCall("GET", "/zones?city="+code, r, "")
	if !bytes.Contains(zb, []byte(`"travel_buffer_minutes":45`)) || !bytes.Contains(zb, []byte(`"MultiPolygon"`)) {
		t.Fatalf("zones list %s", zb)
	}

	// Category, skill, service, option, group, add-on.
	cat := idOf(ac(201, "category", "POST", "/categories", w, `{"slug":"cat-`+suffix+`","name":"Cat","family":"PAINTING"}`))
	ac(409, "duplicate slug", "POST", "/categories", w, `{"slug":"cat-`+suffix+`","name":"Dup","family":"PAINTING"}`)
	ac(200, "patch category", "PATCH", "/categories/"+cat, w, `{"active":true,"image_url":"https://cdn.example.test/c.png"}`)
	ac(200, "clear image", "PATCH", "/categories/"+cat, w, `{"image_url":null}`)
	salon := idOf(ac(201, "salon", "POST", "/categories", w, `{"slug":"salon-`+suffix+`","name":"Salon","family":"BEAUTY_SALON","gender_rule":"female_pros_only"}`))
	ac(400, "salon with rate card", "PATCH", "/categories/"+salon, w, `{"extras_policy":"rate_card"}`)
	skill := "sk_" + suffix
	ac(201, "skill", "POST", "/skills", w, `{"code":"`+skill+`","name":"Skill"}`)
	svc := idOf(ac(201, "service", "POST", "/services", w, `{"category_id":"`+cat+`","slug":"svc","name":"Svc","duration_minutes":60,"required_skill":"`+skill+`","active":true}`))
	ac(400, "crew service", "POST", "/services", w, `{"category_id":"`+cat+`","slug":"crew","name":"Crew","duration_minutes":60,"required_skill":"`+skill+`","crew_size":2}`)
	ac(400, "unknown skill", "POST", "/services", w, `{"category_id":"`+cat+`","slug":"noskill","name":"N","duration_minutes":60,"required_skill":"unknown_skill"}`)
	ac(200, "patch service", "PATCH", "/services/"+svc, w, `{"inclusions":["a","b"],"rework_days":10}`)
	opt := idOf(ac(201, "option", "POST", "/services/"+svc+"/options", w, `{"name":"Opt","duration_minutes":60,"is_default":true}`))
	ac(409, "second default", "POST", "/services/"+svc+"/options", w, `{"name":"Opt2","duration_minutes":60,"is_default":true}`)
	ac(404, "option of missing service", "POST", "/services/"+uuid.NewString()+"/options", w, `{"name":"x","duration_minutes":60}`)
	ac(200, "patch option", "PATCH", "/options/"+opt, w, `{"max_quantity":3}`)
	grp := idOf(ac(201, "group", "POST", "/services/"+svc+"/addon-groups", w, `{"name":"G","min_select":1,"max_select":2,"is_required":true}`))
	ac(400, "min > max", "POST", "/services/"+svc+"/addon-groups", w, `{"name":"Bad","min_select":3,"max_select":2}`)
	ac(400, "patch min > max (DB CHECK)", "PATCH", "/addon-groups/"+grp, w, `{"min_select":5}`)
	addon := idOf(ac(201, "addon", "POST", "/addon-groups/"+grp+"/addons", w, `{"name":"A","extra_duration_minutes":10}`))
	ac(200, "patch addon", "PATCH", "/addons/"+addon, w, `{"name":"A2"}`)
	_, tree := rg.adminCall("GET", "/services/"+svc, r, "")
	if !bytes.Contains(tree, []byte(`"name":"A2"`)) || !bytes.Contains(tree, []byte(`"max_quantity":3`)) {
		t.Fatalf("tree %s", tree)
	}
	ac(201, "addon price", "POST", "/prices", w, `{"city_code":"`+code+`","item_kind":"addon","item_id":"`+addon+`","price_paise":9900}`)
	ac(400, "addon id as option", "POST", "/prices", w, `{"city_code":"`+code+`","item_kind":"option","item_id":"`+addon+`","price_paise":9900}`)

	// Rate card, slot config, cancellation and commission rules.
	rc := idOf(ac(201, "rate card", "POST", "/rate-cards", w, `{"city_code":"`+code+`","category_id":"`+cat+`","code":"putty","name":"Putty","unit":"per_item","price_paise":150000}`))
	ac(200, "patch rate card", "PATCH", "/rate-cards/"+rc, w, `{"price_paise":160000}`)
	sc := idOf(ac(201, "slot config", "POST", "/slot-configs", c, `{"city_code":"`+code+`","open_time":"09:00","close_time":"18:00"}`))
	ac(409, "second city default", "POST", "/slot-configs", c, `{"city_code":"`+code+`","open_time":"09:00","close_time":"18:00"}`)
	ac(200, "patch slot", "PATCH", "/slot-configs/"+sc, c, `{"close_time":"19:30"}`)
	ac(400, "close before open", "POST", "/slot-configs", c, `{"city_code":"`+code+`","open_time":"19:00","close_time":"09:00"}`)
	cr := idOf(ac(201, "cancel rule", "POST", "/cancellation-rules", c, `{"city_code":"`+code+`","stage":"assigned","minutes_before_lt":180,"fee_paise":7500}`))
	if _, b := rg.adminCall("PATCH", "/cancellation-rules/"+cr, c, `{"minutes_before_lt":null}`); !bytes.Contains(b, []byte(`"minutes_before_lt":null`)) {
		t.Fatalf("clear minutes_before_lt: %s", b)
	}
	cm := idOf(ac(201, "commission", "POST", "/commission-rules", c, `{"city_code":"`+code+`","commission_bps":1800}`))
	ac(400, "commission too high", "PATCH", "/commission-rules/"+cm, c, `{"commission_bps":9000}`)
	ac(200, "patch commission", "PATCH", "/commission-rules/"+cm, c, `{"commission_bps":1500}`)
	for _, path := range []string{"/cities", "/categories", "/skills", "/services?category_id=" + cat, "/rate-cards?city=" + code,
		"/slot-configs?city=" + code, "/cancellation-rules?city=" + code, "/commission-rules?city=" + code} {
		ac(200, "list "+path, "GET", path, r, "")
	}

	// Every successful write has one audit row with the token's actor and
	// the route's permission; refused writes have none.
	var rows, wrongActor int
	if err := pool(t).QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE permission NOT IN ($2, $3))
		FROM doorstep.admin_audit_log WHERE actor_user_id = $1`, rg.actor, w, c).Scan(&rows, &wrongActor); err != nil {
		t.Fatal(err)
	}
	if rows < 24 || wrongActor != 0 {
		t.Fatalf("audit rows %d (wrong permission %d)", rows, wrongActor)
	}
	_, ab := rg.adminCall("GET", "/audit-logs?entity=city_price&limit=5", doorstephttp.PermAuditRead, "")
	if !bytes.Contains(ab, []byte(rg.actor.String())) {
		t.Fatalf("audit list %s", ab)
	}
}

func TestSeedIsIdempotent(t *testing.T) {
	p := pool(t)
	ctx := context.Background()
	count := func() int {
		var n int
		if err := p.QueryRow(ctx, `SELECT (SELECT count(*) FROM doorstep.services) + (SELECT count(*) FROM doorstep.city_prices)
			+ (SELECT count(*) FROM doorstep.addons) + (SELECT count(*) FROM doorstep.rate_cards)`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count()
	if err := devseed.Seed(ctx, p); err != nil {
		t.Fatal(err)
	}
	if after := count(); after != before {
		t.Fatalf("seed not idempotent: %d -> %d", before, after)
	}
	if !errors.Is(devseed.Run(ctx, p, func(string) string { return "prod" }), devseed.ErrNotDevelopment) {
		t.Fatal("seed ran in production")
	}
	_ = http.StatusOK
}
