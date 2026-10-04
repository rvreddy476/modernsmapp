package http

import (
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

// The route inventory is exact: a route added without updating this list
// (and the contract) fails here.
var wantRoutes = []string{
	"GET /healthz",
	"GET /v1/doorstep/catalogue",
	"GET /v1/doorstep/categories/:slug",
	"GET /v1/doorstep/services/:id",
	"POST /v1/doorstep/serviceability",
	"POST /v1/doorstep/quotes",
	"GET /v1/doorstep/quotes/:id",
	// Professional onboarding (A2).
	"POST /v1/doorstep/pro/apply",
	"GET /v1/doorstep/pro/me",
	"PATCH /v1/doorstep/pro/me",
	"GET /v1/doorstep/pro/readiness",
	"POST /v1/doorstep/pro/digilocker/start",
	"POST /v1/doorstep/pro/digilocker/callback",
	"POST /v1/doorstep/pro/selfie",
	"GET /v1/doorstep/pro/skills",
	"PUT /v1/doorstep/pro/me/skills",
	"POST /v1/doorstep/pro/me/skills/:code/certificate",
	"PUT /v1/doorstep/pro/me/area",
	"GET /v1/doorstep/pro/me/hours",
	"PUT /v1/doorstep/pro/me/hours",
	"GET /v1/doorstep/pro/me/days-off",
	"POST /v1/doorstep/pro/me/days-off",
	"DELETE /v1/doorstep/pro/me/days-off/:date",
	"PUT /v1/doorstep/pro/me/bank",
	"POST /v1/doorstep/pro/me/police-certificate",
	"POST /v1/doorstep/pro/me/agreement",
	"PUT /v1/doorstep/pro/me/pan",
	"POST /v1/doorstep/webhooks/background-check/:provider",
	"GET /v1/doorstep/internal/admin/cities",
	"POST /v1/doorstep/internal/admin/cities",
	"PATCH /v1/doorstep/internal/admin/cities/:code",
	"GET /v1/doorstep/internal/admin/zones",
	"POST /v1/doorstep/internal/admin/zones",
	"PATCH /v1/doorstep/internal/admin/zones/:id",
	"GET /v1/doorstep/internal/admin/categories",
	"POST /v1/doorstep/internal/admin/categories",
	"PATCH /v1/doorstep/internal/admin/categories/:id",
	"GET /v1/doorstep/internal/admin/skills",
	"POST /v1/doorstep/internal/admin/skills",
	"GET /v1/doorstep/internal/admin/services",
	"POST /v1/doorstep/internal/admin/services",
	"GET /v1/doorstep/internal/admin/services/:id",
	"PATCH /v1/doorstep/internal/admin/services/:id",
	"POST /v1/doorstep/internal/admin/services/:id/options",
	"PATCH /v1/doorstep/internal/admin/options/:id",
	"POST /v1/doorstep/internal/admin/services/:id/addon-groups",
	"PATCH /v1/doorstep/internal/admin/addon-groups/:id",
	"POST /v1/doorstep/internal/admin/addon-groups/:id/addons",
	"PATCH /v1/doorstep/internal/admin/addons/:id",
	"GET /v1/doorstep/internal/admin/prices",
	"POST /v1/doorstep/internal/admin/prices",
	"GET /v1/doorstep/internal/admin/rate-cards",
	"POST /v1/doorstep/internal/admin/rate-cards",
	"PATCH /v1/doorstep/internal/admin/rate-cards/:id",
	"GET /v1/doorstep/internal/admin/slot-configs",
	"POST /v1/doorstep/internal/admin/slot-configs",
	"PATCH /v1/doorstep/internal/admin/slot-configs/:id",
	"GET /v1/doorstep/internal/admin/cancellation-rules",
	"POST /v1/doorstep/internal/admin/cancellation-rules",
	"PATCH /v1/doorstep/internal/admin/cancellation-rules/:id",
	"GET /v1/doorstep/internal/admin/commission-rules",
	"POST /v1/doorstep/internal/admin/commission-rules",
	"PATCH /v1/doorstep/internal/admin/commission-rules/:id",
	"GET /v1/doorstep/internal/admin/audit-logs",
	"GET /v1/doorstep/internal/admin/professionals",
	"GET /v1/doorstep/internal/admin/professionals/:id",
	"POST /v1/doorstep/internal/admin/professionals/:id/approve",
	"POST /v1/doorstep/internal/admin/professionals/:id/reject",
	"POST /v1/doorstep/internal/admin/professionals/:id/suspend",
	"POST /v1/doorstep/internal/admin/professionals/:id/reinstate",
	"POST /v1/doorstep/internal/admin/professionals/:id/block",
	"POST /v1/doorstep/internal/admin/professionals/:id/skills/:code/verify",
	"GET /v1/doorstep/internal/admin/documents",
	"POST /v1/doorstep/internal/admin/documents/:id/decide",
	"GET /v1/doorstep/internal/admin/documents/:id/view",
}

func TestRouteInventory(t *testing.T) {
	rg := newRig(t)
	var got []string
	for _, r := range rg.r.Routes() {
		got = append(got, r.Method+" "+r.Path)
	}
	want := append([]string(nil), wantRoutes...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("routes differ.\n got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Every admin route needs exactly the permission the contract names: reads
// catalogue.read; catalogue writes catalogue.write; city/zone/slot/
// cancellation/commission writes config.write; audit audit.read.
func TestAdminRoutePermissions(t *testing.T) {
	h := &Handler{}
	for _, rt := range h.adminRoutes() {
		want := PermCatalogueWrite
		switch {
		case rt.path == "/audit-logs":
			want = PermAuditRead
		case rt.method == http.MethodGet:
			want = PermCatalogueRead
		default:
			for _, p := range []string{"/cities", "/zones", "/slot-configs", "/cancellation-rules", "/commission-rules"} {
				if strings.HasPrefix(rt.path, p) {
					want = PermConfigWrite
				}
			}
		}
		if rt.perm != want {
			t.Errorf("%s %s needs %s, table says %s", rt.method, rt.path, want, rt.perm)
		}
	}
	for _, p := range AdminPermissions {
		if !strings.HasPrefix(p, "doorstep:") {
			t.Errorf("permission %q outside the doorstep: namespace", p)
		}
	}
	if len(AdminPermissions) != 18 {
		t.Fatalf("AdminPermissions has %d entries, the contract pins 18", len(AdminPermissions))
	}
}

// The A2 admin routes carry the contract's x-permission exactly.
func TestProAdminRoutePermissions(t *testing.T) {
	want := map[string]string{
		"GET /professionals":                          PermProsRead,
		"GET /professionals/:id":                      PermProsRead,
		"POST /professionals/:id/approve":             PermProsApprove,
		"POST /professionals/:id/reject":              PermProsApprove,
		"POST /professionals/:id/suspend":             PermProsSuspend,
		"POST /professionals/:id/reinstate":           PermProsSuspend,
		"POST /professionals/:id/block":               PermProsSuspend,
		"POST /professionals/:id/skills/:code/verify": PermProsApprove,
		"GET /documents":                              PermDocumentsReview,
		"POST /documents/:id/decide":                  PermDocumentsReview,
		"GET /documents/:id/view":                     PermDocumentsReview,
	}
	h := &Handler{}
	routes := h.proAdminRoutes()
	if len(routes) != len(want) {
		t.Fatalf("pro admin routes %d, want %d", len(routes), len(want))
	}
	for _, rt := range routes {
		if p, ok := want[rt.method+" "+rt.path]; !ok || p != rt.perm {
			t.Errorf("%s %s: permission %s, want %s", rt.method, rt.path, rt.perm, p)
		}
	}
}

func sample(path string) string {
	id := uuid.NewString()
	return strings.NewReplacer(":id", id, ":slug", "salon-women", ":code", "HYD").Replace(path)
}

// ---- internal key ----

func TestUserRoutesRequireInternalKey(t *testing.T) {
	rg := newRig(t)
	for _, rt := range wantRoutes {
		method, path, _ := strings.Cut(rt, " ")
		if !strings.HasPrefix(path, "/v1/doorstep/") || strings.HasPrefix(path, InternalAdminPrefix) {
			continue
		}
		for _, key := range []string{"", "not-the-key"} {
			hdr := rg.user()
			if key != "" {
				hdr["X-Internal-Service-Key"] = key
			}
			w := rg.do(req{method: method, path: sample(path) + "?city=HYD", headers: hdr, noKey: true, body: "{}"})
			if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), `"UNAUTHORIZED"`) {
				t.Errorf("%s %s key=%q: %d %s, want 401 UNAUTHORIZED", method, path, key, w.Code, w.Body.String())
			}
		}
	}
}

// With the key, an identity route without X-User-Id reaches the identity
// guard (AUTH_REQUIRED), proving the key middleware let it through.
func TestIdentityRoutesRequireGatewayIdentity(t *testing.T) {
	rg := newRig(t)
	for _, p := range []req{
		{method: "POST", path: "/v1/doorstep/quotes", body: "{}"},
		{method: "GET", path: "/v1/doorstep/quotes/" + uuid.NewString()},
	} {
		w := rg.do(p)
		if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "AUTH_REQUIRED") {
			t.Errorf("%s %s without identity: %d %s", p.method, p.path, w.Code, w.Body.String())
		}
	}
	w := rg.do(req{method: "GET", path: "/v1/doorstep/quotes/" + uuid.NewString(), headers: map[string]string{"X-User-Id": "not-a-uuid"}})
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "INVALID_USER_ID") {
		t.Fatalf("bad identity: %d %s", w.Code, w.Body.String())
	}
}

