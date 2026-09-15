// Authorization tests for dating-service (Dating plan lane D1). No database:
// every case here is decided by the gateway-set headers and the configured
// credentials before a handler touches the store, so it runs in plain
// `go test ./...`. The DB-backed 200 paths and audit-actor checks live in
// authz_integration_test.go.
package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const testInternalKey = "d1-test-internal-key"

// newAuthRouter builds the real route table over a nil service. Requests the
// auth layer admits reach a handler; the cases below either stop before that
// or fail parameter parsing, which proves admission without a store.
func newAuthRouter(t *testing.T, key string, v *servicetoken.Verifier) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	h := New(nil)
	if key != "" {
		h.WithInternalKey(key)
	}
	if v != nil {
		h.WithServiceAuth(v)
	}
	h.RegisterRoutes(r)
	return r
}

func serve(r *gin.Engine, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	if body == "" {
		rd = bytes.NewReader(nil)
	} else {
		rd = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, path, rd)
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

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error *struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || env.Error == nil {
		return ""
	}
	return env.Error.Code
}

// gatewayUser is what the api-gateway forwards for a logged-in user: its
// injected internal key plus the identity it derived from the JWT.
func gatewayUser(userID uuid.UUID, scopes string) map[string]string {
	h := map[string]string{
		headerInternalKey:    testInternalKey,
		headerUserID:         userID.String(),
		headerVerifiedUserID: userID.String(),
	}
	if scopes != "" {
		h[headerScopes] = scopes
	}
	return h
}

var adminRoutes = []struct{ method, path, body string }{
	{http.MethodGet, "/v1/dating/admin/reports", ""},
	{http.MethodPost, "/v1/dating/admin/reports/" + uuid.NewString() + "/action", `{"action":"dismiss"}`},
	{http.MethodGet, "/v1/dating/admin/safety/panic", ""},
	{http.MethodPost, "/v1/dating/admin/safety/panic/" + uuid.NewString() + "/ack", ""},
	{http.MethodGet, "/v1/dating/admin/safety/panic/" + uuid.NewString(), ""},
	{http.MethodPost, "/v1/dating/admin/safety/panic/" + uuid.NewString() + "/resolve", `{"note":"x"}`},
	{http.MethodGet, "/v1/dating/admin/photos/pending", ""},
	{http.MethodGet, "/v1/dating/admin/audit", ""},
	{http.MethodGet, "/v1/dating/admin/risk", ""},
	{http.MethodPost, "/v1/dating/photos/" + uuid.NewString() + "/moderation", `{"status":"approved"}`},
}

// Every admin route and photo moderation: anonymous → 401, a plain user
// holding the gateway-injected key → 403 ADMIN_SCOPE_REQUIRED. A forged
// X-Admin-Id or a look-alike scope changes nothing.
func TestAdminRoutes_RefuseAnonymousAndPlainUsers(t *testing.T) {
	r := newAuthRouter(t, testInternalKey, nil)
	user := uuid.New()
	for _, rt := range adminRoutes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			w := serve(r, rt.method, rt.path, rt.body, map[string]string{headerInternalKey: testInternalKey})
			if w.Code != http.StatusUnauthorized || errorCode(t, w) != CodeAuthRequired {
				t.Fatalf("anonymous: status=%d code=%q body=%s, want 401 %s", w.Code, errorCode(t, w), w.Body.String(), CodeAuthRequired)
			}

			for _, scopes := range []string{"", "feed:read dating:write", "administrator", "Admin"} {
				hdr := gatewayUser(user, scopes)
				hdr["X-Admin-Id"] = uuid.NewString() // never trusted
				w = serve(r, rt.method, rt.path, rt.body, hdr)
				if w.Code != http.StatusForbidden || errorCode(t, w) != CodeAdminScopeRequired {
					t.Fatalf("user scopes=%q: status=%d code=%q body=%s, want 403 %s",
						scopes, w.Code, errorCode(t, w), w.Body.String(), CodeAdminScopeRequired)
				}
			}
		})
	}
}

func TestHasAdminScope(t *testing.T) {
	for scopes, want := range map[string]bool{
		"":                          false,
		"user":                      false,
		"administrator moderators":  false,
		"admin":                     true,
		"feed:read moderator":       true,
		"superadmin":                true,
		"  dating:write   admin   ": true,
	} {
		if got := hasAdminScope(scopes); got != want {
			t.Errorf("hasAdminScope(%q)=%v want %v", scopes, got, want)
		}
	}
}

