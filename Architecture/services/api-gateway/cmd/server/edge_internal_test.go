package main

// NOTE: new files under cmd/server/ are git-ignored by the root `server` rule;
// this file must be added with `git add -f`.
//
// These tests drive the chain main wires (edgeChain + newCoreHandler + newRoute
// over the real route table) against a recording upstream, so a regression in
// wiring — a re-added chain-wide key stamp, a scope exception, a skipped
// revocation lookup — fails here even if the packages stay correct.

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/atpost/api-gateway/pkg/disabledprefixes"
	"github.com/atpost/api-gateway/pkg/internalroutes"
	"github.com/atpost/api-gateway/pkg/sessionrevocation"
)

const edgeTestInternalKey = "edge-test-internal-key"

type upstreamHit struct {
	path     string
	key      string
	user     string
	revision string
}

type recordingUpstream struct {
	mu   sync.Mutex
	hits []upstreamHit
	srv  *httptest.Server
}

func newRecordingUpstream(t *testing.T) *recordingUpstream {
	t.Helper()
	u := &recordingUpstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.hits = append(u.hits, upstreamHit{path: r.URL.Path, key: r.Header.Get("X-Internal-Service-Key"), user: r.Header.Get("X-User-Id"), revision: r.Header.Get("X-Work-Revision")})
		u.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *recordingUpstream) take() []upstreamHit {
	u.mu.Lock()
	defer u.mu.Unlock()
	h := u.hits
	u.hits = nil
	return h
}

