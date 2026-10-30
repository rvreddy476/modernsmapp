package main

// NOTE: new files under cmd/server/ are git-ignored by the root `server` rule;
// this file must be added with `git add -f`.
//
// Wiring tests for the auth/edge audit fixes (H1 gateway half, M1, L1, L2,
// L3). They drive the production chain (edgeHandler / edgeChain +
// newCoreHandler + newRoute over the real route table) so a regression in how
// main composes the layers fails here even when the packages stay correct.
// The package-level rules are tested in pkg/publicauth, pkg/clientip,
// pkg/metricsgate, pkg/edgeheaders, pkg/sessionrevocation and pkg/tokenpolicy.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/atpost/api-gateway/pkg/clientip"
	"github.com/atpost/api-gateway/pkg/disabledprefixes"
	"github.com/atpost/api-gateway/pkg/sessionrevocation"
)

// ── H1: a bad credential on a public sign-in route is anonymous ──────────

func expiredGateToken(t *testing.T) string {
	return gateToken(t, "", map[string]any{"exp": time.Now().Add(-time.Hour).Unix()})
}

func TestStaleCredentialDoesNotBlockPublicAuthRoutes(t *testing.T) {
	gw, up := gateGateway(t, nil)
	stale := map[string]string{
		"expired":        expiredGateToken(t),
		"foreign secret": signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"}, map[string]any{"user_id": gateUserID, "exp": time.Now().Add(time.Hour).Unix()}, "not-the-secret"),
		"garbage":        "not.a.jwt",
		"admin session":  gateToken(t, "admin", nil),
	}
	for _, p := range []string{"/v1/auth/refresh", "/v1/auth/login", "/v1/auth/register", "/v1/auth/logout", "/v1/auth/oauth/google/callback"} {
		for name, tok := range stale {
			for _, viaCookie := range []bool{true, false} {
				g := gateReq{method: http.MethodPost, path: p, bearer: tok}
				label := p + " " + name + " bearer"
				if viaCookie {
					g = gateReq{method: http.MethodPost, path: p, userCookie: tok}
					label = p + " " + name + " cookie"
				}
				h := wantPassed(t, label, gateDo(t, gw, up, g))
				for _, hdr := range []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role"} {
					if got := h.Get(hdr); got != "" {
						t.Errorf("%s: %s = %q, want anonymous", label, hdr, got)
					}
				}
			}
		}
	}
	// A good token on a public route still identifies the caller.
	h := wantPassed(t, "valid refresh", gateDo(t, gw, up, gateReq{method: http.MethodPost, path: "/v1/auth/refresh", bearer: gateToken(t, "", nil)}))
	if got := h.Get("X-User-Id"); got != gateUserID {
		t.Errorf("valid token on refresh: X-User-Id %q, want %q", got, gateUserID)
	}
}

func TestStaleCredentialStillRefusedOnProtectedRoutes(t *testing.T) {
	gw, up := gateGateway(t, nil)
	tok := expiredGateToken(t)
	for _, p := range []string{"/v1/auth/me", "/v1/auth/logout-all", "/v1/auth/sessions", "/v1/auth/refresh-all", gateConsumerPath, "/v1/users/me"} {
		wantRefused(t, p+" bearer", gateDo(t, gw, up, gateReq{method: http.MethodPost, path: p, bearer: tok}), http.StatusUnauthorized, "UNAUTHORIZED")
		wantRefused(t, p+" cookie", gateDo(t, gw, up, gateReq{method: http.MethodPost, path: p, userCookie: tok}), http.StatusUnauthorized, "UNAUTHORIZED")
	}
	// The admin session on a consumer path that is not a sign-in route is
	// still the wrong session.
	wantRefused(t, "admin on /v1/auth/me", gateDo(t, gw, up, gateReq{path: "/v1/auth/me", bearer: gateToken(t, "admin", nil)}), http.StatusUnauthorized, "WRONG_SESSION")
}

