// Admin-service token tests (admin console Wave 2 — Money). No database:
// admission is proven either by a probe mounted on the exact token chain
// (which reports the actor getAdminID hands to the service) or by a real
// route answering its own pre-service validation error; refusals are proven
// by the auth layer's or the boundary's own status and code. Audit rows and
// stats are in admin_token_integration_test.go.
package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/runmode"
	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const adminTestInternalKey = "monetization-admin-token-test-key"

type adminTokenRig struct {
	v        *servicetoken.Verifier
	admin    *servicetoken.Signer // admin-service, registered for AdminPermissions
	notifier *servicetoken.Signer // a registered caller that is not admin-service
	rogue    *servicetoken.Signer // claims to be admin-service, unregistered key
	actor    uuid.UUID
}

func newAdminTokenRig(t *testing.T) *adminTokenRig {
	t.Helper()
	aPub, aPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	nPub, nPriv, err := servicetoken.GenerateKeypair()
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
		"SERVICE_CALLER_NOTIFICATION_SERVICE_PUBKEY": nPub,
		// Deliberately over-granted: even with the op registered, a caller
		// other than admin-service must not reach an admin route.
		"SERVICE_CALLER_NOTIFICATION_SERVICE_OPS": strings.Join(AdminPermissions, ","),
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
	return &adminTokenRig{
		v:        v,
		admin:    mk(IssuerAdminService, "a1", aPriv),
		notifier: mk("notification-service", "n1", nPriv),
		rogue:    mk(IssuerAdminService, "a1", rPriv),
		actor:    uuid.New(),
	}
}

// handler is a monetization handler with no service behind it: anything that
// reaches the service panics and gin.Recovery answers 500, so every assertion
// below is on a status the auth layer, the boundary or pre-service validation
// produced.
func (rg *adminTokenRig) handler() *Handler {
	return New(nil).WithInternalKey(adminTestInternalKey).WithServiceAuth(rg.v).WithWritesEnabled(true)
}

func routerFor(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	h.RegisterRoutes(r)
	return r
}

// probePath is a test-only route mounted on the exact token chain; it
// answers with the actor adminActor resolved.
const probePath = InternalAdminPrefix + "/probe"

func probeRouter(h *Handler, perm string, maintenanceOpen bool) *gin.Engine {
	r := routerFor(h)
	r.Handle(http.MethodPost, probePath, h.tokenChain(adminRoute{
		method: http.MethodPost, path: "/probe", perm: perm, maintenanceOpen: maintenanceOpen,
		handler: func(c *gin.Context) {
			a, ok := adminActor(c)
			if !ok {
				return
			}
			c.JSON(http.StatusOK, gin.H{"actor": a.ID.String(), "via": a.Via})
		},
	})...)
	return r
}

