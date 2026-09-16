// Admin-service token tests (admin console Wave 2 — Chat). No database: a
// recording fake backend proves admission and the actor handed down; refusals
// are proven by the auth layer's own status and code. Audit rows and stats
// counts are in admin_token_integration_test.go.
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/channel-service/internal/store"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const adminTestInternalKey = "channel-admin-token-test-key"

// adminFakeBackend records the actor each admin write receives.
type adminFakeBackend struct {
	actors []uuid.UUID
	calls  int
}

func (f *adminFakeBackend) AdminListReports(context.Context, string, int, string) ([]store.ReportListItem, string, error) {
	f.calls++
	return []store.ReportListItem{}, "", nil
}

func (f *adminFakeBackend) AdminGetReport(_ context.Context, id uuid.UUID) (*store.ReportListItem, error) {
	f.calls++
	return &store.ReportListItem{ChannelReport: store.ChannelReport{ID: id}}, nil
}

func (f *adminFakeBackend) AdminDecideReport(_ context.Context, actor, id uuid.UUID, _, _ string) (*store.ReportListItem, error) {
	f.calls++
	f.actors = append(f.actors, actor)
	return &store.ReportListItem{ChannelReport: store.ChannelReport{ID: id}}, nil
}

func (f *adminFakeBackend) AdminSuspendChannel(_ context.Context, actor, id uuid.UUID, _ string) (*store.ChannelStatusChange, error) {
	f.calls++
	f.actors = append(f.actors, actor)
	return &store.ChannelStatusChange{ChannelID: id, Status: "suspended"}, nil
}

func (f *adminFakeBackend) AdminUnsuspendChannel(_ context.Context, actor, id uuid.UUID, _ string) (*store.ChannelStatusChange, error) {
	f.calls++
	f.actors = append(f.actors, actor)
	return &store.ChannelStatusChange{ChannelID: id, Status: "active"}, nil
}

func (f *adminFakeBackend) AdminStats(context.Context) (*store.AdminStats, error) {
	f.calls++
	return &store.AdminStats{}, nil
}

type adminTokenRig struct {
	r        *gin.Engine
	v        *servicetoken.Verifier
	be       *adminFakeBackend
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
	be := &adminFakeBackend{}
	return &adminTokenRig{
		r:        adminTokenRouter(be, v, adminTestInternalKey, true),
		v:        v,
		be:       be,
		admin:    mk(IssuerAdminService, "a1", aPriv),
		notifier: mk("notification-service", "n1", nPriv),
		rogue:    mk(IssuerAdminService, "a1", rPriv),
		actor:    uuid.New(),
	}
}

func adminTokenRouter(be ChannelAdmin, v *servicetoken.Verifier, key string, communities bool) *gin.Engine {
	h := New(nil).WithInternalKey(key).WithCommunitiesEnabled(communities).WithServiceAuth(v)
	h.admin = be
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

// forgedEdge is what a caller holding the internal key could send: the key,
// a forged gateway identity and admin scopes.
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

const adminBody = `{"decision":"uphold","reason":"spam network"}`

// adminRoutePermissions is the route table under test: method + suffix →
// the permission the route requires.
func adminRoutePermissions() map[string]string {
	return map[string]string{
		"GET /stats":                               PermStatsRead,
		"GET /channel-reports":                     PermReportsRead,
		"GET /channel-reports/:reportId":           PermReportsRead,
		"POST /channel-reports/:reportId/decision": PermReportsAct,
		"POST /channels/:channelId/suspend":        PermChannelsModerate,
		"POST /channels/:channelId/unsuspend":      PermChannelsModerate,
	}
}

// actorRoutes are the writes whose actor the backend records.
var actorRoutes = []string{
	"POST /channel-reports/:reportId/decision",
	"POST /channels/:channelId/suspend",
	"POST /channels/:channelId/unsuspend",
}

func TestAdminToken_RightScopeAdmittedActorIsAct(t *testing.T) {
	rg := newAdminTokenRig(t)
	perms := adminRoutePermissions()
	for _, route := range actorRoutes {
		method, suffix, _ := strings.Cut(route, " ")
		tok := rg.mint(t, rg.admin, AudienceChat, []string{perms[route]}, rg.actor.String())
		// A forged gateway identity and the key ride along; the actor is act.
		hdr := forgedEdge(uuid.New())
		hdr[ServiceAuthHeader] = "Bearer " + tok
		rg.be.actors = nil
		w := adminServe(rg.r, method, uuidParams(InternalAdminPrefix+suffix), adminBody, hdr)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s, want 200", route, w.Code, w.Body.String())
		}
		if len(rg.be.actors) != 1 || rg.be.actors[0] != rg.actor {
			t.Fatalf("%s: backend actors=%v, want [%s]", route, rg.be.actors, rg.actor)
		}
	}
}