func TestRevokedSessionCookieDoesNotBlockSignIn(t *testing.T) {
	const sid = "1b2c3d4e-5f60-4718-8293-a4b5c6d7e8f9"
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: 100 * time.Millisecond})
	t.Cleanup(func() { _ = rdb.Close() })
	gw, up := gateGateway(t, sessionrevocation.RedisChecker{RDB: rdb, Timeout: 200 * time.Millisecond})
	tok := gateToken(t, "", map[string]any{"sid": sid})
	_ = mr.Set("sess_revoked:"+sid, "1")

	for _, p := range []string{"/v1/auth/login", "/v1/auth/refresh"} {
		h := wantPassed(t, p+" revoked cookie", gateDo(t, gw, up, gateReq{method: http.MethodPost, path: p, userCookie: tok}))
		for _, hdr := range []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role", "X-Admin-MFA"} {
			if got := h.Get(hdr); got != "" {
				t.Errorf("%s revoked: %s = %q, want anonymous", p, hdr, got)
			}
		}
	}
	got := gateDo(t, gw, up, gateReq{path: gateConsumerPath, userCookie: tok})
	wantRefused(t, "revoked on feed", got, http.StatusUnauthorized, "SESSION_REVOKED")
}

// ── M1: the rate limit keys on the resolved client, not a header ─────────