func (rg *adminTokenRig) mint(t *testing.T, s *servicetoken.Signer, aud string, scope []string, actor string) string {
	t.Helper()
	var opts []servicetoken.MintOption
	if actor != "" {
		opts = append(opts, servicetoken.WithActor(actor))
	}
	tok, err := s.Mint(aud, "admin-console", scope, nil, time.Minute, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func adminServe(r *gin.Engine, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func bearer(tok string) map[string]string {
	return map[string]string{ServiceAuthHeader: "Bearer " + tok}
}

// legacyAdmin is what the gateway would send for an admin today, key included.
func legacyAdmin(user uuid.UUID, scopes string) map[string]string {
	return map[string]string{
		"X-Internal-Service-Key": adminTestInternalKey,
		"X-User-Id":              user.String(),
		"X-Scopes":               scopes,
	}
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Error == nil {
		return ""
	}
	return body.Error.Code
}

func uuidParams(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if strings.HasPrefix(p, ":") {
			parts[i] = uuid.NewString()
		}
	}
	return strings.Join(parts, "/")
}

// reversePath is a real admin write whose handler refuses an empty reason
// (400 REASON_REQUIRED) after resolving the actor and before any service
// call: a 400 there means the request was admitted.
func reversePath(prefix string) string {
	return prefix + "/creator-fund/earnings/" + uuid.NewString() + "/reverse"
}

func TestAdminToken_RightScopeAdmittedActorIsAct(t *testing.T) {
	rg := newAdminTokenRig(t)
	h := rg.handler()
	r := probeRouter(h, PermFundReverse, true)
	tok := rg.mint(t, rg.admin, AudienceMonetization, []string{PermFundReverse}, rg.actor.String())

	// A forged gateway identity, the key and extra actor headers ride along;
	// the actor is act.
	forged := uuid.New()
	hdr := legacyAdmin(forged, "superadmin admin")
	hdr["X-Admin-Id"] = forged.String()
	hdr["X-Actor-Id"] = forged.String()
	hdr[ServiceAuthHeader] = "Bearer " + tok
	w := adminServe(r, http.MethodPost, probePath, `{}`, hdr)
	if w.Code != http.StatusOK {
		t.Fatalf("probe: status=%d body=%s, want 200", w.Code, w.Body.String())
	}
	var got struct{ Actor, Via string }
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Actor != rg.actor.String() || got.Via != service.ViaAdminService {
		t.Fatalf("actor=%s via=%s, want act %s via %s (forged header was %s)", got.Actor, got.Via, rg.actor, service.ViaAdminService, forged)
	}

	// A real route admits the same token: it reaches its own validation.
	w = adminServe(routerFor(h), http.MethodPost, reversePath(InternalAdminPrefix), `{}`, bearer(tok))
	if w.Code != http.StatusBadRequest || errorCode(t, w) != "REASON_REQUIRED" {
		t.Fatalf("reverse with token: status=%d body=%s, want 400 REASON_REQUIRED", w.Code, w.Body.String())
	}
}

// On the token path the actor is never taken from headers: a token path with
// no admitted actor is refused even when X-User-Id and the admin scope are set.
func TestAdminToken_TokenPathNeverReadsActorHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, probePath, nil)
	c.Request.Header.Set("X-User-Id", uuid.NewString())
	c.Request.Header.Set("X-Scopes", "superadmin")
	c.Set(ctxTokenPath, true)
	if id, ok := getAdminID(c); ok || id != uuid.Nil {
		t.Fatalf("getAdminID on the token path without act = %s, %t; want refused", id, ok)
	}
	if w.Code != http.StatusForbidden || errorCode(t, w) != CodeAdminActorRequired {
		t.Fatalf("status=%d code=%q, want 403 %s", w.Code, errorCode(t, w), CodeAdminActorRequired)
	}
}

func TestAdminToken_Refusals(t *testing.T) {
	rg := newAdminTokenRig(t)
	actor := rg.actor.String()
	cases := []struct {
		name     string
		tok      string
		clock    time.Duration
		wantCode string
	}{
		{"wrong audience", rg.mint(t, rg.admin, "dating", []string{PermFundReverse}, actor), 0, CodeServiceTokenRejected},
		{"payments audience", rg.mint(t, rg.admin, servicetoken.AudiencePayments, []string{PermFundReverse}, actor), 0, CodeServiceTokenRejected},
		{"missing act", rg.mint(t, rg.admin, AudienceMonetization, []string{PermFundReverse}, ""), 0, CodeAdminActorRequired},
		{"malformed act", rg.mint(t, rg.admin, AudienceMonetization, []string{PermFundReverse}, "not-a-uuid"), 0, CodeAdminActorRequired},
		{"nil act", rg.mint(t, rg.admin, AudienceMonetization, []string{PermFundReverse}, uuid.Nil.String()), 0, CodeAdminActorRequired},
		{"wrong scope", rg.mint(t, rg.admin, AudienceMonetization, []string{PermFundRead}, actor), 0, CodeAdminPermissionScope},
		{"expired", rg.mint(t, rg.admin, AudienceMonetization, []string{PermFundReverse}, actor), 2 * time.Minute, CodeServiceTokenRejected},
		{"unknown caller key", rg.mint(t, rg.rogue, AudienceMonetization, []string{PermFundReverse}, actor), 0, CodeServiceTokenRejected},
		{"registered non-admin caller", rg.mint(t, rg.notifier, AudienceMonetization, []string{PermFundReverse}, actor), 0, CodeServiceTokenRejected},
		{"garbage", "a.b.c", 0, CodeServiceTokenRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.clock != 0 {
				rg.v.SetClock(func() time.Time { return time.Now().Add(tc.clock) })
				defer rg.v.SetClock(time.Now)
			}
			r := probeRouter(rg.handler(), PermFundReverse, true)
			for _, path := range []string{probePath, reversePath(InternalAdminPrefix)} {
				hdr := legacyAdmin(uuid.New(), "superadmin")
				hdr[ServiceAuthHeader] = "Bearer " + tc.tok
				w := adminServe(r, http.MethodPost, path, `{"reason":"x"}`, hdr)
				if w.Code != http.StatusForbidden || errorCode(t, w) != tc.wantCode {
					t.Fatalf("%s: status=%d code=%q body=%s, want 403 %s", path, w.Code, errorCode(t, w), w.Body.String(), tc.wantCode)
				}
			}
		})
	}
}

