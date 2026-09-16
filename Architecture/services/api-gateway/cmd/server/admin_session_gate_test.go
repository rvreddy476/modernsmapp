package main

// NOTE: new files under cmd/server/ are git-ignored by the root `server` rule;
// this file must be added with `git add -f`.
//
// The admin console session at the edge (pkg/adminsession). These tests drive
// the production chain — edgeChain + newCoreHandler + newRoute over the real
// route table — to a header-recording upstream, so they fail on a wiring
// regression even when the package helpers stay correct.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/atpost/api-gateway/pkg/sessionrevocation"
)

const (
	gateAdminPath    = "/v1/admin/commerce/sellers/queue"
	gateConsumerPath = "/v1/feed/home"
	gateUserID       = "55555555-5555-4555-8555-555555555555"
	gateCSRF         = "csrf-value-for-tests"
)

func gateGateway(t *testing.T, checker sessionrevocation.Checker) (http.Handler, *headerUpstream) {
	t.Helper()
	up := newHeaderUpstream(t)
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
	return edgeChain(keys, devTestPolicy(), passThrough, checker, core), up
}

// gateToken mints a token; kind "" means no sk claim (a consumer token).
func gateToken(t *testing.T, kind string, extra map[string]any) string {
	t.Helper()
	claims := map[string]any{
		"user_id": gateUserID,
		"scopes":  "admin",
		"exp":     time.Now().Add(time.Hour).Unix(),
	}
	if kind != "" {
		claims["sk"] = kind
	}
	for k, v := range extra {
		claims[k] = v
	}
	return signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"}, claims, "secret")
}

type gateReq struct {
	method      string
	path        string
	adminCookie string
	userCookie  string
	bearer      string
	csrfCookie  string
	csrfHeader  string
}

type gateResult struct {
	status int
	code   string
	hits   []http.Header
}

func gateDo(t *testing.T, gw http.Handler, up *headerUpstream, g gateReq) gateResult {
	t.Helper()
	method := g.method
	if method == "" {
		method = http.MethodGet
	}
	req := httptest.NewRequest(method, g.path, nil)
	if g.adminCookie != "" {
		req.AddCookie(&http.Cookie{Name: "admin_access_token", Value: g.adminCookie})
	}
	if g.userCookie != "" {
		req.AddCookie(&http.Cookie{Name: "access_token", Value: g.userCookie})
	}
	if g.csrfCookie != "" {
		req.AddCookie(&http.Cookie{Name: "admin_csrf_token", Value: g.csrfCookie})
	}
	if g.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+g.bearer)
	}
	if g.csrfHeader != "" {
		req.Header.Set("X-CSRF-Token", g.csrfHeader)
	}
	res := httptest.NewRecorder()
	gw.ServeHTTP(res, req)

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &body)

	up.mu.Lock()
	hits := up.seen
	up.seen = nil
	up.mu.Unlock()
	return gateResult{status: res.Code, code: body.Error.Code, hits: hits}
}

func wantRefused(t *testing.T, name string, got gateResult, status int, code string) {
	t.Helper()
	if got.status != status || got.code != code {
		t.Errorf("%s: status %d code %q, want %d %q", name, got.status, got.code, status, code)
	}
	if len(got.hits) != 0 {
		t.Errorf("%s: reached the upstream %d times, want 0", name, len(got.hits))
	}
}

func wantPassed(t *testing.T, name string, got gateResult) http.Header {
	t.Helper()
	if got.status != http.StatusOK || len(got.hits) != 1 {
		t.Fatalf("%s: status %d code %q hits %d, want 200 and one hit", name, got.status, got.code, len(got.hits))
	}
	return got.hits[0]
}