func TestAdminToken_Refusals(t *testing.T) {
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
		{"payments audience", rg.mint(t, rg.admin, servicetoken.AudiencePayments, scope, actor), 0, CodeServiceTokenRejected},
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
			w := adminServe(rg.r, http.MethodPost, InternalAdminPrefix+"/channel-reports/"+uuid.NewString()+"/decision", adminBody, hdr)
			if w.Code != http.StatusForbidden || errorCode(t, w) != tc.wantCode {
				t.Fatalf("status=%d code=%q body=%s, want 403 %s", w.Code, errorCode(t, w), w.Body.String(), tc.wantCode)
			}
			if rg.be.calls != 0 {
				t.Fatalf("a refused request reached the backend")
			}
		})
	}
}

// The edge key plus a forged actor opens nothing: no token, no entry.
func TestAdminToken_KeyAndForgedActorRefused(t *testing.T) {
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
	if checked != len(adminRoutePermissions()) {
		t.Fatalf("checked %d admin routes, want %d", checked, len(adminRoutePermissions()))
	}
	if rg.be.calls != 0 {
		t.Fatalf("a request without a token reached the backend")
	}
}

func TestAdminToken_EveryRouteNeedsItsPermission(t *testing.T) {
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
			t.Fatalf("%s without %s: status=%d code=%q, want 403 %s", route, perm, w.Code, errorCode(t, w), CodeAdminPermissionScope)
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

// The token family does not depend on the internal key or the communities
// switch: no key configured, a key configured but not sent, and the product
// switched off all admit a good token.
func TestAdminToken_IndependentOfKeyAndKillSwitch(t *testing.T) {
	rg := newAdminTokenRig(t)
	tok := rg.mint(t, rg.admin, AudienceChat, []string{PermStatsRead}, rg.actor.String())
	for name, r := range map[string]*gin.Engine{
		"key configured, not sent": rg.r,
		"no key configured":        adminTokenRouter(rg.be, rg.v, "", true),
		"communities disabled":     adminTokenRouter(rg.be, rg.v, adminTestInternalKey, false),
	} {
		if w := adminServe(r, http.MethodGet, InternalAdminPrefix+"/stats", "", bearer(tok)); w.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s, want 200", name, w.Code, w.Body.String())
		}
	}
}

// The key routes are unchanged: still registered with the key, still 401
// without it, and a service token does not stand in for the key.
func TestAdminToken_KeyRoutesUnchanged(t *testing.T) {
	rg := newAdminTokenRig(t)
	routes := routeSet(rg.r)
	for _, want := range []string{
		"GET /internal/channel-reports",
		"POST /internal/channel-reports/:reportId/review",
		"POST /internal/channels/:channelId/suspend",
		"DELETE /internal/channels/:channelId/suspend",
	} {
		if !routes[want] {
			t.Fatalf("key route %q missing", want)
		}
	}
	tok := rg.mint(t, rg.admin, AudienceChat, AdminPermissions, rg.actor.String())
	for _, path := range []string{"/internal/channel-reports", "/v1/broadcast-channels/discover"} {
		if w := adminServe(rg.r, http.MethodGet, path, "", bearer(tok)); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s with a token but no key: status=%d, want 401", path, w.Code)
		}
	}
}

// A deployment without admin-service registered accepts no admin token.
func TestAdminToken_NoVerifierRefuses(t *testing.T) {
	rg := newAdminTokenRig(t)
	tok := rg.mint(t, rg.admin, AudienceChat, []string{PermStatsRead}, rg.actor.String())
	r := adminTokenRouter(&adminFakeBackend{}, nil, adminTestInternalKey, true)
	if w := adminServe(r, http.MethodGet, InternalAdminPrefix+"/stats", "", bearer(tok)); w.Code != http.StatusUnauthorized {
		t.Fatalf("no verifier: status=%d, want 401", w.Code)
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
