package http

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type stubAuthService struct {
	requestOTPFn       func(phone, purpose string) error
	verifyOTPFn        func(phone, code, purpose, deviceID, platform, ip, userAgent string) (*service.AuthResponse, error)
	registerWithPassFn func(phone, email, password, firstName, lastName, dob, gender string) (*service.AuthResponse, error)
	loginWithPassFn    func(identifier, password, deviceID, platform, ip, userAgent string) (*service.AuthResponse, error)
	refreshSessionFn   func(refreshToken, ip, userAgent string) (*service.AuthResponse, error)
	logoutFn           func(refreshToken string) error
	issueMiniAppFn     func(appID, userID uuid.UUID, grantedPermissions []string) (*service.MiniAppSessionResponse, error)
	miniAppJWKSFn      func() (*service.JSONWebKeySet, error)
}

func (s *stubAuthService) RequestOTP(ctx context.Context, phone, purpose string) error {
	if s.requestOTPFn == nil {
		return nil
	}
	return s.requestOTPFn(phone, purpose)
}

func (s *stubAuthService) VerifyOTP(ctx context.Context, phone, code, purpose, deviceID, platform, ip, userAgent string) (*service.AuthResponse, error) {
	if s.verifyOTPFn == nil {
		return nil, nil
	}
	return s.verifyOTPFn(phone, code, purpose, deviceID, platform, ip, userAgent)
}

func (s *stubAuthService) RegisterWithPassword(ctx context.Context, phone, email, password, firstName, lastName, dob, gender string) (*service.AuthResponse, error) {
	if s.registerWithPassFn == nil {
		return nil, nil
	}
	return s.registerWithPassFn(phone, email, password, firstName, lastName, dob, gender)
}

func (s *stubAuthService) LoginWithPassword(ctx context.Context, identifier, password, deviceID, platform, ip, userAgent string) (*service.AuthResponse, error) {
	if s.loginWithPassFn == nil {
		return nil, nil
	}
	return s.loginWithPassFn(identifier, password, deviceID, platform, ip, userAgent)
}

func (s *stubAuthService) RefreshSession(ctx context.Context, refreshToken, ip, userAgent string) (*service.AuthResponse, error) {
	if s.refreshSessionFn == nil {
		return nil, nil
	}
	return s.refreshSessionFn(refreshToken, ip, userAgent)
}

func (s *stubAuthService) Logout(ctx context.Context, refreshToken string) error {
	if s.logoutFn == nil {
		return nil
	}
	return s.logoutFn(refreshToken)
}

func (s *stubAuthService) LogoutAll(_ context.Context, _ uuid.UUID) (int64, error) { return 0, nil }
func (s *stubAuthService) ListSessions(_ context.Context, _ uuid.UUID) ([]store.Session, error) {
	return nil, nil
}
func (s *stubAuthService) RevokeSessionByID(_ context.Context, _, _ uuid.UUID) error { return nil }
func (s *stubAuthService) DeleteAccount(_ context.Context, _ uuid.UUID) error        { return nil }

// RBAC stubs
func (s *stubAuthService) GrantRole(_ context.Context, _, _ uuid.UUID, _ string) error  { return nil }
func (s *stubAuthService) RevokeRole(_ context.Context, _, _ uuid.UUID, _ string) error { return nil }
func (s *stubAuthService) ListUserRoles(_ context.Context, _, _ uuid.UUID) ([]store.UserRole, error) {
	return nil, nil
}