// internalCases uses an unparseable id so an admitted request stops at 400
// INVALID_ID inside the handler, before any store call.
var internalCases = []struct{ name, method, path, body, op string }{
	{"preview", http.MethodGet, "/v1/dating/internal/profile/not-a-uuid/preview", "", OpProfilePreview},
	{"first-message", http.MethodPost, "/v1/dating/internal/matches/not-a-uuid/first-message", `{"actor_id":"x"}`, OpMatchFirstMessage},
	{"risk", http.MethodGet, "/v1/dating/internal/risk/not-a-uuid", "", OpRiskRead},
}

func TestInternalRoutes_LegacyKey(t *testing.T) {
	r := newAuthRouter(t, testInternalKey, nil)
	for _, tc := range internalCases {
		t.Run(tc.name, func(t *testing.T) {
			// A gateway-proxied user request carries the injected key AND an
			// identity: refused, admin or not.
			for _, scopes := range []string{"", "admin"} {
				w := serve(r, tc.method, tc.path, tc.body, gatewayUser(uuid.New(), scopes))
				if w.Code != http.StatusForbidden || errorCode(t, w) != CodeUserCallerRefused {
					t.Fatalf("user scopes=%q: status=%d code=%q, want 403 %s", scopes, w.Code, errorCode(t, w), CodeUserCallerRefused)
				}
			}
			// Scopes alone (no user id) still marks a gateway request.
			w := serve(r, tc.method, tc.path, tc.body, map[string]string{headerInternalKey: testInternalKey, headerScopes: "admin"})
			if w.Code != http.StatusForbidden {
				t.Fatalf("scopes-only: status=%d, want 403", w.Code)
			}
			// No credential / wrong credential → 401.
			for _, hdr := range []map[string]string{
				{},
				{headerInternalKey: "wrong"},
				{"X-Internal-Key": testInternalKey},
			} {
				w := serve(r, tc.method, tc.path, tc.body, hdr)
				if w.Code != http.StatusUnauthorized || errorCode(t, w) != CodeServiceCredentialRequired {
					t.Fatalf("headers=%v: status=%d code=%q, want 401 %s", hdr, w.Code, errorCode(t, w), CodeServiceCredentialRequired)
				}
			}
			// Service call, key, no identity → admitted (handler's 400).
			w = serve(r, tc.method, tc.path, tc.body, map[string]string{headerInternalKey: testInternalKey})
			if w.Code != http.StatusBadRequest {
				t.Fatalf("service call: status=%d body=%s, want 400 from the handler", w.Code, w.Body.String())
			}
		})
	}
}

// With no internal key configured (local/dev only) the internal family and
// the moderation scan admit nobody without a service token.
func TestInternalRoutes_FailClosedWithoutKey(t *testing.T) {
	r := newAuthRouter(t, "", nil)
	for _, tc := range internalCases {
		w := serve(r, tc.method, tc.path, tc.body, map[string]string{headerInternalKey: ""})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status=%d, want 401", tc.name, w.Code)
		}
	}
	w := serve(r, http.MethodPost, "/v1/dating/moderation/scan", `{}`, nil)
	if w.Code != http.StatusUnauthorized || errorCode(t, w) != CodeServiceCredentialRequired {
		t.Fatalf("scan without key: status=%d code=%q, want 401 (was fail-open)", w.Code, errorCode(t, w))
	}
}

func TestModerationScan_ServiceCallerOnly(t *testing.T) {
	r := newAuthRouter(t, testInternalKey, nil)
	w := serve(r, http.MethodPost, "/v1/dating/moderation/scan", `{}`, gatewayUser(uuid.New(), ""))
	if w.Code != http.StatusForbidden || errorCode(t, w) != CodeUserCallerRefused {
		t.Fatalf("user: status=%d code=%q, want 403", w.Code, errorCode(t, w))
	}
	w = serve(r, http.MethodPost, "/v1/dating/moderation/scan", `{}`, map[string]string{headerInternalKey: testInternalKey})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("service call: status=%d body=%s, want 400 (admitted, invalid message_id)", w.Code, w.Body.String())
	}
}

func mustCallerVerifier(t *testing.T, ops string) (*servicetoken.Verifier, *servicetoken.Signer) {
	t.Helper()
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"SERVICE_CALLERS":                            "notification-service",
		"SERVICE_CALLER_NOTIFICATION_SERVICE_KID":    "n1",
		"SERVICE_CALLER_NOTIFICATION_SERVICE_PUBKEY": pub,
		"SERVICE_CALLER_NOTIFICATION_SERVICE_OPS":    ops,
	}
	v, err := ServiceCallersFromEnv(func(k string) string { return env[k] })
	if err != nil || v == nil {
		t.Fatalf("ServiceCallersFromEnv: v=%v err=%v", v, err)
	}
	s, err := servicetoken.NewSignerFromBase64("notification-service", "n1", priv)
	if err != nil {
		t.Fatal(err)
	}
	return v, s
}