// edgeGateway builds the production chain with every route pointed at one
// recording upstream.
func edgeGateway(t *testing.T, up *recordingUpstream, checker sessionrevocation.Checker) http.Handler {
	t.Helper()
	target, err := url.Parse(up.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	var routes []route
	for _, rd := range routeDefinitions() {
		routes = append(routes, newRoute(rd.prefix, target, edgeTestInternalKey))
	}
	core := newCoreHandler(routes, nil, true, nil, disabledprefixes.Set{})
	passThrough := func(next http.Handler) http.Handler { return next }
	keys := jwtKeySet{activeKID: "v1", activeSecret: "secret"}
	return edgeChain(keys, devTestPolicy(), passThrough, checker, core)
}

func edgeToken(t *testing.T, scopes, sessionID string) string {
	t.Helper()
	claims := map[string]any{
		"user_id": "33333333-3333-4333-8333-333333333333",
		"exp":     time.Now().Add(time.Hour).Unix(),
	}
	if scopes != "" {
		claims["scopes"] = scopes
	}
	if sessionID != "" {
		claims["sid"] = sessionID
	}
	return signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"}, claims, "secret")
}

// adminEdgeToken is edgeToken for the admin console session (sk=admin).
func adminEdgeToken(t *testing.T, scopes, sessionID string) string {
	t.Helper()
	claims := map[string]any{
		"user_id": "33333333-3333-4333-8333-333333333333",
		"exp":     time.Now().Add(time.Hour).Unix(),
		"sk":      "admin",
	}
	if scopes != "" {
		claims["scopes"] = scopes
	}
	if sessionID != "" {
		claims["sid"] = sessionID
	}
	return signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"}, claims, "secret")
}

func TestEveryGatewayRouteHasAnInternalKeyDecision(t *testing.T) {
	var prefixes []string
	for _, rd := range routeDefinitions() {
		prefixes = append(prefixes, rd.prefix)
	}
	if err := internalroutes.GuardStampPolicy(prefixes); err != nil {
		t.Fatal(err)
	}
}

var edgeInternalTargets = []string{
	"/v1/wallet/internal/debit",
	"/v1/wallet/internal/refund",
	"/v1/wallet/internal/balance/11111111-1111-4111-8111-111111111111",
	"/v1/commerce/internal/sellers/11111111-1111-4111-8111-111111111111/approve",
	"/v1/commerce/internal/sellers/11111111-1111-4111-8111-111111111111/kyc/verify",
	"/v1/monetization/internal/charge-and-credit",
	"/v1/search/internal/reindex",
	"/v1/media/internal/orphan/11111111-1111-4111-8111-111111111111",
	"/v1/subtitles/internal/jobs",
	"/v1/permissions/internal/check",
	"/v1/audio-library/internal/tracks",
	"/v1/feed/internal/debug",
	"/v1/reviewer/internal/enqueue",
	"/v1/dating/internal/risk/11111111-1111-4111-8111-111111111111",
	"/v1/doorstep/internal/admin/professionals/11111111-1111-4111-8111-111111111111/approve",
	"/v1/doorstep/internal/admin/bookings/11111111-1111-4111-8111-111111111111/refund",
}

func TestEdgeRefusesInternalPathsForEveryCaller(t *testing.T) {
	up := newRecordingUpstream(t)
	gw := edgeGateway(t, up, nil)
	callers := map[string]string{
		"anonymous":  "",
		"user":       edgeToken(t, "user", ""),
		"moderator":  edgeToken(t, "moderator", ""),
		"admin":      edgeToken(t, "admin", ""),
		"superadmin": edgeToken(t, "superadmin admin moderator", ""),
	}
	for name, token := range callers {
		for _, p := range edgeInternalTargets {
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				req := httptest.NewRequest(method, p, nil)
				if token != "" {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				// A forged key from the client changes nothing.
				req.Header.Set("X-Internal-Service-Key", edgeTestInternalKey)
				res := httptest.NewRecorder()
				gw.ServeHTTP(res, req)
				if res.Code != http.StatusNotFound {
					t.Errorf("%s %s %s: status %d, want 404", name, method, p, res.Code)
				}
			}
		}
	}
	if hits := up.take(); len(hits) != 0 {
		t.Fatalf("internal paths reached an upstream: %+v", hits)
	}
}

func rawGet(t *testing.T, addr, target, token string) int {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("GET " + target + " HTTP/1.1\r\nHost: gateway\r\nAuthorization: Bearer " + token + "\r\nConnection: close\r\n\r\n"))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("%s: %v", target, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestEdgeRefusesNormalisationBypassesForSuperadmin(t *testing.T) {
	up := newRecordingUpstream(t)
	srv := httptest.NewServer(edgeGateway(t, up, nil))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	token := edgeToken(t, "superadmin admin moderator", "")

	for _, p := range []string{
		"/v1/wallet%2Finternal/debit",
		"/v1/wallet%2finternal%2fdebit",
		"/v1/wallet%252Finternal/debit",
		"/v1/wallet/x/../internal/debit",
		"/v1/wallet/x/..%2Finternal/debit",
		"/v1/wallet//internal//debit",
		"//v1/wallet/internal/debit",
		"/v1/wallet/INTERNAL/debit",
		"/v1/wallet/%69nternal/debit",
		"/v1/commerce/Internal/sellers/x/approve",
	} {
		if got := rawGet(t, addr, p, token); got != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", p, got)
		}
	}
	if hits := up.take(); len(hits) != 0 {
		t.Fatalf("bypass reached an upstream: %+v", hits)
	}
}

func TestInternalKeyIsStampedOnlyForUpstreamsThatNeedIt(t *testing.T) {
	up := newRecordingUpstream(t)
	gw := edgeGateway(t, up, nil)
	token := edgeToken(t, "user", "")

	cases := []struct {
		path      string
		wantStamp bool
	}{
		{"/v1/wallet/balance", false},
		{"/v1/billpay/payments", false},
		{"/v1/auth/me", false},
		{"/v1/media/uploads", false},
		{"/v1/chat/conversations", false},
		{"/v1/calls/history", false},
		{"/v1/commerce/products", true},
		{"/v1/posts/feed", true},
		{"/v1/doorstep/catalogue", true},
		{"/v1/partner/work-session", true},
		{"/v1/admin/commerce/sellers/queue", true},
	}
	for _, anonymous := range []bool{true, false} {
		for _, c := range cases {
			isAdmin := strings.HasPrefix(c.path, "/v1/admin/")
			if anonymous && isAdmin {
				continue // no admin session: refused 401 before routing
			}
			req := httptest.NewRequest(http.MethodGet, c.path, nil)
			switch {
			case isAdmin:
				req.AddCookie(&http.Cookie{Name: "admin_access_token", Value: adminEdgeToken(t, "admin", "")})
			case !anonymous:
				req.Header.Set("Authorization", "Bearer "+token)
			}
			req.Header.Set("X-Internal-Service-Key", "client-forged")
			res := httptest.NewRecorder()
			gw.ServeHTTP(res, req)
			hits := up.take()
			if res.Code != http.StatusOK || len(hits) != 1 {
				t.Fatalf("%s (anonymous=%t): status %d hits %d, want 200 and one hit", c.path, anonymous, res.Code, len(hits))
			}
			want := ""
			if c.wantStamp {
				want = edgeTestInternalKey
			}
			if hits[0].key != want {
				t.Errorf("%s (anonymous=%t): upstream saw key %q, want %q", c.path, anonymous, hits[0].key, want)
			}
		}
	}
}

func TestPartnerWorkReadbackGatewayIdentityAndRevision(t *testing.T) {
	up := newRecordingUpstream(t)
	gw := edgeGateway(t, up, nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/partner/work-session", nil)
	req.Header.Set("Authorization", "Bearer "+edgeToken(t, "user", ""))
	req.Header.Set("X-User-Id", "client-forged")
	req.Header.Set("X-Internal-Service-Key", "client-forged")
	req.Header.Set("X-Work-Revision", "42")
	res := httptest.NewRecorder()
	gw.ServeHTTP(res, req)
	hits := up.take()
	if res.Code != http.StatusOK || len(hits) != 1 {
		t.Fatalf("readback did not reach authority: status=%d hits=%d", res.Code, len(hits))
	}
	if h := hits[0]; h.key != edgeTestInternalKey || h.user != "33333333-3333-4333-8333-333333333333" || h.revision != "42" {
		t.Fatalf("gateway authentication or revision lost: %+v", h)
	}
}

func TestEdgeRefusesRevokedSessions(t *testing.T) {
	const sid = "7a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: 100 * time.Millisecond})
	t.Cleanup(func() { _ = rdb.Close() })
	up := newRecordingUpstream(t)
	gw := edgeGateway(t, up, sessionrevocation.RedisChecker{RDB: rdb, Timeout: 200 * time.Millisecond})

	do := func(path, token string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if strings.HasPrefix(path, "/v1/admin/") {
			req.AddCookie(&http.Cookie{Name: "admin_access_token", Value: token})
		} else {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res := httptest.NewRecorder()
		gw.ServeHTTP(res, req)
		up.take()
		return res.Code
	}

	live := edgeToken(t, "user", sid)
	if got := do("/v1/feed/home", live); got != http.StatusOK {
		t.Fatalf("unrevoked session: %d, want 200", got)
	}

	_ = mr.Set("sess_revoked:"+sid, "1")
	if got := do("/v1/feed/home", live); got != http.StatusUnauthorized {
		t.Fatalf("revoked session on consumer path: %d, want 401", got)
	}
	if got := do("/v1/admin/commerce/sellers/queue", adminEdgeToken(t, "admin", sid)); got != http.StatusUnauthorized {
		t.Fatalf("revoked session on admin path: %d, want 401", got)
	}

	mr.Close()
	other := edgeToken(t, "user", "8b2c3d4e-5f60-4b7c-9d8e-0f1a2b3c4d5e")
	if got := do("/v1/feed/home", other); got != http.StatusOK {
		t.Errorf("redis down, consumer path: %d, want 200 (fail open)", got)
	}
	if got := do("/v1/admin/commerce/sellers/queue", adminEdgeToken(t, "user", "8b2c3d4e-5f60-4b7c-9d8e-0f1a2b3c4d5e")); got != http.StatusServiceUnavailable {
		t.Errorf("redis down, admin path: %d, want 503 (fail closed)", got)
	}
	if got := do("/v1/feed/home", edgeToken(t, "moderator", "8b2c3d4e-5f60-4b7c-9d8e-0f1a2b3c4d5e")); got != http.StatusServiceUnavailable {
		t.Errorf("redis down, moderator token on consumer path: %d, want 503 (fail closed)", got)
	}
}

// ─── /v1/subtitles ───────────────────────────────────────────────────
//
// media-service has served captions (GET/POST/PATCH /v1/subtitles/:mediaId
// plus /auto, /status, /request) for as long as the studio has existed, but
// the group is registered at the ROOT of that service rather than under
// /v1/media, so no gateway prefix ever matched it and every client got a 404
// from the edge. These tests hold the prefix in the table, keep it pointed at
// the media upstream, and keep it from shadowing or being shadowed.

const subtitlesMediaID = "44444444-4444-4444-8444-444444444444"

func TestSubtitlesPrefixTargetsTheMediaUpstream(t *testing.T) {
	var mediaTarget, subtitlesTarget string
	var found bool
	for _, rd := range routeDefinitions() {
		switch rd.prefix {
		case "/v1/media":
			mediaTarget = rd.target
		case "/v1/subtitles":
			subtitlesTarget, found = rd.target, true
		}
	}
	if !found {
		t.Fatal("no /v1/subtitles route: media-service captions are unreachable from every client")
	}
	if subtitlesTarget != mediaTarget {
		t.Errorf("/v1/subtitles -> %q, want the /v1/media upstream %q", subtitlesTarget, mediaTarget)
	}
	// Same upstream, same stamping decision: media-service reads the internal
	// key only on its /internal group, so stamping here would hand the
	// credential to anyone who can reach the service.
	if internalroutes.ShouldStamp("/v1/subtitles") {
		t.Error("/v1/subtitles must not carry X-Internal-Service-Key")
	}
}

func TestSubtitlesPrefixIsNotShadowed(t *testing.T) {
	defs := routeDefinitions()
	// The same walk newCoreHandler performs: first prefix in table order that
	// fits wins.
	match := func(path string) string {
		for _, rd := range defs {
			if path == rd.prefix || strings.HasPrefix(path, rd.prefix+"/") {
				return rd.prefix
			}
		}
		return ""
	}
	for _, p := range []string{
		"/v1/subtitles",
		"/v1/subtitles/" + subtitlesMediaID,
		"/v1/subtitles/" + subtitlesMediaID + "/auto",
		"/v1/subtitles/" + subtitlesMediaID + "/status",
		"/v1/subtitles/" + subtitlesMediaID + "/request",
	} {
		if got := match(p); got != "/v1/subtitles" {
			t.Errorf("%s matched %q, want /v1/subtitles", p, got)
		}
	}
	// And nothing else changed hands: every other prefix still claims its own
	// root and its own sub-paths.
	for _, rd := range defs {
		if rd.prefix == "/v1/subtitles" {
			continue
		}
		if got := match(rd.prefix); got != rd.prefix {
			t.Errorf("%s is now shadowed by %q", rd.prefix, got)
		}
		if got := match(rd.prefix + "/x"); got != rd.prefix {
			t.Errorf("%s/x is now shadowed by %q", rd.prefix, got)
		}
	}
}

func TestSubtitlesRoutesReachTheUpstream(t *testing.T) {
	up := newRecordingUpstream(t)
	gw := edgeGateway(t, up, nil)
	token := edgeToken(t, "user", "")

	cases := []struct {
		method    string
		path      string
		anonymous bool
	}{
		// A player rendering a public video reads the track list and the
		// caption job state without signing in.
		{http.MethodGet, "/v1/subtitles/" + subtitlesMediaID, true},
		{http.MethodGet, "/v1/subtitles/" + subtitlesMediaID + "/status", true},
		// Writes are owner-only upstream; the edge only has to deliver them.
		{http.MethodPost, "/v1/subtitles/" + subtitlesMediaID, false},
		{http.MethodPatch, "/v1/subtitles/" + subtitlesMediaID, false},
		{http.MethodPost, "/v1/subtitles/" + subtitlesMediaID + "/auto", false},
		{http.MethodPost, "/v1/subtitles/" + subtitlesMediaID + "/request", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		if !c.anonymous {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		// A forged key from the client must not survive to the upstream.
		req.Header.Set("X-Internal-Service-Key", "client-forged")
		res := httptest.NewRecorder()
		gw.ServeHTTP(res, req)
		hits := up.take()
		if res.Code != http.StatusOK || len(hits) != 1 {
			t.Fatalf("%s %s: status %d hits %d, want 200 and one hit", c.method, c.path, res.Code, len(hits))
		}
		if hits[0].path != c.path {
			t.Errorf("%s %s: upstream saw %q", c.method, c.path, hits[0].path)
		}
		if hits[0].key != "" {
			t.Errorf("%s %s: upstream saw internal key %q, want none", c.method, c.path, hits[0].key)
		}
	}
}

func TestEdgeRefusesInternalPathsUnderSubtitles(t *testing.T) {
	up := newRecordingUpstream(t)
	srv := httptest.NewServer(edgeGateway(t, up, nil))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	// The strongest caller there is: a new prefix must not reopen the
	// service-only surface for a superadmin either.
	token := edgeToken(t, "superadmin admin moderator", "")

	for _, p := range []string{
		"/v1/subtitles/internal/jobs",
		"/v1/subtitles/INTERNAL/jobs",
		"/v1/subtitles%2Finternal/jobs",
		"/v1/subtitles/%69nternal/jobs",
		"/v1/subtitles/x/../internal/jobs",
		"/v1/subtitles//internal//jobs",
	} {
		if got := rawGet(t, addr, p, token); got != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", p, got)
		}
	}
	if hits := up.take(); len(hits) != 0 {
		t.Fatalf("internal path under /v1/subtitles reached an upstream: %+v", hits)
	}
}

// ─── /v1/doorstep ────────────────────────────────────────────────────
//
// Doorstep's admin family lives at /v1/doorstep/internal/admin/* and is for
// admin-service in-cluster only. Through the real chain, with the product
// OPEN and the caller on the pilot list holding every platform role, it is
// still 404 and never reaches doorstep-service; the user routes do reach it,
// stamped with the internal key, and only for the pilot while closed.
func TestDoorstepInternalAdminIsNeverReachableFromTheEdge(t *testing.T) {
	const pilotUser = "33333333-3333-4333-8333-333333333333" // edgeToken's user
	up := newRecordingUpstream(t)
	target, err := url.Parse(up.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	var routes []route
	for _, rd := range routeDefinitions() {
		routes = append(routes, newRoute(rd.prefix, target, edgeTestInternalKey))
	}
	keys := jwtKeySet{activeKID: "v1", activeSecret: "secret"}
	passThrough := func(next http.Handler) http.Handler { return next }
	build := func(env map[string]string) http.Handler {
		products, err := dormantProductsFromEnv(envOf(env), true)
		if err != nil {
			t.Fatal(err)
		}
		core := newCoreHandler(routes, nil, true, products, disabledprefixes.Set{})
		return edgeChain(keys, devTestPolicy(), passThrough, nil, core)
	}
	superadmin := edgeToken(t, "superadmin admin moderator", "")

	for name, env := range map[string]map[string]string{
		"open":           {"DOORSTEP_PUBLIC_ENABLED": "true"},
		"closed, pilot":  {"DOORSTEP_PILOT_USER_IDS": pilotUser},
		"closed, nobody": nil,
	} {
		gw := build(env)
		for _, p := range []string{
			"/v1/doorstep/internal/admin/professionals",
			"/v1/doorstep/internal/admin/bookings/x/refund",
			"/v1/doorstep/INTERNAL/admin/stats",
			"/v1/doorstep%2Finternal/admin/audit",
			"/v1/doorstep/x/../internal/admin/config",
		} {
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				req := httptest.NewRequest(method, p, nil)
				req.Header.Set("Authorization", "Bearer "+superadmin)
				req.Header.Set("X-Internal-Service-Key", edgeTestInternalKey)
				res := httptest.NewRecorder()
				gw.ServeHTTP(res, req)
				if res.Code != http.StatusNotFound {
					t.Errorf("%s: %s %s gave %d, want 404", name, method, p, res.Code)
				}
			}
		}
		if hits := up.take(); len(hits) != 0 {
			t.Fatalf("%s: a doorstep internal path reached the upstream: %+v", name, hits)
		}

		req := httptest.NewRequest(http.MethodGet, "/v1/doorstep/catalogue", nil)
		req.Header.Set("Authorization", "Bearer "+edgeToken(t, "user", ""))
		req.Header.Set("X-Internal-Service-Key", "client-forged")
		res := httptest.NewRecorder()
		gw.ServeHTTP(res, req)
		hits := up.take()
		if name == "closed, nobody" {
			if res.Code != http.StatusNotFound || len(hits) != 0 {
				t.Errorf("%s: catalogue gave %d with %d hits, want 404 and none", name, res.Code, len(hits))
			}
			continue
		}
		if res.Code != http.StatusOK || len(hits) != 1 || hits[0].key != edgeTestInternalKey {
			t.Errorf("%s: catalogue gave %d hits %+v, want 200 with the gateway's key", name, res.Code, hits)
		}
	}
}

// ─── /v1/permissions and /v1/audio-library ───────────────────────────
//
// Both route families were registered upstream and had no gateway prefix:
// graph-service's permission check API sits at its root beside /v1/graph,
// and media-service's sound library is "/v1/audio-library", which the
// segment-boundary match never gives to "/v1/audio". These tests hold each
// prefix on its owner's upstream, hold the stamping decision, show that the
// permission checks can only ever carry the verified actor, and keep every
// `internal` sub-path closed.

const edgeTestUserID = "33333333-3333-4333-8333-333333333333" // edgeToken's user

func TestPermissionsAndAudioLibraryTargetTheirOwners(t *testing.T) {
	targets := map[string]string{}
	for _, rd := range routeDefinitions() {
		targets[rd.prefix] = rd.target
	}
	for prefix, owner := range map[string]string{
		"/v1/permissions":   "/v1/graph", // graph-service
		"/v1/audio-library": "/v1/media", // media-service
	} {
		got, ok := targets[prefix]
		if !ok {
			t.Errorf("no %s route: its upstream handlers are unreachable from every client", prefix)
			continue
		}
		if got != targets[owner] {
			t.Errorf("%s -> %q, want the %s upstream %q", prefix, got, owner, targets[owner])
		}
	}
	// graph-service gates its whole engine on the key, so the permission
	// checks need it exactly as /v1/graph does. media-service reads it only
	// on its internal groups, so the library must never carry it.
	if !internalroutes.ShouldStamp("/v1/permissions") {
		t.Error("/v1/permissions must carry the key: graph-service's RequireInternalKey is engine-wide")
	}
	if internalroutes.ShouldStamp("/v1/audio-library") {
		t.Error("/v1/audio-library must not carry X-Internal-Service-Key")
	}
}

func TestPermissionsAndAudioLibraryAreNotShadowed(t *testing.T) {
	defs := routeDefinitions()
	match := func(path string) string {
		for _, rd := range defs {
			if path == rd.prefix || strings.HasPrefix(path, rd.prefix+"/") {
				return rd.prefix
			}
		}
		return ""
	}
	for path, want := range map[string]string{
		"/v1/permissions/check":                  "/v1/permissions",
		"/v1/permissions/check-batch":            "/v1/permissions",
		"/v1/audio-library":                      "/v1/audio-library",
		"/v1/audio-library/" + subtitlesMediaID:  "/v1/audio-library",
		"/v1/audio/trending":                     "/v1/audio",
		"/v1/audio/" + subtitlesMediaID + "/url": "/v1/audio",
		"/v1/graph/relationship":                 "/v1/graph",
	} {
		if got := match(path); got != want {
			t.Errorf("%s matched %q, want %q", path, got, want)
		}
	}
	// Near-prefix siblings belong to nobody.
	for _, path := range []string{"/v1/permissionsx/check", "/v1/audio-libraryx"} {
		if got := match(path); got != "" {
			t.Errorf("%s matched %q, want no route", path, got)
		}
	}
}

// graphLikeUpstream answers the way graph-service's permission handlers do:
// RequireInternalKey on the engine, then getUserID, which is 401 unless
// X-User-Id is a UUID. It records the identity each admitted call carried.
func graphLikeUpstream(t *testing.T) (*httptest.Server, func() []upstreamHit) {
	t.Helper()
	var mu sync.Mutex
	var hits []upstreamHit
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Service-Key") != edgeTestInternalKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		user := r.Header.Get("X-User-Id")
		mu.Lock()
		hits = append(hits, upstreamHit{path: r.URL.Path, key: r.Header.Get("X-Internal-Service-Key"), user: user})
		mu.Unlock()
		if !isUUID(user) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	take := func() []upstreamHit {
		mu.Lock()
		defer mu.Unlock()
		h := hits
		hits = nil
		return h
	}
	return srv, take
}

func TestPermissionChecksRefuseAnonymousCallersAndCarryOnlyTheVerifiedActor(t *testing.T) {
	srv, take := graphLikeUpstream(t)
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	var routes []route
	for _, rd := range routeDefinitions() {
		routes = append(routes, newRoute(rd.prefix, target, edgeTestInternalKey))
	}
	core := newCoreHandler(routes, nil, true, nil, disabledprefixes.Set{})
	passThrough := func(next http.Handler) http.Handler { return next }
	gw := edgeChain(jwtKeySet{activeKID: "v1", activeSecret: "secret"}, devTestPolicy(), passThrough, nil, core)

	const victim = "55555555-5555-4555-8555-555555555555"
	check := "/v1/permissions/check?target_user_id=" + victim + "&actions=message,call"
	batch := "/v1/permissions/check-batch"
	do := func(method, target, token string) (int, []upstreamHit) {
		body := ""
		if method == http.MethodPost {
			body = `{"target_user_ids":["` + victim + `"],"actions":["message"]}`
		}
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		// A client naming someone else as the actor, and forging the key,
		// changes nothing.
		req.Header.Set("X-User-Id", victim)
		req.Header.Set("X-Verified-User-Id", victim)
		req.Header.Set("X-Internal-Service-Key", "client-forged")
		res := httptest.NewRecorder()
		gw.ServeHTTP(res, req)
		return res.Code, take()
	}

	for _, c := range []struct{ method, path string }{{http.MethodGet, check}, {http.MethodPost, batch}} {
		// Anonymous: delivered with the gateway's key but NO actor, so the
		// upstream refuses it. The forged actor never arrives.
		code, hits := do(c.method, c.path, "")
		if code != http.StatusUnauthorized {
			t.Errorf("anonymous %s %s: status %d, want 401", c.method, c.path, code)
		}
		if len(hits) != 1 || hits[0].user != "" {
			t.Errorf("anonymous %s %s: upstream saw %+v, want one call with no X-User-Id", c.method, c.path, hits)
		}

		// A bad token is refused at the edge and never reaches graph-service.
		code, hits = do(c.method, c.path, "not-a-jwt")
		if code != http.StatusUnauthorized || len(hits) != 0 {
			t.Errorf("bad token %s %s: status %d hits %d, want 401 and none", c.method, c.path, code, len(hits))
		}

		// Signed in: the actor is the token's subject, never the forged one,
		// and the upstream gets the gateway's key.
		code, hits = do(c.method, c.path, edgeToken(t, "user", ""))
		if code != http.StatusOK || len(hits) != 1 {
			t.Fatalf("signed-in %s %s: status %d hits %d, want 200 and one hit", c.method, c.path, code, len(hits))
		}
		wantPath := strings.SplitN(c.path, "?", 2)[0]
		if h := hits[0]; h.user != edgeTestUserID || h.key != edgeTestInternalKey || h.path != wantPath {
			t.Errorf("signed-in %s %s: upstream saw %+v, want user %s, the gateway key and path %s", c.method, c.path, h, edgeTestUserID, wantPath)
		}
	}
}

func TestAudioLibraryIsPublicAndUnstamped(t *testing.T) {
	up := newRecordingUpstream(t)
	gw := edgeGateway(t, up, nil)
	token := edgeToken(t, "user", "")

	for _, c := range []struct {
		path      string
		anonymous bool
		wantUser  string
	}{
		// The catalogue is the same for every caller; a signed-out studio
		// browses it.
		{"/v1/audio-library", true, ""},
		{"/v1/audio-library/" + subtitlesMediaID, true, ""},
		{"/v1/audio-library", false, edgeTestUserID},
		{"/v1/audio-library/" + subtitlesMediaID, false, edgeTestUserID},
	} {
		req := httptest.NewRequest(http.MethodGet, c.path+"?genre=pop&limit=20", nil)
		if !c.anonymous {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("X-User-Id", "client-forged")
		req.Header.Set("X-Internal-Service-Key", "client-forged")
		res := httptest.NewRecorder()
		gw.ServeHTTP(res, req)
		hits := up.take()
		if res.Code != http.StatusOK || len(hits) != 1 {
			t.Fatalf("%s (anonymous=%t): status %d hits %d, want 200 and one hit", c.path, c.anonymous, res.Code, len(hits))
		}
		if h := hits[0]; h.path != c.path || h.key != "" || h.user != c.wantUser {
			t.Errorf("%s (anonymous=%t): upstream saw %+v, want path %s, no key, user %q", c.path, c.anonymous, h, c.path, c.wantUser)
		}
	}
}

func TestEdgeRefusesInternalPathsUnderPermissionsAndAudioLibrary(t *testing.T) {
	up := newRecordingUpstream(t)
	srv := httptest.NewServer(edgeGateway(t, up, nil))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	// The strongest caller there is: a new prefix must not reopen the
	// service-only surface for a superadmin either.
	token := edgeToken(t, "superadmin admin moderator", "")

	for _, prefix := range []string{"/v1/permissions", "/v1/audio-library"} {
		for _, p := range []string{
			prefix + "/internal/check",
			prefix + "/INTERNAL/check",
			prefix + "%2Finternal/check",
			prefix + "/%69nternal/check",
			prefix + "/x/../internal/check",
			prefix + "//internal//check",
			// graph-service's real service-only family, reached by walking
			// out of the new prefix.
			prefix + "/../internal/graph/can",
			prefix + "/..%2Finternal/graph/blocked-any",
		} {
			if got := rawGet(t, addr, p, token); got != http.StatusNotFound {
				t.Errorf("%s: status %d, want 404", p, got)
			}
		}
	}
	if hits := up.take(); len(hits) != 0 {
		t.Fatalf("an internal path under a new prefix reached an upstream: %+v", hits)
	}
}