func TestProbesAreNotBehindInternalKey(t *testing.T) {
	rg := newRig(t)
	if w := rg.do(req{method: "GET", path: "/healthz", noKey: true}); w.Code != 200 {
		t.Fatalf("/healthz without key: %d", w.Code)
	}
}

func TestOpenWhenNoKeyConfiguredOutsideProduction(t *testing.T) {
	rg := newRig(t)
	rg.r = rg.router("", rg.v)
	if w := rg.do(req{method: "GET", path: "/v1/doorstep/catalogue?city=HYD", noKey: true}); w.Code != 200 {
		t.Fatalf("no key configured: %d %s", w.Code, w.Body.String())
	}
}

func TestCheckInternalKey(t *testing.T) {
	for _, tc := range []struct {
		production bool
		key        string
		wantErr    bool
	}{{true, "", true}, {true, "  ", true}, {true, "k", false}, {false, "", false}, {false, "k", false}} {
		if err := CheckInternalKey(tc.production, tc.key); (err != nil) != tc.wantErr {
			t.Errorf("CheckInternalKey(%v,%q) = %v", tc.production, tc.key, err)
		}
	}
}

// ---- admin token ----

// The admin family is token-only: the internal key and a forged admin
// identity are worth nothing there, on every route of the table.
func TestEveryAdminRouteRequiresAToken(t *testing.T) {
	rg := newRig(t)
	for _, rt := range rg.r.Routes() {
		if !strings.HasPrefix(rt.Path, InternalAdminPrefix) {
			continue
		}
		w := rg.do(req{method: rt.Method, path: sample(rt.Path), body: "{}",
			headers: map[string]string{"X-User-Id": uuid.NewString(), "X-Scopes": "admin superadmin"}})
		if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), CodeAdminTokenRequired) {
			t.Errorf("%s %s with key + forged admin identity: %d %s", rt.Method, rt.Path, w.Code, w.Body.String())
		}
	}
}