func TestInternalRoutes_ServiceToken(t *testing.T) {
	v, signer := mustCallerVerifier(t, OpProfilePreview)
	r := newAuthRouter(t, testInternalKey, v)
	preview := "/v1/dating/internal/profile/not-a-uuid/preview"

	tok, err := signer.Mint(AudienceDating, "", []string{OpProfilePreview}, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// Valid token, no key, no identity → admitted.
	w := serve(r, http.MethodGet, preview, "", map[string]string{ServiceAuthHeader: "Bearer " + tok})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("token: status=%d body=%s, want 400 from the handler", w.Code, w.Body.String())
	}
	// Token plus a gateway identity → still refused.
	hdr := gatewayUser(uuid.New(), "admin")
	hdr[ServiceAuthHeader] = tok
	if w := serve(r, http.MethodGet, preview, "", hdr); w.Code != http.StatusForbidden || errorCode(t, w) != CodeUserCallerRefused {
		t.Fatalf("token+user: status=%d code=%q, want 403 %s", w.Code, errorCode(t, w), CodeUserCallerRefused)
	}
	// Token for another operation → rejected, and never downgraded to the
	// internal key that also rides along.
	w = serve(r, http.MethodGet, "/v1/dating/internal/risk/not-a-uuid", "", map[string]string{
		ServiceAuthHeader: tok, headerInternalKey: testInternalKey,
	})
	if w.Code != http.StatusForbidden || errorCode(t, w) != CodeServiceTokenRejected {
		t.Fatalf("wrong op: status=%d code=%q, want 403 %s", w.Code, errorCode(t, w), CodeServiceTokenRejected)
	}
	// Token minted for payments' audience → rejected.
	other, _ := signer.Mint(servicetoken.AudiencePayments, "", []string{OpProfilePreview}, nil, time.Minute)
	if w := serve(r, http.MethodGet, preview, "", map[string]string{ServiceAuthHeader: other}); w.Code != http.StatusForbidden {
		t.Fatalf("wrong audience: status=%d, want 403", w.Code)
	}
	// Garbage token → rejected.
	if w := serve(r, http.MethodGet, preview, "", map[string]string{ServiceAuthHeader: "a.b.c"}); w.Code != http.StatusForbidden {
		t.Fatalf("garbage token: status=%d, want 403", w.Code)
	}
	// A deployment with no registered callers does not accept tokens.
	bare := newAuthRouter(t, testInternalKey, nil)
	if w := serve(bare, http.MethodGet, preview, "", map[string]string{ServiceAuthHeader: tok}); w.Code != http.StatusUnauthorized {
		t.Fatalf("no verifier: status=%d, want 401", w.Code)
	}
}

func TestOldPaths_GoneOrLegacyPreview(t *testing.T) {
	r := newAuthRouter(t, testInternalKey, nil)
	id := uuid.NewString()

	for _, tc := range []struct{ method, path, body, movedTo string }{
		{http.MethodPost, "/v1/dating/matches/" + id + "/first-message", `{}`, InternalFirstMessagePath},
		{http.MethodGet, "/v1/dating/risk/" + id, "", InternalRiskPath},
	} {
		for _, hdr := range []map[string]string{
			{headerInternalKey: testInternalKey},
			gatewayUser(uuid.New(), ""),
		} {
			w := serve(r, tc.method, tc.path, tc.body, hdr)
			if w.Code != http.StatusGone || errorCode(t, w) != CodeEndpointMoved || !strings.Contains(w.Body.String(), tc.movedTo) {
				t.Fatalf("%s %s: status=%d body=%s, want 410 pointing at %s", tc.method, tc.path, w.Code, w.Body.String(), tc.movedTo)
			}
		}
	}

	// Legacy preview: a gateway user gets 410 …
	w := serve(r, http.MethodGet, "/v1/dating/profile/"+id+"/preview", "", gatewayUser(uuid.New(), ""))
	if w.Code != http.StatusGone || !strings.Contains(w.Body.String(), InternalProfilePreviewPath) {
		t.Fatalf("legacy preview as user: status=%d body=%s, want 410", w.Code, w.Body.String())
	}
	// … notification-service's key-only call is still served (400 here is
	// the handler rejecting the id, i.e. admitted) …
	w = serve(r, http.MethodGet, "/v1/dating/profile/not-a-uuid/preview", "", map[string]string{headerInternalKey: testInternalKey})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("legacy preview as service: status=%d body=%s, want 400 from the handler", w.Code, w.Body.String())
	}
	// … and without the key it is refused.
	if w := serve(r, http.MethodGet, "/v1/dating/profile/"+id+"/preview", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("legacy preview without key: status=%d, want 401", w.Code)
	}
}

