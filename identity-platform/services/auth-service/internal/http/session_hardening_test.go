package http

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/gin-gonic/gin"
)

func serve(r http.Handler, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ── /logout (L8 / M3) ──────────────────────────────────────────────────────

func logoutRouter(t *testing.T) (*gin.Engine, *[]string) {
	t.Helper()
	var revoked []string
	stub := &stubAuthService{logoutFn: func(rt string) error {
		revoked = append(revoked, rt)
		return nil
	}}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(stub, &config.Config{}, nil, nil)
	r.POST("/v1/auth/logout", h.Logout)
	return r, &revoked
}

func TestLogoutRevokesRefreshTokenFromJSONBody(t *testing.T) {
	r, revoked := logoutRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", strings.NewReader(`{"refresh_token":"body-token"}`))
	req.Header.Set("Content-Type", "application/json")
	if w := serve(r, req); w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if len(*revoked) != 1 || (*revoked)[0] != "body-token" {
		t.Fatalf("body refresh token not revoked: %v", *revoked)
	}
}

func TestLogoutRevokesCookieAndDistinctBodyToken(t *testing.T) {
	r, revoked := logoutRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", strings.NewReader(`{"refresh_token":"body-token"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: refreshTokenCookieName, Value: "cookie-token"})
	serve(r, req)
	if strings.Join(*revoked, ",") != "cookie-token,body-token" {
		t.Fatalf("revoked %v, want cookie then body", *revoked)
	}
}

func TestLogoutWithNoTokenIsStillOK(t *testing.T) {
	r, revoked := logoutRouter(t)
	if w := serve(r, httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)); w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if len(*revoked) != 0 {
		t.Fatalf("revoked %v with no token", *revoked)
	}
}

// ── body limit and server timeouts (M8) ────────────────────────────────────

func bodyRouter(read *int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(BodyLimitMiddleware(1024))
	r.POST("/x", func(c *gin.Context) {
		b, err := io.ReadAll(c.Request.Body)
		*read = len(b)
		if err != nil {
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		c.Status(http.StatusOK)
	})
	return r
}

func TestBodyLimitRefusesDeclaredOversizeBody(t *testing.T) {
	read := -1
	r := bodyRouter(&read)
	w := serve(r, httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(make([]byte, 4096))))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413", w.Code)
	}
	if read != -1 {
		t.Fatal("handler ran on an oversize body")
	}
}

func TestBodyLimitCapsChunkedBody(t *testing.T) {
	read := -1
	r := bodyRouter(&read)
	req := httptest.NewRequest(http.MethodPost, "/x", io.MultiReader(bytes.NewReader(make([]byte, 4096))))
	req.ContentLength = -1 // chunked: no declared length
	w := serve(r, req)
	if w.Code != http.StatusRequestEntityTooLarge || read > 1024 {
		t.Fatalf("chunked body not capped: status %d, read %d", w.Code, read)
	}
}