// The edge cannot reach the token-only family with the key the gateway stamps
// plus a forged actor: no token, no entry, whatever the headers say.
func TestAdminToken_InternalFamilyRefusesKeyAndForgedActor(t *testing.T) {
	rg := newAdminTokenRig(t)
	r := routerFor(rg.handler())
	forged := uuid.New()
	var checked int
	for _, ri := range r.Routes() {
		if !strings.HasPrefix(ri.Path, InternalAdminPrefix+"/") {
			continue
		}
		checked++
		hdr := legacyAdmin(forged, "superadmin admin moderator")
		hdr["X-Admin-Id"] = forged.String()
		hdr["X-Actor-Id"] = forged.String()
		w := adminServe(r, ri.Method, uuidParams(ri.Path), `{"reason":"x"}`, hdr)
		if w.Code != http.StatusUnauthorized || errorCode(t, w) != CodeAdminTokenRequired {
			t.Fatalf("%s %s: status=%d code=%q, want 401 %s", ri.Method, ri.Path, w.Code, errorCode(t, w), CodeAdminTokenRequired)
		}
	}
	if checked != len(adminRoutePermissions()) {
		t.Fatalf("checked %d internal admin routes, want %d", checked, len(adminRoutePermissions()))
	}
}

// adminRoutePermissions is the route table under test: method + suffix →
// the permission the route requires.
func adminRoutePermissions() map[string]string {
	return map[string]string{
		"GET /stats":                               PermStatsRead,
		"GET /fraud-reviews":                       PermFraudReview,
		"PATCH /fraud-reviews/:id":                 PermFraudReview,
		"POST /wallet/:userId/freeze":              PermWalletFreeze,
		"POST /wallet/:userId/unfreeze":            PermWalletUnfreeze,
		"POST /wallet/:userId/rebuild":             PermWalletRebuild,
		"GET /creator-fund/rates":                  PermFundRead,
		"PUT /creator-fund/rates":                  PermFundRates,
		"PUT /creator-fund/quality-bands":          PermFundRates,
		"POST /creator-fund/:userId/suspend":       PermCreatorsSuspend,
		"POST /creator-fund/:userId/unsuspend":     PermCreatorsSuspend,
		"POST /creator-fund/settle":                PermFundSettle,
		"POST /creator-fund/settle-period":         PermFundSettle,
		"POST /creator-fund/:userId/settle-period": PermFundSettle,
		"POST /creator-fund/earnings/:id/reverse":  PermFundReverse,
		"GET /creator-fund/earnings/:id":           PermFundRead,
		"GET /creator-fund/budgets":                PermFundRead,
		"PUT /creator-fund/budgets":                PermFundBudget,
		"GET /disputes":                            PermDisputesRead,
		"PATCH /disputes/:id":                      PermDisputesAct,
		"POST /refunds":                            PermRefundIssue,
		"GET /payout-requests":                     PermPayoutsRead,
		"GET /audit-logs":                          PermAuditRead,
	}
}