func TestAdminTokenRefusals(t *testing.T) {
	rg := newRig(t)
	path := InternalAdminPrefix + "/cities"
	actor := rg.actor.String()
	cases := []struct {
		name   string
		hdr    map[string]string
		status int
		code   string
	}{
		{"no token", nil, 401, CodeAdminTokenRequired},
		{"wrong audience (food)", rg.token(rg.admin, "food", PermCatalogueRead, actor), 403, CodeServiceTokenRejected},
		{"wrong audience (rider)", rg.token(rg.admin, "rider", PermCatalogueRead, actor), 403, CodeServiceTokenRejected},
		{"other registered caller", rg.token(rg.other, AudienceDoorstep, PermCatalogueRead, actor), 403, CodeServiceTokenRejected},
		{"rogue key claiming admin-service", rg.token(rg.rogue, AudienceDoorstep, PermCatalogueRead, actor), 403, CodeServiceTokenRejected},
		{"scope without the permission", rg.token(rg.admin, AudienceDoorstep, PermAuditRead, actor), 403, CodeAdminPermissionScope},
		{"no actor", rg.token(rg.admin, AudienceDoorstep, PermCatalogueRead, ""), 403, CodeAdminActorRequired},
		{"malformed actor", rg.token(rg.admin, AudienceDoorstep, PermCatalogueRead, "admin@example"), 403, CodeAdminActorRequired},
		{"garbage token", map[string]string{ServiceAuthHeader: "Bearer a.b.c"}, 403, CodeServiceTokenRejected},
		{"admitted", rg.token(rg.admin, AudienceDoorstep, PermCatalogueRead, actor), 200, ""},
	}
	for _, c := range cases {
		w := rg.do(req{method: "GET", path: path, headers: c.hdr, noKey: true})
		if w.Code != c.status || (c.code != "" && !strings.Contains(w.Body.String(), `"`+c.code+`"`)) {
			t.Errorf("%s: %d %s, want %d %s", c.name, w.Code, w.Body.String(), c.status, c.code)
		}
	}
}