func TestBodyLimitPassesSmallBody(t *testing.T) {
	read := -1
	r := bodyRouter(&read)
	if w := serve(r, httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"a":1}`))); w.Code != http.StatusOK {
		t.Fatalf("small body refused: %d", w.Code)
	}
}

func TestNewServerSetsTimeouts(t *testing.T) {
	s := NewServer(":0", http.NewServeMux())
	if s.ReadHeaderTimeout <= 0 || s.ReadTimeout <= 0 || s.WriteTimeout <= 0 || s.IdleTimeout <= 0 || s.MaxHeaderBytes <= 0 {
		t.Fatalf("server limits not all set: %+v", s)
	}
}

// ── OAuth routes (C1, M7) ──────────────────────────────────────────────────

type oauthStub struct {
	*stubAuthService
	tokenCalls  int
	gotIDToken  string
	gotBinding  string
	callbackErr error
	tokenErr    error
}

func (o *oauthStub) GetOAuthRedirectURL(_ context.Context, _ string) (*service.OAuthStart, error) {
	return &service.OAuthStart{URL: "https://accounts.example/auth?state=s", Binding: "bind-secret"}, nil
}

func (o *oauthStub) HandleOAuthCallback(_ context.Context, _, _, _, binding string) (*service.OAuthCallbackResult, error) {
	o.gotBinding = binding
	if o.callbackErr != nil {
		return nil, o.callbackErr
	}
	return &service.OAuthCallbackResult{Auth: &service.AuthResponse{}}, nil
}

func (o *oauthStub) HandleOAuthToken(_ context.Context, _, idToken string) (*service.OAuthCallbackResult, error) {
	o.tokenCalls++
	o.gotIDToken = idToken
	if o.tokenErr != nil {
		return nil, o.tokenErr
	}
	return &service.OAuthCallbackResult{Auth: &service.AuthResponse{}}, nil
}

func oauthRouter(o *oauthStub) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(o, &config.Config{FrontendURL: "https://app.example", CookieSecure: true}, nil, nil)
	r.GET("/v1/auth/oauth/:provider", h.OAuthRedirect)
	r.GET("/v1/auth/oauth/:provider/callback", h.OAuthCallback)
	r.POST("/v1/auth/oauth/:provider/token", h.OAuthToken)
	return r
}

func TestOAuthTokenRequiresIDToken(t *testing.T) {
	o := &oauthStub{stubAuthService: &stubAuthService{}}
	r := oauthRouter(o)
	for _, body := range []string{`{"token":"ya29.access-token"}`, `{}`} {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/oauth/google/token", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := serve(r, req)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "ID_TOKEN_REQUIRED") {
			t.Fatalf("%s: status %d body %s", body, w.Code, w.Body.String())
		}
	}
	if o.tokenCalls != 0 {
		t.Fatal("a bare access token reached the service")
	}
}

func TestOAuthTokenPassesIDTokenAndMapsRefusals(t *testing.T) {
	cases := []struct {
		err  error
		code int
		name string
	}{
		{nil, http.StatusOK, ""},
		{service.ErrOAuthTokenProviderUnsupported, http.StatusBadRequest, "OAUTH_PROVIDER_UNSUPPORTED"},
		{service.ErrOAuthNotConfigured, http.StatusServiceUnavailable, "OAUTH_UNAVAILABLE"},
		{service.ErrOAuthIDTokenInvalid, http.StatusUnauthorized, "AUTH_FAILED"},
		{service.ErrAccountPendingPurge, http.StatusForbidden, "ACCOUNT_PENDING_PURGE"},
		{service.ErrAccountPurged, http.StatusForbidden, "ACCOUNT_PURGED"},
		{service.ErrEmailNotVerified, http.StatusForbidden, "EMAIL_NOT_VERIFIED"},
	}
	for _, tc := range cases {
		o := &oauthStub{stubAuthService: &stubAuthService{}, tokenErr: tc.err}
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/oauth/google/token", strings.NewReader(`{"id_token":"eyJ.id.token"}`))
		req.Header.Set("Content-Type", "application/json")
		w := serve(oauthRouter(o), req)
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.name) {
			t.Fatalf("err %v: status %d body %s", tc.err, w.Code, w.Body.String())
		}
		if o.gotIDToken != "eyJ.id.token" {
			t.Fatalf("id_token not forwarded: %q", o.gotIDToken)
		}
	}
}

func TestOAuthRedirectSetsBindingCookieAndCallbackForwardsIt(t *testing.T) {
	o := &oauthStub{stubAuthService: &stubAuthService{}}
	r := oauthRouter(o)
	w := serve(r, httptest.NewRequest(http.MethodGet, "/v1/auth/oauth/google", nil))
	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("redirect status %d", w.Code)
	}
	var bind *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == oauthBindingCookieName {
			bind = c
		}
	}
	if bind == nil || bind.Value != "bind-secret" || !bind.HttpOnly || !bind.Secure ||
		bind.SameSite != http.SameSiteLaxMode || bind.Path != oauthBindingCookiePath || bind.MaxAge <= 0 {
		t.Fatalf("binding cookie wrong: %+v", bind)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/oauth/google/callback?code=c&state=s", nil)
	req.AddCookie(&http.Cookie{Name: oauthBindingCookieName, Value: "bind-secret"})
	w = serve(r, req)
	if o.gotBinding != "bind-secret" {
		t.Fatalf("callback did not forward the binding cookie: %q", o.gotBinding)
	}
	cleared := false
	for _, c := range w.Result().Cookies() {
		if c.Name == oauthBindingCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("callback did not clear the binding cookie")
	}
}

func TestOAuthCallbackWithoutBindingIsRefused(t *testing.T) {
	o := &oauthStub{stubAuthService: &stubAuthService{}, callbackErr: service.ErrOAuthStateInvalid}
	w := serve(oauthRouter(o), httptest.NewRequest(http.MethodGet, "/v1/auth/oauth/google/callback?code=c&state=s", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", w.Code)
	}
	if o.gotBinding != "" {
		t.Fatalf("binding %q invented with no cookie", o.gotBinding)
	}
}