// Every route refuses a token carrying every OTHER admin permission.
func TestAdminToken_EveryRouteNeedsItsPermission(t *testing.T) {
	rg := newAdminTokenRig(t)
	r := routerFor(rg.handler())
	want := adminRoutePermissions()
	seen := 0
	for _, ri := range r.Routes() {
		if !strings.HasPrefix(ri.Path, InternalAdminPrefix+"/") {
			continue
		}
		key := ri.Method + " " + strings.TrimPrefix(ri.Path, InternalAdminPrefix)
		perm, ok := want[key]
		if !ok {
			t.Fatalf("undeclared token admin route %s", key)
		}
		seen++
		var others []string
		for _, p := range AdminPermissions {
			if p != perm {
				others = append(others, p)
			}
		}
		tok := rg.mint(t, rg.admin, AudienceMonetization, others, rg.actor.String())
		hdr := bearer(tok)
		hdr["X-Internal-Service-Key"] = adminTestInternalKey
		hdr["X-Scopes"] = "superadmin"
		w := adminServe(r, ri.Method, uuidParams(ri.Path), `{"reason":"x"}`, hdr)
		if w.Code != http.StatusForbidden || errorCode(t, w) != CodeAdminPermissionScope {
			t.Fatalf("%s without %s: status=%d code=%q, want 403 %s", key, perm, w.Code, errorCode(t, w), CodeAdminPermissionScope)
		}
	}
	if seen != len(want) {
		t.Fatalf("saw %d token admin routes, want %d", seen, len(want))
	}
	// Every permission the family uses is registered for admin-service, and
	// no registered permission is unused.
	used := map[string]bool{}
	for _, p := range want {
		used[p] = true
	}
	if len(used) != len(AdminPermissions) {
		t.Fatalf("routes use %d permissions, AdminPermissions lists %d", len(used), len(AdminPermissions))
	}
}

// The LEGACY path is unchanged: X-Scopes admin plus the key, the token ignored.
func TestAdminToken_LegacyScopesStillWork(t *testing.T) {
	rg := newAdminTokenRig(t)
	r := routerFor(rg.handler())
	legacy := "/v1/monetization/admin"
	user := uuid.New()

	if w := adminServe(r, http.MethodPost, reversePath(legacy), `{}`, legacyAdmin(user, "admin")); w.Code != http.StatusBadRequest || errorCode(t, w) != "REASON_REQUIRED" {
		t.Fatalf("legacy admin: status=%d body=%s, want 400 REASON_REQUIRED (admitted)", w.Code, w.Body.String())
	}
	if w := adminServe(r, http.MethodPost, reversePath(legacy), `{}`, legacyAdmin(user, "moderator")); w.Code != http.StatusForbidden {
		t.Fatalf("legacy moderator: status=%d, want 403", w.Code)
	}
	noKey := legacyAdmin(user, "admin")
	delete(noKey, "X-Internal-Service-Key")
	if w := adminServe(r, http.MethodPost, reversePath(legacy), `{}`, noKey); w.Code != http.StatusUnauthorized {
		t.Fatalf("legacy without the internal key: status=%d, want 401", w.Code)
	}
	// A valid token does not stand in for the scope on the legacy family.
	tok := rg.mint(t, rg.admin, AudienceMonetization, []string{PermFundReverse}, rg.actor.String())
	hdr := bearer(tok)
	hdr["X-Internal-Service-Key"] = adminTestInternalKey
	if w := adminServe(r, http.MethodPost, reversePath(legacy), `{}`, hdr); w.Code != http.StatusForbidden {
		t.Fatalf("legacy with token but no scope: status=%d, want 403", w.Code)
	}
	// The legacy actor is X-User-Id, recorded as via gateway.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, reversePath(legacy), nil)
	c.Request.Header.Set("X-User-Id", user.String())
	c.Request.Header.Set("X-Scopes", "admin")
	a, ok := adminActor(c)
	if !ok || a.ID != user || a.Via != service.ViaGateway {
		t.Fatalf("legacy actor = %+v, %t; want %s via gateway", a, ok, user)
	}
}

