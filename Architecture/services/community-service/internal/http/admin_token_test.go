// Admin-service token tests (admin console Wave 2 — Chat, communities). No
// database: a recording fake backend proves admission and the actor handed
// down; refusals are proven by the auth layer's own status and code. Audit
// rows and stats are in admin_token_integration_test.go.
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/community-service/internal/store"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func init() { gin.SetMode(gin.TestMode) }

const adminTestInternalKey = "community-admin-token-test-key"

type adminFakeBackend struct {
	actors []uuid.UUID
	calls  int
}

func (f *adminFakeBackend) AdminListCommunityReports(context.Context, string, int, string) ([]store.CommunityReport, string, error) {
	f.calls++
	return []store.CommunityReport{}, "", nil
}

func (f *adminFakeBackend) AdminGetCommunityReport(_ context.Context, id uuid.UUID) (*store.CommunityReport, error) {
	f.calls++
	return &store.CommunityReport{ID: id}, nil
}

func (f *adminFakeBackend) AdminDecideCommunityReport(_ context.Context, actor, id uuid.UUID, status, _ string) (*store.CommunityReport, error) {
	f.calls++
	f.actors = append(f.actors, actor)
	return &store.CommunityReport{ID: id, Status: status}, nil
}

func (f *adminFakeBackend) AdminCommunityStats(context.Context) (*store.CommunityAdminStats, error) {
	f.calls++
	return &store.CommunityAdminStats{}, nil
}

type adminTokenRig struct {
	r        *gin.Engine
	v        *servicetoken.Verifier
	be       *adminFakeBackend
	admin    *servicetoken.Signer
	notifier *servicetoken.Signer
	rogue    *servicetoken.Signer
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
		"SERVICE_CALLER_NOTIFICATION_SERVICE_OPS":    strings.Join(AdminPermissions, ","),
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
	be := &adminFakeBackend{}
	return &adminTokenRig{
		r:        adminTokenRouter(be, v, adminTestInternalKey),
		v:        v,
		be:       be,
		admin:    mk(IssuerAdminService, "a1", aPriv),
		notifier: mk("notification-service", "n1", nPriv),
		rogue:    mk(IssuerAdminService, "a1", rPriv),
		actor:    uuid.New(),
	}
}

func adminTokenRouter(be CommunityAdmin, v *servicetoken.Verifier, key string) *gin.Engine {
	h := New(nil).WithInternalKey(key).WithServiceAuth(v)
	if be != nil {
		h.WithAdmin(be)
	}
	r := gin.New()
	r.Use(gin.Recovery())
	h.RegisterRoutes(r)
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

func forgedEdge(user uuid.UUID) map[string]string {
	return map[string]string{
		"X-Internal-Service-Key": adminTestInternalKey,
		"X-User-Id":              user.String(),
		"X-Scopes":               "superadmin admin moderator",
		"X-Admin-Id":             user.String(),
		"X-Actor-Id":             user.String(),
	}
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
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

const adminBody = `{"decision":"uphold","reason":"harassment in community"}`

func adminRoutePermissions() map[string]string {
	return map[string]string{
		"GET /stats":                       PermStatsRead,
		"GET /reports":                     PermReportsRead,
		"GET /reports/:reportId":           PermReportsRead,
		"POST /reports/:reportId/decision": PermReportsAct,
	}
}

func TestCommunityAdminToken_RightScopeAdmittedActorIsAct(t *testing.T) {
	rg := newAdminTokenRig(t)
	hdr := forgedEdge(uuid.New())
	hdr[ServiceAuthHeader] = "Bearer " + rg.mint(t, rg.admin, AudienceChat, []string{PermReportsAct}, rg.actor.String())
	w := adminServe(rg.r, http.MethodPost, InternalAdminPrefix+"/reports/"+uuid.NewString()+"/decision", adminBody, hdr)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", w.Code, w.Body.String())
	}
	if len(rg.be.actors) != 1 || rg.be.actors[0] != rg.actor {
		t.Fatalf("backend actors=%v, want [%s]", rg.be.actors, rg.actor)
	}
}

func TestCommunityAdminToken_Refusals(t *testing.T) {
	rg := newAdminTokenRig(t)
	actor := rg.actor.String()
	scope := []string{PermReportsAct}
	cases := []struct {
		name     string
		tok      string
		clock    time.Duration
		wantCode string
	}{
		{"wrong audience", rg.mint(t, rg.admin, "food", scope, actor), 0, CodeServiceTokenRejected},
		{"missing act", rg.mint(t, rg.admin, AudienceChat, scope, ""), 0, CodeAdminActorRequired},
		{"malformed act", rg.mint(t, rg.admin, AudienceChat, scope, "not-a-uuid"), 0, CodeAdminActorRequired},
		{"nil act", rg.mint(t, rg.admin, AudienceChat, scope, uuid.Nil.String()), 0, CodeAdminActorRequired},
		{"wrong scope", rg.mint(t, rg.admin, AudienceChat, []string{PermReportsRead}, actor), 0, CodeAdminPermissionScope},
		{"expired", rg.mint(t, rg.admin, AudienceChat, scope, actor), 2 * time.Minute, CodeServiceTokenRejected},
		{"unknown caller key", rg.mint(t, rg.rogue, AudienceChat, scope, actor), 0, CodeServiceTokenRejected},
		{"registered non-admin caller", rg.mint(t, rg.notifier, AudienceChat, scope, actor), 0, CodeServiceTokenRejected},
		{"garbage", "a.b.c", 0, CodeServiceTokenRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.clock != 0 {
				rg.v.SetClock(func() time.Time { return time.Now().Add(tc.clock) })
				defer rg.v.SetClock(time.Now)
			}
			hdr := forgedEdge(uuid.New())
			hdr[ServiceAuthHeader] = "Bearer " + tc.tok
			rg.be.calls = 0
			w := adminServe(rg.r, http.MethodPost, InternalAdminPrefix+"/reports/"+uuid.NewString()+"/decision", adminBody, hdr)
			if w.Code != http.StatusForbidden || errorCode(t, w) != tc.wantCode {
				t.Fatalf("status=%d code=%q body=%s, want 403 %s", w.Code, errorCode(t, w), w.Body.String(), tc.wantCode)
			}
			if rg.be.calls != 0 {
				t.Fatalf("a refused request reached the backend")
			}
		})
	}
}

