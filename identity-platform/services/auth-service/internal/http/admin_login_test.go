package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"context"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/atpost/identity-auth-service/pkg/accesstoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ── stub ────────────────────────────────────────────────────────────────────

type adminSessionStub struct {
	loginFn   func(identifier, password string) (*service.AdminLoginChallenge, error)
	verifyFn  func(pending, code string) (*service.AuthResponse, error)
	refreshFn func(token string) (*service.AuthResponse, error)
	logoutFn  func(token string) error
	stepUpFn  func(userID, sessionID uuid.UUID, code string) (*service.StepUpResponse, error)
	statusFn  func(userID, sessionID uuid.UUID) (*service.AdminSessionStatus, error)
	calls     []string
}

func (s *stubAuthService) AdminLogin(_ context.Context, identifier, password, _, _ string) (*service.AdminLoginChallenge, error) {
	s.admin.calls = append(s.admin.calls, "login")
	if s.admin.loginFn == nil {
		return &service.AdminLoginChallenge{Requires2FA: true, PendingToken: "pending"}, nil
	}
	return s.admin.loginFn(identifier, password)
}

func (s *stubAuthService) AdminVerify2FA(_ context.Context, pending, code string) (*service.AuthResponse, error) {
	s.admin.calls = append(s.admin.calls, "verify")
	return s.admin.verifyFn(pending, code)
}

func (s *stubAuthService) AdminRefreshSession(_ context.Context, token, _, _ string) (*service.AuthResponse, error) {
	s.admin.calls = append(s.admin.calls, "refresh:"+token)
	return s.admin.refreshFn(token)
}

func (s *stubAuthService) AdminLogout(_ context.Context, token string) error {
	s.admin.calls = append(s.admin.calls, "logout:"+token)
	if s.admin.logoutFn == nil {
		return nil
	}
	return s.admin.logoutFn(token)
}

func (s *stubAuthService) AdminStepUp(_ context.Context, userID, sessionID uuid.UUID, code string) (*service.StepUpResponse, error) {
	s.admin.calls = append(s.admin.calls, "stepup")
	return s.admin.stepUpFn(userID, sessionID, code)
}

func (s *stubAuthService) AdminSessionStatusFor(_ context.Context, userID, sessionID uuid.UUID) (*service.AdminSessionStatus, error) {
	s.admin.calls = append(s.admin.calls, "status")
	if s.admin.statusFn == nil {
		return &service.AdminSessionStatus{UserID: userID.String(), AdminMFA: true}, nil
	}
	return s.admin.statusFn(userID, sessionID)
}

// ── harness ─────────────────────────────────────────────────────────────────

// adminRouter mounts the consumer AND admin routes behind their real
// middlewares. COOKIE_DOMAIN is set to a parent domain on purpose: the admin
// cookies must ignore it.
func adminRouter(t *testing.T, svc *stubAuthService, secure bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	cfg := &config.Config{
		InternalServiceKey: permsInternalKey,
		CookieDomain:       ".cleestudio.com",
		AdminCookieSecure:  secure,
		AdminSessionTTL:    8 * time.Hour,
	}
	h := New(svc, cfg, nil, nil)
	keys := JWTKeySet{ActiveSecret: a2Secret}
	h.RegisterRoutes(r, AuthMiddlewareWithKeys(keys, nil), RequireCSRFMiddleware())
	h.RegisterAdminSessionRoutes(r, AdminAuthMiddlewareWithKeys(keys, nil))
	return r
}

type reqOpt func(*http.Request)

func withCookie(name, value string) reqOpt {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: name, Value: value}) }
}

func withHeader(name, value string) reqOpt {
	return func(r *http.Request) { r.Header.Set(name, value) }
}

func adminDo(r *gin.Engine, method, path, body string, opts ...reqOpt) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, o := range opts {
		o(req)
	}
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	return resp
}

func setCookies(resp *httptest.ResponseRecorder) map[string]*http.Cookie {
	out := map[string]*http.Cookie{}
	for _, c := range (&http.Response{Header: resp.Header()}).Cookies() {
		out[c.Name] = c
	}
	return out
}

func adminSessionToken(t *testing.T, user, sid uuid.UUID, kind string) string {
	t.Helper()
	return a2Token(t, user, sid, accesstoken.Session{AMR: []string{"pwd", "otp"}, AdminMFA: true, Kind: kind})
}

func verifiedAdmin(user uuid.UUID) *service.AuthResponse {
	email := "admin@example.test"
	return &service.AuthResponse{
		Tokens: service.TokenPair{
			AccessToken:  "admin-access-value",
			RefreshToken: "admin-refresh-value",
			ExpiresAt:    time.Now().Add(15 * time.Minute),
		},
		User:      &store.User{ID: user, Email: &email},
		SessionID: uuid.New(),
	}
}

