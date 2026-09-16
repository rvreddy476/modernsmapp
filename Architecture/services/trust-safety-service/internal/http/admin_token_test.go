// Admin-service token tests (admin console Wave 1 — B4 trust & safety).
// Copied from dating-service's admin_token_test.go. No database: admission is
// proven by reaching a handler that then refuses a malformed path or query
// (400), refusal by the auth layer's own status and code. The audit-actor,
// list and stats checks are in admin_token_integration_test.go.
package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const tokenTestInternalKey = "test-internal-key"

// newTokenTestRouter builds the engine in the same order as cmd/server/main.go:
// the token-only family first, then the internal key, then every other route.
func newTokenTestRouter(t *testing.T, h *Handler) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	h.RegisterAdminTokenRoutes(r)
	r.Use(middleware.RequireInternalKey(tokenTestInternalKey))
	h.RegisterRoutes(r)
	return r
}

func serveAdmin(r *gin.Engine, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func adminErrorCode(w *httptest.ResponseRecorder) string {
	var env struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || env.Error == nil {
		return ""
	}
	return env.Error.Code
}

// gatewayAdmin is what the api-gateway forwards for a logged-in admin: the
// injected internal key plus the identity and scopes it derived.
func gatewayAdmin(userID uuid.UUID, scopes string) map[string]string {
	return map[string]string{
		"X-Internal-Service-Key": tokenTestInternalKey,
		"X-User-Id":              userID.String(),
		"X-Verified-User-Id":     userID.String(),
		"X-Scopes":               scopes,
	}
}

type adminTokenRig struct {
	r        *gin.Engine
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
		"SERVICE_CALLERS":                      "admin-service,dating-service",
		"SERVICE_CALLER_ADMIN_SERVICE_KID":     "a1",
		"SERVICE_CALLER_ADMIN_SERVICE_PUBKEY":  aPub,
		"SERVICE_CALLER_ADMIN_SERVICE_OPS":     strings.Join(AdminPermissions, ","),
		"SERVICE_CALLER_DATING_SERVICE_KID":    "d1",
		"SERVICE_CALLER_DATING_SERVICE_PUBKEY": nPub,
		// Deliberately over-granted: even with the ops registered, a caller
		// other than admin-service must not reach an admin route.
		"SERVICE_CALLER_DATING_SERVICE_OPS": strings.Join(AdminPermissions, ","),
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
		r:        newTokenTestRouter(t, New(nil).WithServiceAuth(v)),
		v:        v,
		admin:    mk(IssuerAdminService, "a1", aPriv),
		notifier: mk("dating-service", "d1", nPriv),
		rogue:    mk(IssuerAdminService, "a1", rPriv),
		actor:    uuid.New(),
	}
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

func bearer(tok string) map[string]string {
	return map[string]string{ServiceAuthHeader: "Bearer " + tok}
}

// Report detail with a malformed id: admitted → the handler's 400.
const reportDetailBad = InternalAdminPrefix + "/reports/not-a-uuid"

func TestAdminToken_RightScopeAdmittedWithoutInternalKey(t *testing.T) {
	rg := newAdminTokenRig(t)
	tok := rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermReportsRead}, rg.actor.String())
	// No internal key: the family is outside the key middleware.
	if w := serveAdmin(rg.r, http.MethodGet, reportDetailBad, "", bearer(tok)); w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400 from the handler (admitted)", w.Code, w.Body.String())
	}
	// PATCH needs an actor; the token's act is it, with no identity header.
	actTok := rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermReportsAct}, rg.actor.String())
	w := serveAdmin(rg.r, http.MethodPatch, reportDetailBad, `{"status":"reviewing"}`, bearer(actTok))
	if w.Code != http.StatusBadRequest || adminErrorCode(w) != "BAD_REQUEST" {
		t.Fatalf("patch status=%d body=%s, want 400 BAD_REQUEST (admitted, actor from token)", w.Code, w.Body.String())
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
		{"wrong audience", rg.mint(t, rg.admin, "dating", []string{PermReportsRead}, actor), 0, CodeServiceTokenRejected},
		{"missing act", rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermReportsRead}, ""), 0, CodeAdminActorRequired},
		{"malformed act", rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermReportsRead}, "not-a-uuid"), 0, CodeAdminActorRequired},
		{"nil act", rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermReportsRead}, uuid.Nil.String()), 0, CodeAdminActorRequired},
		{"wrong scope", rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermReportsAct}, actor), 0, CodeAdminPermissionScope},
		{"expired", rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermReportsRead}, actor), 2 * time.Minute, CodeServiceTokenRejected},
		{"unknown caller key", rg.mint(t, rg.rogue, AudienceTrustSafety, []string{PermReportsRead}, actor), 0, CodeServiceTokenRejected},
		{"registered non-admin caller", rg.mint(t, rg.notifier, AudienceTrustSafety, []string{PermReportsRead}, actor), 0, CodeServiceTokenRejected},
		{"garbage", "a.b.c", 0, CodeServiceTokenRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.clock != 0 {
				rg.v.SetClock(func() time.Time { return time.Now().Add(tc.clock) })
				defer rg.v.SetClock(time.Now)
			}
			// The key and a full admin gateway identity ride along; a request
			// with a token is judged by the token alone.
			hdr := gatewayAdmin(uuid.New(), "superadmin admin")
			hdr[ServiceAuthHeader] = "Bearer " + tc.tok
			w := serveAdmin(rg.r, http.MethodGet, reportDetailBad, "", hdr)
			if w.Code != http.StatusForbidden || adminErrorCode(w) != tc.wantCode {
				t.Fatalf("status=%d code=%q body=%s, want 403 %s", w.Code, adminErrorCode(w), w.Body.String(), tc.wantCode)
			}
		})
	}
}