// 2FA stubs
func (s *stubAuthService) Setup2FA(_ context.Context, _ uuid.UUID) (*service.TwoFASetupResponse, error) {
	return nil, nil
}
func (s *stubAuthService) Verify2FASetup(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (s *stubAuthService) Disable2FA(_ context.Context, _ uuid.UUID, _, _ string) error  { return nil }
func (s *stubAuthService) Verify2FA(_ context.Context, _ uuid.UUID, _, _ string) (*service.AuthResponse, error) {
	return nil, nil
}

// OAuth stubs
func (s *stubAuthService) GetOAuthRedirectURL(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (s *stubAuthService) HandleOAuthCallback(_ context.Context, _, _, _ string) (*service.OAuthCallbackResult, error) {
	return nil, nil
}
func (s *stubAuthService) HandleOAuthToken(_ context.Context, _, _ string) (*service.OAuthCallbackResult, error) {
	return nil, nil
}
func (s *stubAuthService) CompleteOAuthSignup(_ context.Context, _, _ string) error {
	return nil
}
func (s *stubAuthService) VerifyOAuthSignup(_ context.Context, _, _, _, _, _, _ string) (*service.AuthResponse, error) {
	return nil, nil
}

//func (s *stubAuthService) ForgotPassword(_ context.Context, _ string) error      { return nil }
//func (s *stubAuthService) ResetPassword(_ context.Context, _, _, _ string) error { return nil }

// Password reset stubs
func (s *stubAuthService) ForgotPassword(_ context.Context, _ string) error      { return nil }
func (s *stubAuthService) ResetPassword(_ context.Context, _, _, _ string) error { return nil }

// Email/Phone verification stubs
func (s *stubAuthService) RequestEmailVerification(_ context.Context, _ uuid.UUID) error { return nil }
func (s *stubAuthService) VerifyEmail(_ context.Context, _ uuid.UUID, _ string) error    { return nil }
func (s *stubAuthService) RequestPhoneVerification(_ context.Context, _ uuid.UUID) error { return nil }
func (s *stubAuthService) VerifyPhone(_ context.Context, _ uuid.UUID, _ string) error    { return nil }

// Trusted device stubs
func (s *stubAuthService) ListTrustedDevices(_ context.Context, _ uuid.UUID) ([]store.TrustedDevice, error) {
	return nil, nil
}
func (s *stubAuthService) TrustDevice(_ context.Context, _ uuid.UUID, _ string, _ *string) error {
	return nil
}
func (s *stubAuthService) RemoveTrustedDevice(_ context.Context, _, _ uuid.UUID) error { return nil }
func (s *stubAuthService) ListMyAnomalies(_ context.Context, _ uuid.UUID, _ int) ([]store.LoginAnomaly, error) {
	return nil, nil
}
func (s *stubAuthService) AcknowledgeMyAnomaly(_ context.Context, _, _ uuid.UUID) error { return nil }
func (s *stubAuthService) ResolveAnomalyStepUpEmail(_ context.Context, _, _ string) (*service.AuthResponse, error) {
	return nil, nil
}
func (s *stubAuthService) ResolveAnomalyStepUp2FA(_ context.Context, _, _ string) (*service.AuthResponse, error) {
	return nil, nil
}

// GDPR stub
func (s *stubAuthService) ExportUserData(_ context.Context, _ string) (*service.DataExport, error) {
	return &service.DataExport{}, nil
}
func (s *stubAuthService) GetUserContact(_ context.Context, _ uuid.UUID) (*store.User, error) {
	return &store.User{}, nil
}
func (s *stubAuthService) IssueMiniAppSession(_ context.Context, appID, userID uuid.UUID, grantedPermissions []string) (*service.MiniAppSessionResponse, error) {
	if s.issueMiniAppFn != nil {
		return s.issueMiniAppFn(appID, userID, grantedPermissions)
	}
	return &service.MiniAppSessionResponse{
		AppID:              appID.String(),
		UserID:             userID.String(),
		TokenType:          "Bearer",
		AccessToken:        "stub",
		GrantedPermissions: grantedPermissions,
	}, nil
}
func (s *stubAuthService) MiniAppJWKS(_ context.Context) (*service.JSONWebKeySet, error) {
	if s.miniAppJWKSFn != nil {
		return s.miniAppJWKSFn()
	}
	return &service.JSONWebKeySet{
		Keys: []service.JSONWebKey{{Kty: "RSA", Kid: "stub", Alg: "RS256"}},
	}, nil
}

func noopMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) { c.Next() }
}

func TestRequestOTPInvalidBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&stubAuthService{}, &config.Config{}, nil, nil)
	h.RegisterRoutes(r, noopMiddleware(), noopMiddleware())

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/request-otp", bytes.NewBufferString("{bad-json"))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, resp.Code)
	}
}