func TestResolveInternalKey(t *testing.T) {
	envOf := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	for _, env := range []string{"prod", "production", "staging", "", "  "} {
		_, _, err := ResolveInternalKey(envOf(map[string]string{"ENV": env}))
		if !errors.Is(err, ErrInternalKeyRequired) {
			t.Errorf("ENV=%q without key: err=%v, want ErrInternalKeyRequired (boot refused)", env, err)
		}
	}
	for _, env := range []string{"local", "dev", "development", "DEV"} {
		key, warning, err := ResolveInternalKey(envOf(map[string]string{"ENV": env}))
		if err != nil || key != "" || warning == "" {
			t.Errorf("ENV=%q without key: key=%q warning=%q err=%v, want boot allowed with a warning", env, key, warning, err)
		}
	}
	key, warning, err := ResolveInternalKey(envOf(map[string]string{"ENV": "prod", "INTERNAL_SERVICE_KEY": "k"}))
	if err != nil || key != "k" || warning != "" {
		t.Errorf("prod with key: key=%q warning=%q err=%v", key, warning, err)
	}
}

func TestServiceCallersFromEnv_RefusesIncompleteCallers(t *testing.T) {
	pub, _, _ := servicetoken.GenerateKeypair()
	if v, err := ServiceCallersFromEnv(func(string) string { return "" }); v != nil || err != nil {
		t.Fatalf("blank SERVICE_CALLERS: v=%v err=%v, want nil,nil", v, err)
	}
	for name, env := range map[string]map[string]string{
		"no ops": {"SERVICE_CALLERS": "chat-service", "SERVICE_CALLER_CHAT_SERVICE_KID": "c1", "SERVICE_CALLER_CHAT_SERVICE_PUBKEY": pub},
		"no key": {"SERVICE_CALLERS": "chat-service", "SERVICE_CALLER_CHAT_SERVICE_KID": "c1", "SERVICE_CALLER_CHAT_SERVICE_OPS": OpMatchFirstMessage},
		"no kid": {"SERVICE_CALLERS": "chat-service", "SERVICE_CALLER_CHAT_SERVICE_PUBKEY": pub, "SERVICE_CALLER_CHAT_SERVICE_OPS": OpMatchFirstMessage},
	} {
		env := env
		if _, err := ServiceCallersFromEnv(func(k string) string { return env[k] }); err == nil {
			t.Errorf("%s: want a configuration error", name)
		}
	}
}

// TestResolveIdentityProfileURL_BootRule: outside local/dev the service
// refuses to boot without IDENTITY_PROFILE_SERVICE_URL; local/dev boots with a
// warning (interim client rule); a malformed URL is refused everywhere.
func TestResolveIdentityProfileURL_BootRule(t *testing.T) {
	envOf := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for _, env := range []string{"", "prod", "production", "staging", "qa"} {
		_, _, err := ResolveIdentityProfileURL(envOf(map[string]string{"ENV": env}))
		if !errors.Is(err, ErrIdentityProfileURLRequired) {
			t.Errorf("ENV=%q without URL: err=%v, want ErrIdentityProfileURLRequired (boot refused)", env, err)
		}
	}
	for _, env := range []string{"local", "dev", "development", "DEV"} {
		u, warning, err := ResolveIdentityProfileURL(envOf(map[string]string{"ENV": env}))
		if err != nil || u != "" || warning == "" {
			t.Errorf("ENV=%q without URL: url=%q warning=%q err=%v, want boot allowed with a warning", env, u, warning, err)
		}
	}
	u, warning, err := ResolveIdentityProfileURL(envOf(map[string]string{
		"ENV": "prod", "IDENTITY_PROFILE_SERVICE_URL": " http://identity-profile-service.atpost.svc.cluster.local:8098/ ",
	}))
	if err != nil || warning != "" || u != "http://identity-profile-service.atpost.svc.cluster.local:8098" {
		t.Errorf("prod with URL: url=%q warning=%q err=%v", u, warning, err)
	}
	for _, bad := range []string{"identity-profile:8098", "ftp://identity-profile:8098", "http://", "/internal"} {
		for _, env := range []string{"dev", "prod"} {
			if _, _, err := ResolveIdentityProfileURL(envOf(map[string]string{"ENV": env, "IDENTITY_PROFILE_SERVICE_URL": bad})); err == nil {
				t.Errorf("ENV=%s URL=%q: want a configuration error", env, bad)
			}
		}
	}
}