// The edge cannot reach the token-only family with the key the gateway
// stamps plus a forged actor: no token, no entry, whatever the headers say.
func TestAdminToken_InternalFamilyRefusesKeyAndForgedActor(t *testing.T) {
	rg := newAdminTokenRig(t)
	forged := uuid.New()
	var checked int
	for _, ri := range rg.r.Routes() {
		if !strings.HasPrefix(ri.Path, InternalAdminPrefix+"/") {
			continue
		}
		checked++
		path := strings.NewReplacer(":id", uuid.NewString(), ":userId", uuid.NewString(), ":mediaId", uuid.NewString()).Replace(ri.Path)
		hdr := gatewayAdmin(forged, "superadmin admin moderator")
		hdr["X-Admin-Id"] = forged.String()
		hdr["X-Actor-Id"] = forged.String()
		w := serveAdmin(rg.r, ri.Method, path, `{"status":"reviewing"}`, hdr)
		if w.Code != http.StatusUnauthorized || adminErrorCode(w) != CodeAdminTokenRequired {
			t.Fatalf("%s %s: status=%d code=%q, want 401 %s", ri.Method, path, w.Code, adminErrorCode(w), CodeAdminTokenRequired)
		}
	}
	if checked != len(adminRouteTable) {
		t.Fatalf("checked %d internal admin routes, want %d", checked, len(adminRouteTable))
	}
}