// assertAdminCookieSet checks the complete admin cookie set on a response:
// exactly the three admin cookies, host-only, SameSite=Strict, Path=/, the two
// tokens HttpOnly and the CSRF cookie readable — and no consumer cookie.
func assertAdminCookieSet(t *testing.T, resp *httptest.ResponseRecorder, secure bool) map[string]*http.Cookie {
	t.Helper()
	got := setCookies(resp)
	for _, consumer := range []string{accessTokenCookieName, refreshTokenCookieName, csrfCookieName} {
		if _, ok := got[consumer]; ok {
			t.Fatalf("admin route set the consumer cookie %q", consumer)
		}
	}
	if len(got) != 3 {
		t.Fatalf("want exactly 3 admin cookies, got %d: %v", len(got), resp.Header().Values("Set-Cookie"))
	}
	for _, raw := range resp.Header().Values("Set-Cookie") {
		if strings.Contains(strings.ToLower(raw), "domain=") {
			t.Fatalf("admin cookie carries a Domain attribute (must be host-only): %s", raw)
		}
	}
	for name, httpOnly := range map[string]bool{adminAccessCookieName: true, adminRefreshCookieName: true, adminCSRFCookieName: false} {
		c, ok := got[name]
		if !ok {
			t.Fatalf("missing admin cookie %q", name)
		}
		if c.Domain != "" {
			t.Fatalf("%s: Domain=%q, want host-only", name, c.Domain)
		}
		if c.SameSite != http.SameSiteStrictMode {
			t.Fatalf("%s: SameSite=%v, want Strict", name, c.SameSite)
		}
		if c.Path != "/" {
			t.Fatalf("%s: Path=%q, want /", name, c.Path)
		}
		if c.HttpOnly != httpOnly {
			t.Fatalf("%s: HttpOnly=%v, want %v", name, c.HttpOnly, httpOnly)
		}
		if c.Secure != secure {
			t.Fatalf("%s: Secure=%v, want %v", name, c.Secure, secure)
		}
	}
	return got
}

// ── login ───────────────────────────────────────────────────────────────────