func TestCommunityAdminToken_KeyAndForgedActorRefused(t *testing.T) {
	rg := newAdminTokenRig(t)
	var checked int
	for _, ri := range rg.r.Routes() {
		if !strings.HasPrefix(ri.Path, InternalAdminPrefix+"/") {
			continue
		}
		checked++
		w := adminServe(rg.r, ri.Method, uuidParams(ri.Path), adminBody, forgedEdge(uuid.New()))
		if w.Code != http.StatusUnauthorized || errorCode(t, w) != CodeAdminTokenRequired {
			t.Fatalf("%s %s: status=%d code=%q, want 401 %s", ri.Method, ri.Path, w.Code, errorCode(t, w), CodeAdminTokenRequired)
		}
	}
	if checked != len(adminRoutePermissions()) || rg.be.calls != 0 {
		t.Fatalf("checked %d routes (want %d), backend calls %d (want 0)", checked, len(adminRoutePermissions()), rg.be.calls)
	}
}

func TestCommunityAdminToken_EveryRouteNeedsItsPermission(t *testing.T) {
	rg := newAdminTokenRig(t)
	want := adminRoutePermissions()
	seen := 0
	for _, ri := range rg.r.Routes() {
		if !strings.HasPrefix(ri.Path, InternalAdminPrefix+"/") {
			continue
		}
		route := ri.Method + " " + strings.TrimPrefix(ri.Path, InternalAdminPrefix)
		perm, ok := want[route]
		if !ok {
			t.Fatalf("undeclared admin route %s", route)
		}
		seen++
		var others []string
		for _, p := range AdminPermissions {
			if p != perm {
				others = append(others, p)
			}
		}
		path := uuidParams(ri.Path)
		w := adminServe(rg.r, ri.Method, path, adminBody, bearer(rg.mint(t, rg.admin, AudienceChat, others, rg.actor.String())))
		if w.Code != http.StatusForbidden || errorCode(t, w) != CodeAdminPermissionScope {
			t.Fatalf("%s without %s: status=%d code=%q, want 403", route, perm, w.Code, errorCode(t, w))
		}
		w = adminServe(rg.r, ri.Method, path, adminBody, bearer(rg.mint(t, rg.admin, AudienceChat, []string{perm}, rg.actor.String())))
		if w.Code != http.StatusOK {
			t.Fatalf("%s with %s: status=%d body=%s, want 200", route, perm, w.Code, w.Body.String())
		}
	}
	if seen != len(want) {
		t.Fatalf("saw %d admin routes, want %d", seen, len(want))
	}
}

// The token family needs no key; the key routes still need the key, and a
// token does not stand in for it.
func TestCommunityAdminToken_KeyIndependentAndKeyRoutesUnchanged(t *testing.T) {
	rg := newAdminTokenRig(t)
	tok := rg.mint(t, rg.admin, AudienceChat, AdminPermissions, rg.actor.String())
	for name, r := range map[string]*gin.Engine{"key configured, not sent": rg.r, "no key configured": adminTokenRouter(rg.be, rg.v, "")} {
		if w := adminServe(r, http.MethodGet, InternalAdminPrefix+"/stats", "", bearer(tok)); w.Code != http.StatusOK {
			t.Fatalf("%s: status=%d, want 200", name, w.Code)
		}
	}
	if w := adminServe(rg.r, http.MethodGet, "/v1/communities/discover", "", bearer(tok)); w.Code != http.StatusUnauthorized {
		t.Fatalf("key route with a token but no key: status=%d, want 401", w.Code)
	}
}

func TestCommunityAdminToken_NoVerifierRefuses(t *testing.T) {
	rg := newAdminTokenRig(t)
	tok := rg.mint(t, rg.admin, AudienceChat, []string{PermStatsRead}, rg.actor.String())
	r := adminTokenRouter(&adminFakeBackend{}, nil, adminTestInternalKey)
	if w := adminServe(r, http.MethodGet, InternalAdminPrefix+"/stats", "", bearer(tok)); w.Code != http.StatusUnauthorized {
		t.Fatalf("no verifier: status=%d, want 401", w.Code)
	}
}

func TestCommunityServiceCallersFromEnv(t *testing.T) {
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