// adminRouteTable is every token route and the permissions that admit it.
var adminRouteTable = map[string][]string{
	"GET " + InternalAdminPrefix + "/stats":                  {PermStatsRead},
	"GET " + InternalAdminPrefix + "/reports":                {PermReportsRead},
	"GET " + InternalAdminPrefix + "/reports/:id":            {PermReportsRead},
	"PATCH " + InternalAdminPrefix + "/reports/:id":          {PermReportsAct},
	"GET " + InternalAdminPrefix + "/appeals":                {PermAppealsRead, PermAppealsAct},
	"PATCH " + InternalAdminPrefix + "/appeals/:id":          {PermAppealsAct},
	"GET " + InternalAdminPrefix + "/grievances":             {PermGrievancesRead, PermGrievancesAct},
	"GET " + InternalAdminPrefix + "/grievances/:id":         {PermGrievancesRead, PermGrievancesAct},
	"GET " + InternalAdminPrefix + "/grievances/:id/history": {PermGrievancesRead, PermGrievancesAct, PermAuditRead},
	"PATCH " + InternalAdminPrefix + "/grievances/:id":       {PermGrievancesAct},
	"GET " + InternalAdminPrefix + "/strikes/:userId":        {PermStrikesRead, PermStrikesManage},
	"GET " + InternalAdminPrefix + "/verification-requests":  {PermVerificationReview},
	"GET " + InternalAdminPrefix + "/media-labels/:mediaId":  {PermMediaLabelsRead},
	"GET " + InternalAdminPrefix + "/keyword-filters":        {PermKeywordFiltersRead},
}

func TestAdminToken_EveryInternalRouteNeedsItsPermission(t *testing.T) {
	rg := newAdminTokenRig(t)
	seen := 0
	for _, ri := range rg.r.Routes() {
		if !strings.HasPrefix(ri.Path, InternalAdminPrefix+"/") {
			continue
		}
		accepted, ok := adminRouteTable[ri.Method+" "+ri.Path]
		if !ok {
			t.Fatalf("undeclared internal admin route %s %s", ri.Method, ri.Path)
		}
		seen++
		path := strings.NewReplacer(":id", uuid.NewString(), ":userId", uuid.NewString(), ":mediaId", uuid.NewString()).Replace(ri.Path)
		// Every OTHER permission, but none that admits this route → refused.
		var others []string
		for _, p := range AdminPermissions {
			admits := false
			for _, a := range accepted {
				admits = admits || a == p
			}
			if !admits {
				others = append(others, p)
			}
		}
		tok := rg.mint(t, rg.admin, AudienceTrustSafety, others, rg.actor.String())
		w := serveAdmin(rg.r, ri.Method, path, `{"status":"reviewing"}`, bearer(tok))
		if w.Code != http.StatusForbidden || adminErrorCode(w) != CodeAdminPermissionScope {
			t.Fatalf("%s %s without %v: status=%d code=%q, want 403 %s", ri.Method, ri.Path, accepted, w.Code, adminErrorCode(w), CodeAdminPermissionScope)
		}
	}
	if seen != len(adminRouteTable) {
		t.Fatalf("saw %d internal admin routes, want %d", seen, len(adminRouteTable))
	}
}