// A deployment without admin-service registered accepts no admin token.
func TestAdminToken_NoVerifierRefuses(t *testing.T) {
	rg := newAdminTokenRig(t)
	tok := rg.mint(t, rg.admin, AudienceMonetization, []string{PermFundReverse}, rg.actor.String())
	r := routerFor(New(nil).WithWritesEnabled(true))
	if w := adminServe(r, http.MethodPost, reversePath(InternalAdminPrefix), `{}`, bearer(tok)); w.Code != http.StatusUnauthorized || errorCode(t, w) != CodeServiceCredentialRequired {
		t.Fatalf("no verifier: status=%d code=%q, want 401 %s", w.Code, errorCode(t, w), CodeServiceCredentialRequired)
	}
}

func TestServiceCallersFromEnv(t *testing.T) {
	if v, err := ServiceCallersFromEnv(func(string) string { return "" }); v != nil || err != nil {
		t.Fatalf("blank SERVICE_CALLERS: v=%v err=%v, want nil, nil", v, err)
	}
	pub, _, _ := servicetoken.GenerateKeypair()
	for name, env := range map[string]map[string]string{
		"missing key": {"SERVICE_CALLERS": "admin-service", "SERVICE_CALLER_ADMIN_SERVICE_OPS": PermStatsRead},
		"missing ops": {"SERVICE_CALLERS": "admin-service", "SERVICE_CALLER_ADMIN_SERVICE_KID": "a1", "SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": pub},
	} {
		if _, err := ServiceCallersFromEnv(func(k string) string { return env[k] }); err == nil {
			t.Fatalf("%s: want a configuration error", name)
		}
	}
}

// ---------------------------------------------------------------------------
// The flags hold on the token path
// ---------------------------------------------------------------------------

// allPermsToken carries every admin permission, so only the flags can refuse.
func (rg *adminTokenRig) allPermsToken(t *testing.T) string {
	return rg.mint(t, rg.admin, AudienceMonetization, AdminPermissions, rg.actor.String())
}

// The table declares what is a read, and only a GET may be one: a write
// marked read would be served in the beta, and a GET left unmarked would
// blank a dashboard. Both directions are pinned here, and the registrar
// refuses to boot a non-GET read.
func TestAdminToken_ReadsAreExactlyTheGets(t *testing.T) {
	rg := newAdminTokenRig(t)
	h := rg.handler()
	reads, writes := 0, 0
	for _, rt := range h.adminRoutes() {
		if rt.read != (rt.method == http.MethodGet) {
			t.Fatalf("%s %s: read=%t, want %t", rt.method, rt.path, rt.read, rt.method == http.MethodGet)
		}
		if rt.read {
			reads++
		} else {
			writes++
		}
	}
	if reads != 8 || writes != 15 {
		t.Fatalf("table has %d reads and %d writes, want 8 and 15 — update this test with the route table", reads, writes)
	}
	// The registrar's guard: a GET read and a POST write pass, a POST marked
	// read panics.
	(adminRoute{method: http.MethodGet, path: "/ok", read: true}).validate()
	(adminRoute{method: http.MethodPost, path: "/ok", read: false}).validate()
	defer func() {
		if recover() == nil {
			t.Fatal("a POST marked read passed validate")
		}
	}()
	(adminRoute{method: http.MethodPost, path: "/bad", read: true}).validate()
}