func TestLoginMissingIdentifier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&stubAuthService{}, &config.Config{}, nil, nil)
	h.RegisterRoutes(r, noopMiddleware(), noopMiddleware())

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewBufferString(`{"password":"secret"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, resp.Code)
	}
}

func TestForgotPasswordMissingIdentifier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&stubAuthService{}, &config.Config{}, nil, nil)
	h.RegisterRoutes(r, noopMiddleware(), noopMiddleware())

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/forgot-password", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should return 400 if identifier is required, or 200 (privacy-safe no-op)
	if w.Code != http.StatusOK && w.Code != http.StatusBadRequest {
		t.Errorf("unexpected status %d", w.Code)
	}
}

func TestRegisterSetsCookies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	cfg := &config.Config{
		RefreshTokenTTL: 30 * time.Minute,
		CookieDomain:    "",
		CookieSecure:    false,
	}
	h := New(&stubAuthService{
		registerWithPassFn: func(phone, email, password, firstName, lastName, dob, gender string) (*service.AuthResponse, error) {
			return &service.AuthResponse{
				Tokens: service.TokenPair{
					AccessToken:  "access",
					RefreshToken: "refresh",
					ExpiresAt:    time.Now().Add(15 * time.Minute),
				},
			}, nil
		},
	}, cfg, nil, nil)
	h.RegisterRoutes(r, noopMiddleware(), noopMiddleware())

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/register", bytes.NewBufferString(`{"email":"a@b.com","password":"secret"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, resp.Code)
	}
	cookies := resp.Result().Cookies()
	foundAccess := false
	foundRefresh := false
	for _, c := range cookies {
		if c.Name == accessTokenCookieName {
			foundAccess = true
		}
		if c.Name == refreshTokenCookieName {
			foundRefresh = true
		}
	}
	if !foundAccess || !foundRefresh {
		t.Fatalf("expected access and refresh cookies to be set")
	}
}

func TestMiniAppJWKSPublicRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&stubAuthService{}, &config.Config{}, nil, nil)
	h.RegisterRoutes(r, noopMiddleware(), noopMiddleware())

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/.well-known/jwks.json", nil)
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resp.Code)
	}
}

func TestCreateMiniAppSessionRequiresInternalServiceKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&stubAuthService{}, &config.Config{InternalServiceKey: "test-internal-key"}, nil, nil)
	h.RegisterRoutes(r, noopMiddleware(), noopMiddleware())

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/internal/mini-app-session", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, resp.Code)
	}
}

func TestCreateMiniAppSessionSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	appID := uuid.New()
	userID := uuid.New()
	called := false

	h := New(&stubAuthService{
		issueMiniAppFn: func(gotAppID, gotUserID uuid.UUID, grantedPermissions []string) (*service.MiniAppSessionResponse, error) {
			called = true
			if gotAppID != appID {
				t.Fatalf("unexpected app id: %s", gotAppID)
			}
			if gotUserID != userID {
				t.Fatalf("unexpected user id: %s", gotUserID)
			}
			if len(grantedPermissions) != 1 || grantedPermissions[0] != "clipboard.write" {
				t.Fatalf("unexpected granted permissions: %#v", grantedPermissions)
			}
			return &service.MiniAppSessionResponse{
				AppID:              gotAppID.String(),
				UserID:             gotUserID.String(),
				TokenType:          "Bearer",
				AccessToken:        "mini-app-token",
				GrantedPermissions: grantedPermissions,
			}, nil
		},
	}, &config.Config{InternalServiceKey: "test-internal-key"}, nil, nil)
	h.RegisterRoutes(r, noopMiddleware(), noopMiddleware())

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/internal/mini-app-session", bytes.NewBufferString(`{"app_id":"`+appID.String()+`","user_id":"`+userID.String()+`","granted_permissions":["clipboard.write"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", "test-internal-key")
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resp.Code)
	}
	if !called {
		t.Fatal("expected IssueMiniAppSession to be called")
	}
}

func TestCreateMiniAppSessionMapsUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	appID := uuid.New()
	userID := uuid.New()

	h := New(&stubAuthService{
		issueMiniAppFn: func(gotAppID, gotUserID uuid.UUID, grantedPermissions []string) (*service.MiniAppSessionResponse, error) {
			return nil, serviceErr("MINI_APP_SESSION_UNAVAILABLE")
		},
	}, &config.Config{InternalServiceKey: "test-internal-key"}, nil, nil)
	h.RegisterRoutes(r, noopMiddleware(), noopMiddleware())

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/internal/mini-app-session", bytes.NewBufferString(`{"app_id":"`+appID.String()+`","user_id":"`+userID.String()+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", "test-internal-key")
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, resp.Code)
	}
}

type serviceErr string

func (e serviceErr) Error() string {
	return string(e)
}