// A moderator holds reports.read, reports.act, appeals.act and grievances.act
// in identity's catalogue. With exactly those, the reports, appeals and
// grievance routes admit the token (today only the "admin" scope passes on
// the legacy path). The list routes are exercised against the database.
func TestAdminToken_ModeratorPermissionsWorkTheQueues(t *testing.T) {
	rg := newAdminTokenRig(t)
	tok := rg.mint(t, rg.admin, AudienceTrustSafety,
		[]string{PermReportsRead, PermReportsAct, PermAppealsAct, PermGrievancesAct}, rg.actor.String())
	cases := []struct{ method, path, body string }{
		{http.MethodGet, InternalAdminPrefix + "/reports/bad", ""},
		{http.MethodPatch, InternalAdminPrefix + "/reports/bad", `{"status":"reviewing"}`},
		{http.MethodPatch, InternalAdminPrefix + "/appeals/bad", `{"status":"upheld"}`},
		{http.MethodGet, InternalAdminPrefix + "/grievances?overdue=maybe", ""},
		{http.MethodGet, InternalAdminPrefix + "/grievances/bad", ""},
		{http.MethodGet, InternalAdminPrefix + "/grievances/bad/history", ""},
		{http.MethodPatch, InternalAdminPrefix + "/grievances/bad", `{"status":"acknowledged"}`},
	}
	for _, tc := range cases {
		if w := serveAdmin(rg.r, tc.method, tc.path, tc.body, bearer(tok)); w.Code != http.StatusBadRequest {
			t.Fatalf("%s %s: status=%d body=%s, want 400 from the handler (admitted)", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

// The LEGACY routes are unchanged: they still need the internal key and the
// "admin" scope, and a service token grants nothing there.
func TestAdminToken_LegacyRoutesUnchanged(t *testing.T) {
	rg := newAdminTokenRig(t)
	admin := uuid.New()
	legacy := "/v1/reports/not-a-uuid"
	if w := serveAdmin(rg.r, http.MethodGet, legacy, "", gatewayAdmin(admin, "admin")); w.Code != http.StatusBadRequest {
		t.Fatalf("legacy admin: status=%d body=%s, want 400 (admitted)", w.Code, w.Body.String())
	}
	if w := serveAdmin(rg.r, http.MethodGet, legacy, "", gatewayAdmin(admin, "moderator")); w.Code != http.StatusForbidden {
		t.Fatalf("legacy moderator: status=%d, want 403 (unchanged threshold)", w.Code)
	}
	tok := rg.mint(t, rg.admin, AudienceTrustSafety, AdminPermissions, admin.String())
	hdr := gatewayAdmin(admin, "user")
	hdr[ServiceAuthHeader] = "Bearer " + tok
	if w := serveAdmin(rg.r, http.MethodGet, legacy, "", hdr); w.Code != http.StatusForbidden {
		t.Fatalf("legacy with token and no admin scope: status=%d, want 403", w.Code)
	}
	noKey := bearer(tok)
	noKey["X-Scopes"] = "admin"
	if w := serveAdmin(rg.r, http.MethodGet, legacy, "", noKey); w.Code != http.StatusUnauthorized {
		t.Fatalf("legacy without key: status=%d, want 401", w.Code)
	}
	// Legacy PATCH still takes the actor from X-User-Id and still refuses none.
	w := serveAdmin(rg.r, http.MethodPatch, legacy, `{"status":"reviewing"}`,
		map[string]string{"X-Internal-Service-Key": tokenTestInternalKey, "X-Scopes": "admin"})
	if w.Code != http.StatusBadRequest || adminErrorCode(w) != codeActorRequired {
		t.Fatalf("legacy patch without actor: status=%d code=%q, want 400 %s", w.Code, adminErrorCode(w), codeActorRequired)
	}
}

// A deployment without admin-service registered accepts no admin token.
func TestAdminToken_NoVerifierRefuses(t *testing.T) {
	rg := newAdminTokenRig(t)
	tok := rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermReportsRead}, rg.actor.String())
	r := newTokenTestRouter(t, New(nil))
	if w := serveAdmin(r, http.MethodGet, reportDetailBad, "", bearer(tok)); w.Code != http.StatusUnauthorized || adminErrorCode(w) != CodeServiceCredentialRequired {
		t.Fatalf("no verifier: status=%d code=%q, want 401 %s", w.Code, adminErrorCode(w), CodeServiceCredentialRequired)
	}
}

func TestServiceCallersFromEnv(t *testing.T) {
	pub, _, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	if v, err := ServiceCallersFromEnv(func(string) string { return "" }); v != nil || err != nil {
		t.Fatalf("blank: v=%v err=%v, want nil nil", v, err)
	}
	bad := []map[string]string{
		{"SERVICE_CALLERS": "admin-service", "SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": pub, "SERVICE_CALLER_ADMIN_SERVICE_OPS": PermReportsRead},
		{"SERVICE_CALLERS": "admin-service", "SERVICE_CALLER_ADMIN_SERVICE_KID": "a1", "SERVICE_CALLER_ADMIN_SERVICE_OPS": PermReportsRead},
		{"SERVICE_CALLERS": "admin-service", "SERVICE_CALLER_ADMIN_SERVICE_KID": "a1", "SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": pub},
		{"SERVICE_CALLERS": " , "},
	}
	for i, env := range bad {
		if _, err := ServiceCallersFromEnv(func(k string) string { return env[k] }); err == nil {
			t.Fatalf("case %d: want a configuration error", i)
		}
	}
}