// MONETIZATION_WRITES_ENABLED=false (founder decision 2026-09-17): every
// read of the family is served to a permitted token, every write answers
// 503 MONETIZATION_NOT_LAUNCHED before its handler, and the legacy admin
// family keeps refusing its reads exactly as before.
func TestAdminToken_WritesDisabledServesReadsRefusesWrites(t *testing.T) {
	rg := newAdminTokenRig(t)
	h := New(nil).WithInternalKey(adminTestInternalKey).WithServiceAuth(rg.v).WithWritesEnabled(false)
	r := probeRouter(h, PermFundReverse, true)
	// A read probe on the exact token chain: admitted, and it names the actor.
	r.Handle(http.MethodGet, probePath, h.tokenChain(adminRoute{
		method: http.MethodGet, path: "/probe", perm: PermFundRead, maintenanceOpen: true, read: true,
		handler: func(c *gin.Context) {
			a, ok := adminActor(c)
			if !ok {
				return
			}
			c.JSON(http.StatusOK, gin.H{"actor": a.ID.String(), "via": a.Via})
		},
	})...)
	tok := rg.allPermsToken(t)

	readByKey := map[string]bool{}
	for _, rt := range h.adminRoutes() {
		readByKey[rt.method+" "+InternalAdminPrefix+rt.path] = rt.read
	}
	reads, writes := 0, 0
	for _, ri := range r.Routes() {
		if !strings.HasPrefix(ri.Path, InternalAdminPrefix+"/") || ri.Path == probePath {
			continue
		}
		w := adminServe(r, ri.Method, uuidParams(ri.Path), `{"reason":"x"}`, bearer(tok))
		if readByKey[ri.Method+" "+ri.Path] {
			reads++
			// No service behind the handler: reaching it panics and
			// gin.Recovery answers 500. Any 503 means the boundary refused.
			if w.Code == http.StatusServiceUnavailable || errorCode(t, w) == "MONETIZATION_NOT_LAUNCHED" {
				t.Fatalf("read %s %s with writes disabled: status=%d body=%s, want served", ri.Method, ri.Path, w.Code, w.Body.String())
			}
			continue
		}
		writes++
		if w.Code != http.StatusServiceUnavailable || errorCode(t, w) != "MONETIZATION_NOT_LAUNCHED" {
			t.Fatalf("write %s %s with writes disabled: status=%d body=%s, want 503 MONETIZATION_NOT_LAUNCHED", ri.Method, ri.Path, w.Code, w.Body.String())
		}
	}
	if reads != 8 || writes != 15 {
		t.Fatalf("checked %d reads and %d writes, want 8 and 15", reads, writes)
	}
	w := adminServe(r, http.MethodGet, probePath, ``, bearer(tok))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), rg.actor.String()) {
		t.Fatalf("read probe with writes disabled: status=%d body=%s, want 200 naming the actor", w.Code, w.Body.String())
	}
	// The write probe on the same chain is still refused.
	if w := adminServe(r, http.MethodPost, probePath, `{}`, bearer(tok)); w.Code != http.StatusServiceUnavailable || errorCode(t, w) != "MONETIZATION_NOT_LAUNCHED" {
		t.Fatalf("write probe with writes disabled: status=%d body=%s, want 503 MONETIZATION_NOT_LAUNCHED", w.Code, w.Body.String())
	}
	// Authentication still comes first: no token is 401 on a read too, not a
	// hint about the run mode and not the data.
	if w := adminServe(r, http.MethodGet, probePath, ``, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token on a read with writes disabled: status=%d, want 401", w.Code)
	}
	// The legacy admin family is unchanged: its reads stay closed in the beta.
	for _, p := range []string{"/v1/monetization/admin/fraud-reviews", "/v1/monetization/admin/creator-fund/budgets", "/v1/monetization/admin/creator-fund/rates"} {
		if w := adminServe(r, http.MethodGet, p, ``, legacyAdmin(rg.actor, "admin")); w.Code != http.StatusServiceUnavailable || errorCode(t, w) != "MONETIZATION_NOT_LAUNCHED" {
			t.Fatalf("legacy GET %s with writes disabled: status=%d body=%s, want 503 MONETIZATION_NOT_LAUNCHED", p, w.Code, w.Body.String())
		}
	}
}