func TestRateLimitIgnoresForgedClientHeaders(t *testing.T) {
	t.Setenv("RATE_LIMIT_IP_PER_SEC", "1") // burst 2
	t.Setenv("RATE_LIMIT_USER_PER_SEC", "1000")
	alb, err := clientip.New([]string{"10.30.0.0/16"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := edgeHandler(nil, alb, rateLimitMiddleware(ok))
	send := func(i int, client string) int {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
		req.RemoteAddr = "10.30.7.7:443" // the ALB
		forged := "198.51.100." + string(rune('1'+i))
		req.Header.Set("X-Real-IP", forged)
		req.Header.Set("X-Forwarded-For", forged+", "+client)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		return res.Code
	}
	codes := []int{send(0, "203.0.113.1"), send(1, "203.0.113.1"), send(2, "203.0.113.1")}
	if codes[2] != http.StatusTooManyRequests {
		t.Fatalf("one client cycling forged X-Real-IP/XFF: statuses %v, want the third limited", codes)
	}
	if c := send(3, "203.0.113.2"); c != http.StatusOK {
		t.Fatalf("a different real client: status %d, want its own bucket", c)
	}

	// The limiter on its own (resolver not in front) keys on the TCP peer and
	// never on a header, so a forged X-Real-IP per request still shares one
	// bucket.
	bare := rateLimitMiddleware(ok)
	var bareCodes []int
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
		req.RemoteAddr = "203.0.113.9:5555"
		req.Header.Set("X-Real-IP", "198.51.100."+string(rune('1'+i)))
		res := httptest.NewRecorder()
		bare.ServeHTTP(res, req)
		bareCodes = append(bareCodes, res.Code)
	}
	if bareCodes[2] != http.StatusTooManyRequests {
		t.Fatalf("bare limiter with forged X-Real-IP: statuses %v, want the third limited", bareCodes)
	}
}

func TestUpstreamSeesResolvedClientNotForgery(t *testing.T) {
	gw, up := gateGateway(t, nil)
	alb, err := clientip.New([]string{"10.30.0.0/16"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	h := edgeHandler(nil, alb, gw)
	req := httptest.NewRequest(http.MethodGet, gateConsumerPath, nil)
	req.RemoteAddr = "10.30.7.7:443"
	req.Header.Set("X-Real-IP", "198.51.100.9")
	req.Header.Set("X-Forwarded-For", "198.51.100.9, 203.0.113.4")
	h.ServeHTTP(httptest.NewRecorder(), req)
	up.mu.Lock()
	defer up.mu.Unlock()
	if len(up.seen) != 1 {
		t.Fatalf("hits %d, want 1", len(up.seen))
	}
	seen := up.seen[0]
	if got := seen.Get("X-Real-IP"); got != "203.0.113.4" {
		t.Errorf("upstream X-Real-IP %q, want the resolved client", got)
	}
	if xff := seen.Get("X-Forwarded-For"); strings.Contains(xff, "198.51.100.9") || !strings.HasPrefix(xff, "203.0.113.4") {
		t.Errorf("upstream X-Forwarded-For %q, want the resolved client first and no forgery", xff)
	}
}

func TestRateLimiterStoreIsBounded(t *testing.T) {
	s := newRateLimiterStore(1, 2)
	s.maxEntries = 50
	for i := 0; i < 1000; i++ {
		s.get("key-" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + string(rune(i)))
		if n := s.size(); n > s.maxEntries {
			t.Fatalf("store holds %d entries after %d keys, cap %d", n, i+1, s.maxEntries)
		}
	}
	// A key in use keeps its bucket while there is room.
	s2 := newRateLimiterStore(1, 1)
	s2.maxEntries = 10
	l := s2.get("hot")
	l.Allow()
	if s2.get("hot") != l {
		t.Fatal("existing key lost its limiter")
	}
}

// ── L1: /metrics is not served to the internet ───────────────────────────

func TestMetricsNotReachableThroughTheLoadBalancer(t *testing.T) {
	scrape := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("metric 1\n")) })
	alb, err := clientip.New([]string{"10.30.0.0/16"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	build := func(prom http.Handler) http.Handler {
		core := newCoreHandler(unreachableRoutes(t), prom, true, nil, disabledprefixes.Set{})
		passThrough := func(next http.Handler) http.Handler { return next }
		return edgeHandler(nil, alb, edgeChain(jwtKeySet{activeKID: "v1", activeSecret: "secret"}, devTestPolicy(), passThrough, nil, core))
	}
	get := func(h http.Handler, forwarded bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.RemoteAddr = "10.30.7.7:443"
		if forwarded {
			req.Header.Set("X-Forwarded-For", "203.0.113.4")
			req.Header.Set("X-Forwarded-Proto", "https")
		}
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		return res
	}
	public, internal := metricsWiring("", scrape)
	if internal != nil {
		t.Fatal("no METRICS_ADDR: want no internal listener")
	}
	gated := build(public)
	if res := get(gated, true); res.Code != http.StatusNotFound || strings.Contains(res.Body.String(), "metric") {
		t.Errorf("through the ALB: status %d body %q, want 404", res.Code, res.Body.String())
	}
	if res := get(gated, false); res.Code != http.StatusOK {
		t.Errorf("direct scrape: status %d, want 200", res.Code)
	}
	// METRICS_ADDR set: the public listener has no metrics handler at all,
	// and the internal one serves it.
	public, internal = metricsWiring(":9090", scrape)
	if internal == nil || internal.Addr != ":9090" {
		t.Fatalf("METRICS_ADDR set: internal listener %v, want :9090", internal)
	}
	if res := get(build(public), false); res.Code != http.StatusNotFound {
		t.Errorf("public listener with an internal metrics port: status %d, want 404", res.Code)
	}
	res := httptest.NewRecorder()
	internal.Handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if res.Code != http.StatusOK {
		t.Errorf("internal listener: status %d, want 200", res.Code)
	}
}

// ── L2: Vary: Origin ─────────────────────────────────────────────────────

func TestCORSResponsesVaryOnOrigin(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := edgeHandler([]string{"https://app.example"}, clientip.Resolver{}, ok)
	for _, tc := range []struct{ method, origin string }{
		{http.MethodGet, "https://app.example"},
		{http.MethodGet, "https://evil.example"},
		{http.MethodGet, ""},
		{http.MethodOptions, "https://app.example"},
	} {
		req := httptest.NewRequest(tc.method, "/v1/feed/home", nil)
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		found := false
		for _, v := range res.Header().Values("Vary") {
			for _, part := range strings.Split(v, ",") {
				if strings.TrimSpace(part) == "Origin" {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("%s origin %q: Vary %q, want Origin", tc.method, tc.origin, res.Header().Values("Vary"))
		}
	}
}

// ── L3: refresh cookies reach auth-service only ──────────────────────────

func TestRefreshCookieForwardedOnlyToAuthService(t *testing.T) {
	gw, up := gateGateway(t, nil)
	send := func(path string) string {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: "csrf_token", Value: "c"})
		req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "r"})
		req.AddCookie(&http.Cookie{Name: "admin_refresh_token", Value: "ar"})
		gw.ServeHTTP(httptest.NewRecorder(), req)
		up.mu.Lock()
		defer up.mu.Unlock()
		if len(up.seen) != 1 {
			t.Fatalf("%s: hits %d, want 1", path, len(up.seen))
		}
		c := up.seen[0].Get("Cookie")
		up.seen = nil
		return c
	}
	for _, p := range []string{gateConsumerPath, "/v1/media/x", "/v1/users/me", "/v1/chat/rooms"} {
		c := send(p)
		if strings.Contains(c, "refresh_token") {
			t.Errorf("%s: upstream Cookie %q carries a refresh token", p, c)
		}
		if !strings.Contains(c, "csrf_token=c") {
			t.Errorf("%s: upstream Cookie %q lost the other cookies", p, c)
		}
	}
	if c := send("/v1/auth/refresh"); !strings.Contains(c, "refresh_token=r") || !strings.Contains(c, "admin_refresh_token=ar") {
		t.Errorf("auth-service Cookie %q, want the refresh cookies", c)
	}
}