func TestAdminTokenExpired(t *testing.T) {
	rg := newRig(t)
	hdr := rg.adminHeaders(PermCatalogueRead)
	rg.v.SetClock(func() time.Time { return time.Now().Add(10 * time.Minute) })
	w := rg.do(req{method: "GET", path: InternalAdminPrefix + "/cities", headers: hdr, noKey: true})
	if w.Code != 403 || !strings.Contains(w.Body.String(), CodeServiceTokenRejected) {
		t.Fatalf("expired token: %d %s", w.Code, w.Body.String())
	}
}

func TestAdminWithoutVerifier(t *testing.T) {
	rg := newRig(t)
	hdr := rg.adminHeaders(PermCatalogueRead)
	rg.r = rg.router(testInternalKey, nil)
	w := rg.do(req{method: "GET", path: InternalAdminPrefix + "/cities", headers: hdr, noKey: true})
	if w.Code != 401 || !strings.Contains(w.Body.String(), CodeServiceCredentialRequired) {
		t.Fatalf("no verifier: %d %s", w.Code, w.Body.String())
	}
}

// X-User-Id riding on an admitted request never becomes the actor.
func TestAdminActorIsTheSignedClaim(t *testing.T) {
	rg := newRig(t)
	hdr := rg.adminHeaders(PermCatalogueWrite)
	hdr["X-User-Id"] = uuid.NewString()
	w := rg.do(req{method: "POST", path: InternalAdminPrefix + "/categories", headers: hdr, noKey: true,
		body: `{"slug":"laundry","name":"Laundry","family":"HOME_CLEANING"}`})
	if w.Code != 201 || len(rg.store.actors) != 1 || rg.store.actors[0].UserID != rg.actor {
		t.Fatalf("actor: %d %+v", w.Code, rg.store.actors)
	}
}

func TestServiceCallersFromEnv(t *testing.T) {
	pub, _, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if v, err := ServiceCallersFromEnv(get(nil)); v != nil || err != nil {
		t.Fatalf("blank: %v %v", v, err)
	}
	for name, env := range map[string]map[string]string{
		"missing pubkey": {"SERVICE_CALLERS": "admin-service", "SERVICE_CALLER_ADMIN_SERVICE_KID": "a1", "SERVICE_CALLER_ADMIN_SERVICE_OPS": PermCatalogueRead},
		"missing ops":    {"SERVICE_CALLERS": "admin-service", "SERVICE_CALLER_ADMIN_SERVICE_KID": "a1", "SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": pub},
		"bad pubkey":     {"SERVICE_CALLERS": "admin-service", "SERVICE_CALLER_ADMIN_SERVICE_KID": "a1", "SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": "x", "SERVICE_CALLER_ADMIN_SERVICE_OPS": PermCatalogueRead},
		"only commas":    {"SERVICE_CALLERS": " , "},
	} {
		if v, err := ServiceCallersFromEnv(get(env)); err == nil || v != nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
