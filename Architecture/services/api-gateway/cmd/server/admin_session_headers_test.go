package main

// NOTE: new files under cmd/server/ are git-ignored by the root `server` rule;
// this file must be added with `git add -f`.
//
// The admin 2FA session headers (X-Admin-MFA, X-Auth-Time, X-Step-Up-At) are
// what admin-service authorises admin and sensitive routes on. These tests
// drive the production chain (edgeChain + newCoreHandler + newRoute over the
// real route table) to a real upstream and assert what that upstream sees.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"
)

var adminSessionHeaders = []string{"X-Admin-MFA", "X-Auth-Time", "X-Step-Up-At"}

// headerUpstream records the full header set of every request it receives.
type headerUpstream struct {
	mu   sync.Mutex
	seen []http.Header
	srv  *httptest.Server
}

func newHeaderUpstream(t *testing.T) *headerUpstream {
	t.Helper()
	u := &headerUpstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.seen = append(u.seen, r.Header.Clone())
		u.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func headerGateway(t *testing.T, up *headerUpstream) http.Handler {
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
	return edgeChain(keys, devTestPolicy(), passThrough, nil, core)
}

// sendThrough sends req through the gateway and returns the headers the
// upstream received. Forged copies are injected under non-canonical map keys
// as well, since Header.Del alone would miss those.
func sendThrough(t *testing.T, gw http.Handler, up *headerUpstream, token string, forged map[string]string) http.Header {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/commerce/sellers/queue", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range forged {
		req.Header[k] = []string{v}
	}
	res := httptest.NewRecorder()
	gw.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", res.Code)
	}
	up.mu.Lock()
	defer up.mu.Unlock()
	if len(up.seen) != 1 {
		t.Fatalf("upstream hits %d, want 1", len(up.seen))
	}
	h := up.seen[0]
	up.seen = nil
	return h
}

var forgedAdminSession = map[string]string{
	"X-Admin-MFA":  "true",
	"x-admin-mfa":  "true",
	"X-ADMIN-MFA":  "true",
	"X-Auth-Time":  "1999999999",
	"x-auth-time":  "1999999999",
	"X-Step-Up-At": "1999999999",
	"x-step-up-at": "1999999999",
}

func sessionToken(t *testing.T, extra map[string]any) string {
	t.Helper()
	claims := map[string]any{
		"user_id": "44444444-4444-4444-8444-444444444444",
		"scopes":  "admin",
		"exp":     time.Now().Add(time.Hour).Unix(),
	}
	for k, v := range extra {
		claims[k] = v
	}
	return signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"}, claims, "secret")
}

func TestAnonymousRequestCarriesNoAdminSessionHeaders(t *testing.T) {
	up := newHeaderUpstream(t)
	gw := headerGateway(t, up)
	for _, forged := range []map[string]string{nil, forgedAdminSession} {
		h := sendThrough(t, gw, up, "", forged)
		for _, name := range adminSessionHeaders {
			if vs := h.Values(name); len(vs) != 0 {
				t.Errorf("anonymous upstream saw %s=%q, want absent", name, vs)
			}
		}
	}
}

func TestOldTokenGetsMFAFalseAndNoTimesEvenWhenForged(t *testing.T) {
	up := newHeaderUpstream(t)
	gw := headerGateway(t, up)
	h := sendThrough(t, gw, up, sessionToken(t, nil), forgedAdminSession)

	if vs := h.Values("X-Admin-MFA"); len(vs) != 1 || vs[0] != "false" {
		t.Errorf("X-Admin-MFA = %q, want exactly [false]", vs)
	}
	for _, name := range []string{"X-Auth-Time", "X-Step-Up-At"} {
		if vs := h.Values(name); len(vs) != 0 {
			t.Errorf("%s = %q, want absent for a token without the claim", name, vs)
		}
	}
}

func TestMFATokenStampsAllThreeFromClaimsNotClient(t *testing.T) {
	up := newHeaderUpstream(t)
	gw := headerGateway(t, up)
	authTime := time.Now().Add(-10 * time.Minute).Unix()
	stepUp := time.Now().Add(-30 * time.Second).Unix()
	token := sessionToken(t, map[string]any{
		"auth_time":  authTime,
		"amr":        []string{"pwd", "otp"},
		"admin_mfa":  true,
		"step_up_at": stepUp,
	})
	h := sendThrough(t, gw, up, token, forgedAdminSession)

	want := map[string]string{
		"X-Admin-MFA":  "true",
		"X-Auth-Time":  strconv.FormatInt(authTime, 10),
		"X-Step-Up-At": strconv.FormatInt(stepUp, 10),
	}
	for name, v := range want {
		if vs := h.Values(name); len(vs) != 1 || vs[0] != v {
			t.Errorf("%s = %q, want exactly [%s]", name, vs, v)
		}
	}
}

func TestExplicitMFAFalseWithoutStepUp(t *testing.T) {
	up := newHeaderUpstream(t)
	gw := headerGateway(t, up)
	authTime := time.Now().Add(-time.Minute).Unix()
	token := sessionToken(t, map[string]any{"auth_time": authTime, "admin_mfa": false})
	h := sendThrough(t, gw, up, token, map[string]string{"x-admin-mfa": "true", "X-Step-Up-At": "1999999999"})

	if vs := h.Values("X-Admin-MFA"); len(vs) != 1 || vs[0] != "false" {
		t.Errorf("X-Admin-MFA = %q, want exactly [false]", vs)
	}
	if vs := h.Values("X-Auth-Time"); len(vs) != 1 || vs[0] != strconv.FormatInt(authTime, 10) {
		t.Errorf("X-Auth-Time = %q, want [%d]", vs, authTime)
	}
	if vs := h.Values("X-Step-Up-At"); len(vs) != 0 {
		t.Errorf("X-Step-Up-At = %q, want absent", vs)
	}
}