func TestAdminSessionCookiePassesAndStampsIdentity(t *testing.T) {
	gw, up := gateGateway(t, nil)
	token := gateToken(t, "admin", map[string]any{"admin_mfa": true})
	h := wantPassed(t, "admin cookie", gateDo(t, gw, up, gateReq{
		path: gateAdminPath, adminCookie: token,
		// A forged Bearer alongside is ignored, and never forwarded.
		bearer: gateToken(t, "", map[string]any{"user_id": "66666666-6666-4666-8666-666666666666"}),
	}))
	for name, want := range map[string]string{
		"X-User-Id":          gateUserID,
		"X-Verified-User-Id": gateUserID,
		"X-Scopes":           "admin",
		"X-Admin-Role":       "admin",
		"X-Admin-MFA":        "true",
	} {
		if got := h.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if got := h.Get("Authorization"); got != "" {
		t.Errorf("Authorization forwarded on an admin path: %q", got)
	}
}

func TestAdminPathIgnoresConsumerCredentials(t *testing.T) {
	gw, up := gateGateway(t, nil)
	adminTok := gateToken(t, "admin", nil)
	consumerTok := gateToken(t, "", nil)
	for name, g := range map[string]gateReq{
		"consumer cookie":             {path: gateAdminPath, userCookie: consumerTok},
		"consumer bearer":             {path: gateAdminPath, bearer: consumerTok},
		"admin token as bearer":       {path: gateAdminPath, bearer: adminTok},
		"admin token as user cookie":  {path: gateAdminPath, userCookie: adminTok},
		"admin token in query":        {path: gateAdminPath + "?access_token=" + adminTok},
		"anonymous":                   {path: gateAdminPath},
		"admin bearer on a write":     {method: http.MethodPost, path: gateAdminPath, bearer: adminTok, csrfCookie: gateCSRF, csrfHeader: gateCSRF},
		"empty admin cookie + bearer": {path: gateAdminPath, adminCookie: "", bearer: adminTok},
	} {
		wantRefused(t, name, gateDo(t, gw, up, g), http.StatusUnauthorized, "ADMIN_SESSION_REQUIRED")
	}
}

// The path is classified the way the internal-route refusal classifies it, so
// a spelling that any upstream would clean to /v1/admin still needs the
// admin session.
func TestAdminPathNormalisationStillNeedsAdminSession(t *testing.T) {
	gw, _ := gateGateway(t, nil)
	srv := httptest.NewServer(gw)
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	consumerTok := gateToken(t, "", map[string]any{"admin_mfa": true})
	for _, p := range []string{
		"/v1/admin",
		"/V1/ADMIN/commerce/sellers/queue",
		"/v1//admin/commerce/sellers/queue",
		"//v1/admin/commerce/sellers/queue",
		"/v1/feed/../admin/commerce/sellers/queue",
		"/v1/%61dmin/commerce/sellers/queue",
		"/v1%2Fadmin/commerce/sellers/queue",
		"/v1/admin;x=1/commerce/sellers/queue",
	} {
		if got := rawGet(t, addr, p, consumerTok); got != http.StatusUnauthorized {
			t.Errorf("%s with a consumer bearer: status %d, want 401", p, got)
		}
	}
}

func TestAdminCookieWithoutAdminKindIsWrongSession(t *testing.T) {
	gw, up := gateGateway(t, nil)
	for name, tok := range map[string]string{
		"no sk":                   gateToken(t, "", nil),
		"sk=consumer":             gateToken(t, "consumer", nil),
		"sk=ADMIN (case)":         gateToken(t, "ADMIN", nil),
		"consumer with admin_mfa": gateToken(t, "", map[string]any{"admin_mfa": true, "auth_time": time.Now().Unix()}),
		"superadmin scope, no sk": gateToken(t, "", map[string]any{"scopes": "superadmin admin moderator", "admin_mfa": true}),
	} {
		wantRefused(t, name, gateDo(t, gw, up, gateReq{path: gateAdminPath, adminCookie: tok}), http.StatusUnauthorized, "WRONG_SESSION")
	}
}

func TestConsumerMFATokenCannotDriveAdmin(t *testing.T) {
	gw, up := gateGateway(t, nil)
	mfa := gateToken(t, "", map[string]any{"admin_mfa": true, "step_up_at": time.Now().Unix()})
	wantRefused(t, "as bearer", gateDo(t, gw, up, gateReq{path: gateAdminPath, bearer: mfa}), http.StatusUnauthorized, "ADMIN_SESSION_REQUIRED")
	wantRefused(t, "as consumer cookie", gateDo(t, gw, up, gateReq{path: gateAdminPath, userCookie: mfa}), http.StatusUnauthorized, "ADMIN_SESSION_REQUIRED")
	wantRefused(t, "copied into the admin cookie", gateDo(t, gw, up, gateReq{
		method: http.MethodPost, path: gateAdminPath, adminCookie: mfa, csrfCookie: gateCSRF, csrfHeader: gateCSRF,
	}), http.StatusUnauthorized, "WRONG_SESSION")
}

// Choice: an admin session is refused on every consumer path, so it can never
// stand in for a consumer session.
func TestAdminSessionTokenRefusedOnConsumerPaths(t *testing.T) {
	gw, up := gateGateway(t, nil)
	adminTok := gateToken(t, "admin", nil)
	for _, p := range []string{gateConsumerPath, "/v1/auth/me", "/v1/wallet/balance", "/v1/auth/admin-session/me"} {
		wantRefused(t, p+" bearer", gateDo(t, gw, up, gateReq{path: p, bearer: adminTok}), http.StatusUnauthorized, "WRONG_SESSION")
		wantRefused(t, p+" access_token cookie", gateDo(t, gw, up, gateReq{path: p, userCookie: adminTok}), http.StatusUnauthorized, "WRONG_SESSION")
	}

	// The admin cookie is not a consumer credential: on a consumer path it is
	// never read, so the request is anonymous rather than authenticated.
	h := wantPassed(t, "admin cookie on consumer path", gateDo(t, gw, up, gateReq{path: gateConsumerPath, adminCookie: adminTok}))
	if got := h.Get("X-User-Id"); got != "" {
		t.Errorf("admin cookie authenticated a consumer path as %q", got)
	}

	// A consumer token on a consumer path is unchanged.
	h = wantPassed(t, "consumer bearer", gateDo(t, gw, up, gateReq{path: gateConsumerPath, bearer: gateToken(t, "", nil)}))
	if got := h.Get("X-User-Id"); got != gateUserID {
		t.Errorf("consumer X-User-Id = %q, want %q", got, gateUserID)
	}
}

// identity reads its own admin cookies on /v1/auth/admin-session; the gateway
// neither demands the admin session nor CSRF there.
func TestAdminSessionAuthRoutesUnaffected(t *testing.T) {
	gw, up := gateGateway(t, nil)
	adminTok := gateToken(t, "admin", nil)
	for _, p := range []string{
		"/v1/auth/admin-session/login",
		"/v1/auth/admin-session/verify-2fa",
		"/v1/auth/admin-session/refresh",
		"/v1/auth/admin-session/logout",
	} {
		// Pre-login: nothing but the body.
		h := wantPassed(t, p+" anonymous", gateDo(t, gw, up, gateReq{method: http.MethodPost, path: p}))
		if got := h.Get("X-User-Id"); got != "" {
			t.Errorf("%s anonymous: X-User-Id %q", p, got)
		}
		// The console's cookies, with no CSRF header, reach identity untouched.
		h = wantPassed(t, p+" admin cookies", gateDo(t, gw, up, gateReq{
			method: http.MethodPost, path: p, adminCookie: adminTok, csrfCookie: gateCSRF,
		}))
		if !strings.Contains(h.Get("Cookie"), "admin_access_token=") {
			t.Errorf("%s: admin cookie not forwarded to identity", p)
		}
		// A consumer session on the same host behaves as it does today.
		h = wantPassed(t, p+" consumer bearer", gateDo(t, gw, up, gateReq{method: http.MethodPost, path: p, bearer: gateToken(t, "", nil)}))
		if got := h.Get("X-User-Id"); got != gateUserID {
			t.Errorf("%s consumer: X-User-Id %q, want %q", p, got, gateUserID)
		}
	}
}

func TestAdminWritesNeedCSRF(t *testing.T) {
	gw, up := gateGateway(t, nil)
	tok := gateToken(t, "admin", nil)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		wantRefused(t, m+" no header, no cookie", gateDo(t, gw, up, gateReq{method: m, path: gateAdminPath, adminCookie: tok}), http.StatusForbidden, "CSRF_FAILED")
		wantRefused(t, m+" no header", gateDo(t, gw, up, gateReq{method: m, path: gateAdminPath, adminCookie: tok, csrfCookie: gateCSRF}), http.StatusForbidden, "CSRF_FAILED")
		wantRefused(t, m+" no cookie", gateDo(t, gw, up, gateReq{method: m, path: gateAdminPath, adminCookie: tok, csrfHeader: gateCSRF}), http.StatusForbidden, "CSRF_FAILED")
		wantRefused(t, m+" mismatch", gateDo(t, gw, up, gateReq{method: m, path: gateAdminPath, adminCookie: tok, csrfCookie: gateCSRF, csrfHeader: gateCSRF + "x"}), http.StatusForbidden, "CSRF_FAILED")
		wantRefused(t, m+" prefix", gateDo(t, gw, up, gateReq{method: m, path: gateAdminPath, adminCookie: tok, csrfCookie: gateCSRF, csrfHeader: gateCSRF[:5]}), http.StatusForbidden, "CSRF_FAILED")
		wantPassed(t, m+" matching", gateDo(t, gw, up, gateReq{method: m, path: gateAdminPath, adminCookie: tok, csrfCookie: gateCSRF, csrfHeader: gateCSRF}))
	}
	for _, m := range []string{http.MethodGet, http.MethodHead} {
		wantPassed(t, m+" exempt", gateDo(t, gw, up, gateReq{method: m, path: gateAdminPath, adminCookie: tok}))
	}
}

func TestRevokedAdminSessionRefused(t *testing.T) {
	const sid = "9c3d4e5f-6071-4c8d-9e0f-1a2b3c4d5e6f"
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: 100 * time.Millisecond})
	t.Cleanup(func() { _ = rdb.Close() })
	gw, up := gateGateway(t, sessionrevocation.RedisChecker{RDB: rdb, Timeout: 200 * time.Millisecond})
	tok := gateToken(t, "admin", map[string]any{"sid": sid})

	wantPassed(t, "live admin session", gateDo(t, gw, up, gateReq{path: gateAdminPath, adminCookie: tok}))
	_ = mr.Set("sess_revoked:"+sid, "1")
	got := gateDo(t, gw, up, gateReq{
		method: http.MethodPost, path: gateAdminPath, adminCookie: tok, csrfCookie: gateCSRF, csrfHeader: gateCSRF,
	})
	if got.status != http.StatusUnauthorized || len(got.hits) != 0 {
		t.Fatalf("revoked admin session: status %d hits %d, want 401 and no hit", got.status, len(got.hits))
	}
}
