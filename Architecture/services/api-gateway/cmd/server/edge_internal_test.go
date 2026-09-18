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

	"github.com/atpost/api-gateway/pkg/internalroutes"
	"github.com/atpost/api-gateway/pkg/sessionrevocation"
)

const edgeTestInternalKey = "edge-test-internal-key"

type upstreamHit struct {
	path string
	key  string
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
		u.hits = append(u.hits, upstreamHit{path: r.URL.Path, key: r.Header.Get("X-Internal-Service-Key")})
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
	core := newCoreHandler(routes, nil, true, nil)
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
	"/v1/feed/internal/debug",
	"/v1/reviewer/internal/enqueue",
	"/v1/dating/internal/risk/11111111-1111-4111-8111-111111111111",
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