func TestAdminSessionLoginReturnsPendingTokenAndNoCookies(t *testing.T) {
	svc := &stubAuthService{}
	r := adminRouter(t, svc, true)
	resp := adminDo(r, http.MethodPost, "/v1/auth/admin-session/login", `{"identifier":"a@example.test","password":"pw"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d: %s", resp.Code, resp.Body.String())
	}
	if n := len(resp.Header().Values("Set-Cookie")); n != 0 {
		t.Fatalf("password step set %d cookies; a password alone must never create a session", n)
	}
	if !strings.Contains(resp.Body.String(), `"requires_2fa":true`) {
		t.Fatalf("body: %s", resp.Body.String())
	}
}

func TestAdminSessionLoginStableRefusals(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
		code   string
	}{
		"wrong password": {service.ErrAdminInvalidCredentials, http.StatusUnauthorized, "AUTH_FAILED"},
		"not an admin":   {service.ErrNotAdmin, http.StatusForbidden, CodeNotAdmin},
		"no totp":        {service.ErrTOTPNotEnrolled, http.StatusForbidden, CodeMFANotEnrolled},
		"inactive":       {service.ErrAdminAccountInactive, http.StatusForbidden, CodeAccountNotActive},
		"perms down":     {service.ErrAdminPermissionsUnavailable, http.StatusServiceUnavailable, CodePermissionsUnavailable},
		"throttled":      {&service.ErrThrottled{RetryAfter: time.Minute}, http.StatusTooManyRequests, "RATE_LIMITED"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			svc := &stubAuthService{admin: adminSessionStub{loginFn: func(string, string) (*service.AdminLoginChallenge, error) {
				return nil, tc.err
			}}}
			resp := adminDo(adminRouter(t, svc, true), http.MethodPost, "/v1/auth/admin-session/login", `{"identifier":"a","password":"b"}`)
			if resp.Code != tc.status || errCode(t, resp) != tc.code {
				t.Fatalf("got %d %s, want %d %s", resp.Code, errCode(t, resp), tc.status, tc.code)
			}
			if n := len(resp.Header().Values("Set-Cookie")); n != 0 {
				t.Fatalf("refusal set %d cookies", n)
			}
		})
	}
}

// ── verify-2fa ──────────────────────────────────────────────────────────────

func TestAdminSessionVerifySetsOnlyHostOnlyStrictAdminCookies(t *testing.T) {
	for _, secure := range []bool{true, false} {
		user := uuid.New()
		svc := &stubAuthService{admin: adminSessionStub{verifyFn: func(pending, code string) (*service.AuthResponse, error) {
			if pending != "pending-token" || code != "123456" {
				t.Fatalf("service got pending=%q code=%q", pending, code)
			}
			return verifiedAdmin(user), nil
		}}}
		resp := adminDo(adminRouter(t, svc, secure), http.MethodPost, "/v1/auth/admin-session/verify-2fa",
			`{"pending_token":"pending-token","code":"123456"}`)
		if resp.Code != http.StatusOK {
			t.Fatalf("status %d: %s", resp.Code, resp.Body.String())
		}
		got := assertAdminCookieSet(t, resp, secure)
		if got[adminAccessCookieName].Value != "admin-access-value" || got[adminRefreshCookieName].Value != "admin-refresh-value" {
			t.Fatal("admin cookies do not carry the minted tokens")
		}
		if got[adminCSRFCookieName].Value == "" {
			t.Fatal("empty admin csrf cookie")
		}
		body := resp.Body.String()
		if strings.Contains(body, "admin-access-value") || strings.Contains(body, "admin-refresh-value") {
			t.Fatal("tokens leaked into the response body; they must exist only as HttpOnly cookies")
		}
		if !strings.Contains(body, `"admin_mfa":true`) {
			t.Fatalf("body lacks admin_mfa: %s", body)
		}
	}
}

func TestAdminSessionVerifyRefusalsSetNoCookies(t *testing.T) {
	for err, code := range map[error]string{
		service.ErrInvalidOTP:          CodeInvalidOTP,
		service.ErrTOTPReplay:          CodeOTPReplayed,
		service.ErrAdminPendingInvalid: CodeAdminSignInExpired,
		service.ErrNotAdmin:            CodeNotAdmin,
	} {
		svc := &stubAuthService{admin: adminSessionStub{verifyFn: func(string, string) (*service.AuthResponse, error) { return nil, err }}}
		resp := adminDo(adminRouter(t, svc, true), http.MethodPost, "/v1/auth/admin-session/verify-2fa", `{"pending_token":"p","code":"000000"}`)
		if errCode(t, resp) != code || len(resp.Header().Values("Set-Cookie")) != 0 {
			t.Fatalf("%v: got %d %s with %d cookies", err, resp.Code, errCode(t, resp), len(resp.Header().Values("Set-Cookie")))
		}
	}
}

// ── refresh ─────────────────────────────────────────────────────────────────

// The consumer refresh cookie (and a body token) never reach the admin
// refresh: only admin_refresh_token is read.
func TestAdminSessionRefreshIgnoresConsumerCredentials(t *testing.T) {
	svc := &stubAuthService{admin: adminSessionStub{refreshFn: func(string) (*service.AuthResponse, error) {
		t.Fatal("service must not be called without an admin refresh cookie")
		return nil, nil
	}}}
	r := adminRouter(t, svc, true)
	resp := adminDo(r, http.MethodPost, "/v1/auth/admin-session/refresh", `{"refresh_token":"consumer-refresh"}`,
		withCookie(refreshTokenCookieName, "consumer-refresh"),
		withCookie(csrfCookieName, "c"), withCookie(adminCSRFCookieName, "c"), withHeader("X-CSRF-Token", "c"))
	if resp.Code != http.StatusUnauthorized || errCode(t, resp) != "AUTH_FAILED" {
		t.Fatalf("got %d %s", resp.Code, errCode(t, resp))
	}
	for _, c := range setCookies(resp) {
		if !strings.HasPrefix(c.Name, "admin_") {
			t.Fatalf("admin refresh touched the consumer cookie %q", c.Name)
		}
	}
}

func TestAdminSessionRefreshRequiresAdminCSRF(t *testing.T) {
	svc := &stubAuthService{admin: adminSessionStub{refreshFn: func(string) (*service.AuthResponse, error) {
		t.Fatal("service must not be called without the admin CSRF pair")
		return nil, nil
	}}}
	r := adminRouter(t, svc, true)
	// The CONSUMER csrf pair does not satisfy the admin check.
	resp := adminDo(r, http.MethodPost, "/v1/auth/admin-session/refresh", "",
		withCookie(adminRefreshCookieName, "admin-refresh"),
		withCookie(csrfCookieName, "c"), withHeader("X-CSRF-Token", "c"))
	if resp.Code != http.StatusForbidden || errCode(t, resp) != "CSRF_FAILED" {
		t.Fatalf("got %d %s", resp.Code, errCode(t, resp))
	}
}

func TestAdminSessionRefreshRotatesAdminCookies(t *testing.T) {
	user := uuid.New()
	var seen string
	svc := &stubAuthService{admin: adminSessionStub{refreshFn: func(token string) (*service.AuthResponse, error) {
		seen = token
		return verifiedAdmin(user), nil
	}}}
	resp := adminDo(adminRouter(t, svc, true), http.MethodPost, "/v1/auth/admin-session/refresh", "",
		withCookie(adminRefreshCookieName, "admin-refresh"),
		withCookie(refreshTokenCookieName, "consumer-refresh"),
		withCookie(adminCSRFCookieName, "x"), withHeader("X-CSRF-Token", "x"))
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d: %s", resp.Code, resp.Body.String())
	}
	if seen != "admin-refresh" {
		t.Fatalf("service got %q, want the admin refresh cookie", seen)
	}
	assertAdminCookieSet(t, resp, true)
}

func TestAdminSessionRefreshRefusalClearsOnlyAdminCookies(t *testing.T) {
	for err, code := range map[error]string{
		service.ErrWrongSessionKind: "AUTH_FAILED",
		service.ErrAdminAccessLost:  CodeAdminAccessEnded,
	} {
		svc := &stubAuthService{admin: adminSessionStub{refreshFn: func(string) (*service.AuthResponse, error) { return nil, err }}}
		resp := adminDo(adminRouter(t, svc, true), http.MethodPost, "/v1/auth/admin-session/refresh", "",
			withCookie(adminRefreshCookieName, "r"), withCookie(adminCSRFCookieName, "x"), withHeader("X-CSRF-Token", "x"))
		if resp.Code != http.StatusUnauthorized || errCode(t, resp) != code {
			t.Fatalf("%v: got %d %s", err, resp.Code, errCode(t, resp))
		}
		got := setCookies(resp)
		if len(got) != 3 {
			t.Fatalf("%v: want the 3 admin cookies cleared, got %v", err, resp.Header().Values("Set-Cookie"))
		}
		for name, c := range got {
			if !strings.HasPrefix(name, "admin_") || c.MaxAge >= 0 || c.Domain != "" || c.SameSite != http.SameSiteStrictMode {
				t.Fatalf("%v: bad clearing cookie %q: %+v", err, name, c)
			}
		}
	}
}

// ── logout ──────────────────────────────────────────────────────────────────

func TestAdminSessionLogoutUsesOnlyAdminRefreshCookie(t *testing.T) {
	svc := &stubAuthService{}
	resp := adminDo(adminRouter(t, svc, true), http.MethodPost, "/v1/auth/admin-session/logout", "",
		withCookie(refreshTokenCookieName, "consumer-refresh"), withCookie(adminRefreshCookieName, "admin-refresh"))
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d", resp.Code)
	}
	if len(svc.admin.calls) != 1 || svc.admin.calls[0] != "logout:admin-refresh" {
		t.Fatalf("calls %v", svc.admin.calls)
	}
	got := setCookies(resp)
	if len(got) != 3 {
		t.Fatalf("want 3 cleared admin cookies, got %v", resp.Header().Values("Set-Cookie"))
	}
	for name := range got {
		if !strings.HasPrefix(name, "admin_") {
			t.Fatalf("admin logout cleared the consumer cookie %q", name)
		}
	}

	// With only a consumer cookie the service sees no token at all.
	svc2 := &stubAuthService{}
	adminDo(adminRouter(t, svc2, true), http.MethodPost, "/v1/auth/admin-session/logout", "",
		withCookie(refreshTokenCookieName, "consumer-refresh"))
	if len(svc2.admin.calls) != 1 || svc2.admin.calls[0] != "logout:" {
		t.Fatalf("consumer refresh cookie reached the admin logout: %v", svc2.admin.calls)
	}
}

// Consumer logout never clears or reads the admin cookies.
func TestConsumerLogoutLeavesAdminCookiesAlone(t *testing.T) {
	var seen string
	svc := &stubAuthService{logoutFn: func(token string) error { seen = token; return nil }}
	resp := adminDo(adminRouter(t, svc, true), http.MethodPost, "/v1/auth/logout", "",
		withCookie(adminRefreshCookieName, "admin-refresh"))
	if resp.Code != http.StatusOK || seen != "" {
		t.Fatalf("status %d, consumer logout saw %q", resp.Code, seen)
	}
	for name := range setCookies(resp) {
		if strings.HasPrefix(name, "admin_") {
			t.Fatalf("consumer logout touched %q", name)
		}
	}
}

// ── admin auth middleware ───────────────────────────────────────────────────

func TestAdminSessionMiddlewareAcceptsOnlyAdminCookieWithAdminKind(t *testing.T) {
	user, sid := uuid.New(), uuid.New()
	adminTok := adminSessionToken(t, user, sid, accesstoken.SessionKindAdmin)
	consumerTok := adminSessionToken(t, user, sid, "") // admin_mfa=true, but a consumer session

	cases := []struct {
		name   string
		opts   []reqOpt
		status int
		code   string
	}{
		{"admin cookie, admin kind", []reqOpt{withCookie(adminAccessCookieName, adminTok)}, http.StatusOK, ""},
		{"consumer token in admin cookie", []reqOpt{withCookie(adminAccessCookieName, consumerTok)}, http.StatusUnauthorized, CodeWrongSession},
		{"admin token as bearer", []reqOpt{withHeader("Authorization", "Bearer "+adminTok)}, http.StatusUnauthorized, "UNAUTHORIZED"},
		{"admin token in consumer cookie", []reqOpt{withCookie(accessTokenCookieName, adminTok)}, http.StatusUnauthorized, "UNAUTHORIZED"},
		{"consumer session cookie", []reqOpt{withCookie(accessTokenCookieName, consumerTok)}, http.StatusUnauthorized, "UNAUTHORIZED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubAuthService{}
			resp := adminDo(adminRouter(t, svc, true), http.MethodGet, "/v1/auth/admin-session", "", tc.opts...)
			if resp.Code != tc.status || (tc.code != "" && errCode(t, resp) != tc.code) {
				t.Fatalf("got %d %s, want %d %s", resp.Code, errCode(t, resp), tc.status, tc.code)
			}
		})
	}
}

func TestAdminSessionStepUpNeedsAdminCSRFAndSetsOnlyAdminAccessCookie(t *testing.T) {
	user, sid := uuid.New(), uuid.New()
	adminTok := adminSessionToken(t, user, sid, accesstoken.SessionKindAdmin)
	now := time.Now()
	svc := &stubAuthService{admin: adminSessionStub{stepUpFn: func(u, s uuid.UUID, code string) (*service.StepUpResponse, error) {
		if u != user || s != sid || code != "654321" {
			t.Fatalf("service got %v %v %q", u, s, code)
		}
		return &service.StepUpResponse{AccessToken: "stepped-up", ExpiresAt: now.Add(15 * time.Minute), StepUpAt: now,
			StepUpValidUntil: now.Add(5 * time.Minute), AdminMFA: true}, nil
	}}}
	r := adminRouter(t, svc, true)

	noCSRF := adminDo(r, http.MethodPost, "/v1/auth/admin-session/step-up", `{"otp":"654321"}`,
		withCookie(adminAccessCookieName, adminTok))
	if noCSRF.Code != http.StatusForbidden || errCode(t, noCSRF) != "CSRF_FAILED" {
		t.Fatalf("without CSRF: %d %s", noCSRF.Code, errCode(t, noCSRF))
	}

	resp := adminDo(r, http.MethodPost, "/v1/auth/admin-session/step-up", `{"otp":"654321"}`,
		withCookie(adminAccessCookieName, adminTok), withCookie(adminCSRFCookieName, "k"), withHeader("X-CSRF-Token", "k"))
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d: %s", resp.Code, resp.Body.String())
	}
	got := setCookies(resp)
	c, ok := got[adminAccessCookieName]
	if len(got) != 1 || !ok || c.Value != "stepped-up" || c.Domain != "" || c.SameSite != http.SameSiteStrictMode || !c.HttpOnly {
		t.Fatalf("step-up cookies: %v", resp.Header().Values("Set-Cookie"))
	}
	var body map[string]any
	_ = json.Unmarshal(resp.Body.Bytes(), &body)
	if strings.Contains(resp.Body.String(), "stepped-up") {
		t.Fatal("step-up token leaked into the body")
	}
}

// An admin session cookie does not authenticate the consumer routes.
func TestAdminCookieDoesNotAuthenticateConsumerRoutes(t *testing.T) {
	user, sid := uuid.New(), uuid.New()
	adminTok := adminSessionToken(t, user, sid, accesstoken.SessionKindAdmin)
	svc := &stubAuthService{}
	resp := adminDo(adminRouter(t, svc, true), http.MethodPost, "/v1/auth/step-up", `{"otp":"123456"}`,
		withCookie(adminAccessCookieName, adminTok), withCookie(adminCSRFCookieName, "k"), withHeader("X-CSRF-Token", "k"))
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("consumer step-up accepted the admin cookie: %d", resp.Code)
	}
}
