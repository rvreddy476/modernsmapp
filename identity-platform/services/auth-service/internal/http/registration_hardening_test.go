package http

// Handler-level regression tests for the registration / verification / rate
// limit audit: field-specific validation codes, forgot-password timing, and
// generic answers for password re-checks.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// hardeningStub overrides the few service methods these tests drive.
type hardeningStub struct {
	*stubAuthService
	forgot     func(ctx context.Context, identifier string) error
	disable2FA func(userID uuid.UUID, password, code string) error
	verify2FA  func(userID uuid.UUID, code, pending string) (*service.AuthResponse, error)
	verifyMail func(tx, code string) error
	deactivate func(userID uuid.UUID, password string) error
}

func (h *hardeningStub) ForgotPassword(ctx context.Context, identifier string) error {
	if h.forgot != nil {
		return h.forgot(ctx, identifier)
	}
	return nil
}
func (h *hardeningStub) Disable2FA(_ context.Context, userID uuid.UUID, password, code string) error {
	return h.disable2FA(userID, password, code)
}
func (h *hardeningStub) Verify2FA(_ context.Context, userID uuid.UUID, code, pending string) (*service.AuthResponse, error) {
	return h.verify2FA(userID, code, pending)
}
func (h *hardeningStub) VerifyEmailWithTransaction(_ context.Context, tx, code string) error {
	return h.verifyMail(tx, code)
}
func (h *hardeningStub) DeactivateAccount(_ context.Context, userID uuid.UUID, password string) error {
	return h.deactivate(userID, password)
}

func hardeningRouter(svc AuthService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(svc, &config.Config{}, nil, nil)
	h.RegisterRoutes(r, noopMiddleware(), noopMiddleware())
	return r
}

func doJSON(r *gin.Engine, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func hardeningErrCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return body.Error.Code
}

func registrationBodyWith(field, value string) string {
	var m map[string]any
	_ = json.Unmarshal([]byte(validRegistrationBody()), &m)
	m["password"] = "Str0ng!pass"
	m[field] = value
	b, _ := json.Marshal(m)
	return string(b)
}

// M1: the email is normalised before it reaches the service, and junk is
// refused with its own code.
func TestRegisterNormalisesAndValidatesEmail(t *testing.T) {
	var got string
	stub := &stubAuthService{registerWithConsentFn: func(_, email, _, _, _, _, _ string, _ service.RegistrationConsent) (*service.AuthResponse, error) {
		got = email
		return &service.AuthResponse{RequiresVerification: true}, nil
	}}
	r := hardeningRouter(stub)

	w := doJSON(r, http.MethodPost, "/v1/auth/register", registrationBodyWith("email", "  Raghu@Example.COM "), nil)
	if w.Code != http.StatusCreated || got != "raghu@example.com" {
		t.Fatalf("status %d, service got %q; want 201 and the normalised address", w.Code, got)
	}

	got = ""
	w = doJSON(r, http.MethodPost, "/v1/auth/register", registrationBodyWith("email", "foo"), nil)
	if w.Code != http.StatusUnprocessableEntity || hardeningErrCode(t, w) != "INVALID_EMAIL" {
		t.Fatalf("junk email: %d %s; want 422 INVALID_EMAIL", w.Code, w.Body.String())
	}
	if got != "" {
		t.Fatal("an invalid email reached the service")
	}
}

