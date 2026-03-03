package http

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-auth-service/internal/store"
)

type stubAuthService struct {
	requestOTPFn       func(phone, purpose string) error
	verifyOTPFn        func(phone, code, purpose, deviceID, platform, ip, userAgent string) (*service.AuthResponse, error)
	registerWithPassFn func(phone, email, password, firstName, lastName, dob, gender string) (*service.AuthResponse, error)
	loginWithPassFn    func(identifier, password, deviceID, platform, ip, userAgent string) (*service.AuthResponse, error)
	refreshSessionFn   func(refreshToken, ip, userAgent string) (*service.AuthResponse, error)
	logoutFn           func(refreshToken string) error
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
func (s *stubAuthService) HandleOAuthCallback(_ context.Context, _, _, _ string) (*service.AuthResponse, error) {
	return nil, nil
}
func (s *stubAuthService) HandleOAuthToken(_ context.Context, _, _ string) (*service.AuthResponse, error) {
	return nil, nil
}

//func (s *stubAuthService) ForgotPassword(_ context.Context, _ string) error      { return nil }
//func (s *stubAuthService) ResetPassword(_ context.Context, _, _, _ string) error { return nil }

// Password reset stubs
func (s *stubAuthService) ForgotPassword(_ context.Context, _ string) error { return nil }
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

// GDPR stub
func (s *stubAuthService) ExportUserData(_ context.Context, _ string) (*service.DataExport, error) {
	return &service.DataExport{}, nil
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