// MONETIZATION_MAINTENANCE=true (writes on too — maintenance wins): refunds
// and dispute updates answer 503 MAINTENANCE on the token path exactly as on
// their legacy routes; the admin corrections stay open to the token.
func TestAdminToken_MaintenanceClosesNonCorrections(t *testing.T) {
	rg := newAdminTokenRig(t)
	h := New(nil).WithInternalKey(adminTestInternalKey).WithServiceAuth(rg.v).WithWritesEnabled(true).WithMaintenance(true)
	r := routerFor(h)
	tok := rg.allPermsToken(t)

	closed := map[string]bool{"PATCH /disputes/:id": true, "POST /refunds": true}
	for _, rt := range h.adminRoutes() {
		key := rt.method + " " + rt.path
		if rt.maintenanceOpen == closed[key] {
			t.Fatalf("%s: maintenanceOpen=%t, want %t", key, rt.maintenanceOpen, !closed[key])
		}
	}
	for key := range closed {
		method, path, _ := strings.Cut(key, " ")
		w := adminServe(r, method, uuidParams(InternalAdminPrefix+path), `{"status":"resolved_denied","transaction_id":"`+uuid.NewString()+`","amount_paise":1,"reason":"x"}`, bearer(tok))
		if w.Code != http.StatusServiceUnavailable || errorCode(t, w) != "MAINTENANCE" {
			t.Fatalf("%s in maintenance: status=%d body=%s, want 503 MAINTENANCE", key, w.Code, w.Body.String())
		}
	}
	// A correction is admitted (it reaches its own validation).
	if w := adminServe(r, http.MethodPost, reversePath(InternalAdminPrefix), `{}`, bearer(tok)); w.Code != http.StatusBadRequest || errorCode(t, w) != "REASON_REQUIRED" {
		t.Fatalf("correction in maintenance: status=%d body=%s, want 400 REASON_REQUIRED", w.Code, w.Body.String())
	}
	// Without a token nothing is open, maintenance or not.
	if w := adminServe(r, http.MethodPost, reversePath(InternalAdminPrefix), `{}`, map[string]string{"X-Internal-Service-Key": adminTestInternalKey, "X-Scopes": "admin"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("correction in maintenance without token: status=%d, want 401", w.Code)
	}
}

// MONETIZATION_PAYOUTS_ENABLED stays the only way money leaves: the family
// has no payout write at all, only the read-only request list, and the run
// modes that could combine maintenance with payouts still refuse to boot.
func TestAdminToken_NoPayoutWriteOnTokenPath(t *testing.T) {
	rg := newAdminTokenRig(t)
	h := rg.handler()
	for _, rt := range h.adminRoutes() {
		if strings.Contains(rt.path, "payout") && rt.method != http.MethodGet {
			t.Fatalf("token family carries a payout write: %s %s", rt.method, rt.path)
		}
	}
	r := routerFor(h)
	tok := rg.allPermsToken(t)
	for _, p := range []string{"/payouts", "/payout-requests", "/payout-requests/" + uuid.NewString() + "/approve", "/payouts/" + uuid.NewString() + "/release"} {
		for _, m := range []string{http.MethodPost, http.MethodPatch, http.MethodPut} {
			if w := adminServe(r, m, InternalAdminPrefix+p, `{}`, bearer(tok)); w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s: status=%d, want no such route", m, p, w.Code)
			}
		}
	}
	if _, err := runmode.Resolve(runmode.Config{Maintenance: true, WritesEnabled: true, PayoutsEnabled: true, InternalKey: "k"}); err == nil {
		t.Fatal("maintenance with payouts booted")
	}
	if _, err := runmode.Resolve(runmode.Config{PayoutsEnabled: true}); err == nil {
		t.Fatal("payouts without writes booted")
	}
}