// H2: an email-shaped or otherwise non-E.164 phone is refused.
func TestRegisterRejectsNonE164Phone(t *testing.T) {
	called := false
	stub := &stubAuthService{registerWithConsentFn: func(_, _, _, _, _, _, _ string, _ service.RegistrationConsent) (*service.AuthResponse, error) {
		called = true
		return &service.AuthResponse{RequiresVerification: true}, nil
	}}
	r := hardeningRouter(stub)
	for _, phone := range []string{"victim@gmail.com", "9876543210", "+91abc"} {
		w := doJSON(r, http.MethodPost, "/v1/auth/register", registrationBodyWith("phone", phone), nil)
		if w.Code != http.StatusUnprocessableEntity || hardeningErrCode(t, w) != "INVALID_PHONE" {
			t.Fatalf("phone %q: %d %s; want 422 INVALID_PHONE", phone, w.Code, w.Body.String())
		}
	}
	if called {
		t.Fatal("an invalid phone reached the service")
	}
	if w := doJSON(r, http.MethodPost, "/v1/auth/register", registrationBodyWith("phone", "+91 98765 43210"), nil); w.Code != http.StatusCreated {
		t.Fatalf("valid E.164 phone refused: %d %s", w.Code, w.Body.String())
	}
}

// M4 / L1 (login audit): forgot-password answers before the work is done, so
// its response time cannot tell a real account (bcrypt + mail send) from an
// unknown one (nothing). The work still runs, on a context the client
// hanging up cannot cancel.
func TestForgotPasswordAnswersWithoutWaitingForTheSend(t *testing.T) {
	release := make(chan struct{})
	done := make(chan error, 1)
	stub := &hardeningStub{stubAuthService: &stubAuthService{}, forgot: func(ctx context.Context, id string) error {
		<-release
		done <- ctx.Err()
		return nil
	}}
	r := hardeningRouter(stub)

	start := time.Now()
	w := doJSON(r, http.MethodPost, "/v1/auth/forgot-password", `{"identifier":"owner@example.com"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("forgot-password waited %v for the send; its timing reveals the account", elapsed)
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the send ran on a cancelled context: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the reset-code send never ran")
	}
}

// M9: Disable2FA never echoes the service error (which named the factor).
func TestDisable2FAErrorIsGeneric(t *testing.T) {
	for _, svcErr := range []error{service.ErrInvalidCredentials, errAny("invalid password"), errAny("invalid TOTP code")} {
		stub := &hardeningStub{stubAuthService: &stubAuthService{},
			disable2FA: func(uuid.UUID, string, string) error { return svcErr }}
		w := doJSON(hardeningRouter(stub), http.MethodPost, "/v1/auth/2fa/disable",
			`{"password":"x","code":"123456"}`, map[string]string{"X-User-Id": uuid.NewString()})
		body := strings.ToLower(w.Body.String())
		if strings.Contains(body, "totp") || strings.Contains(body, "invalid password") {
			t.Fatalf("response names the failed factor: %s", w.Body.String())
		}
	}
}

func TestPerAccountThrottlesAnswer429(t *testing.T) {
	throttled := &service.ErrThrottled{RetryAfter: 90 * time.Second, Reason: "test"}
	stub := &hardeningStub{stubAuthService: &stubAuthService{},
		disable2FA: func(uuid.UUID, string, string) error { return throttled },
		verify2FA:  func(uuid.UUID, string, string) (*service.AuthResponse, error) { return nil, throttled },
		verifyMail: func(string, string) error { return throttled },
		deactivate: func(uuid.UUID, string) error { return throttled },
	}
	r := hardeningRouter(stub)
	uid := map[string]string{"X-User-Id": uuid.NewString()}
	cases := []struct{ path, body string }{
		{"/v1/auth/2fa/disable", `{"password":"x","code":"1"}`},
		{"/v1/auth/2fa/verify", `{"user_id":"` + uuid.NewString() + `","code":"1","pending_token":"p"}`},
		{"/v1/auth/verify-email", `{"verification_token":"t","code":"1"}`},
		{"/v1/auth/account/deactivate", `{"password":"x"}`},
	}
	for _, c := range cases {
		w := doJSON(r, http.MethodPost, c.path, c.body, uid)
		if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "90" {
			t.Errorf("%s: %d Retry-After=%q; want 429 with Retry-After 90", c.path, w.Code, w.Header().Get("Retry-After"))
		}
	}
}

type errAny string

func (e errAny) Error() string { return string(e) }
