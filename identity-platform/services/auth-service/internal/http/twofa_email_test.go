package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/atpost/identity-auth-service/pkg/accesstoken"
	"github.com/google/uuid"
)

// email2FAStub holds the email 2FA hooks of stubAuthService.
type email2FAStub struct {
	startFn       func(userID uuid.UUID, password, purpose string) (*service.EmailChallenge, error)
	enableFn      func(userID uuid.UUID, challenge, code string) error
	disableFn     func(userID uuid.UUID, challenge, code string) error
	resendFn      func(pending string) (*service.EmailCodeSent, error)
	stepUpStartFn func(userID, sessionID uuid.UUID) (*service.EmailChallenge, error)
	stepUpFn      func(userID, sessionID uuid.UUID, challenge, code string) (*service.StepUpResponse, error)
	setupErr      error
	disableTOTP   func(userID uuid.UUID, password, code string) error
	contact       *store.User
}

func (s *stubAuthService) StartEmail2FA(_ context.Context, userID uuid.UUID, password, purpose string) (*service.EmailChallenge, error) {
	if s.email2FA.startFn == nil {
		return &service.EmailChallenge{}, nil
	}
	return s.email2FA.startFn(userID, password, purpose)
}
func (s *stubAuthService) EnableEmail2FA(_ context.Context, userID uuid.UUID, challenge, code string) error {
	if s.email2FA.enableFn == nil {
		return nil
	}
	return s.email2FA.enableFn(userID, challenge, code)
}
func (s *stubAuthService) DisableEmail2FA(_ context.Context, userID uuid.UUID, challenge, code string) error {
	if s.email2FA.disableFn == nil {
		return nil
	}
	return s.email2FA.disableFn(userID, challenge, code)
}
func (s *stubAuthService) Resend2FACode(_ context.Context, pending string) (*service.EmailCodeSent, error) {
	if s.email2FA.resendFn == nil {
		return &service.EmailCodeSent{Sent: true}, nil
	}
	return s.email2FA.resendFn(pending)
}
func (s *stubAuthService) StartStepUpEmail(_ context.Context, userID, sessionID uuid.UUID) (*service.EmailChallenge, error) {
	if s.email2FA.stepUpStartFn == nil {
		return &service.EmailChallenge{}, nil
	}
	return s.email2FA.stepUpStartFn(userID, sessionID)
}
func (s *stubAuthService) StepUpEmail(_ context.Context, userID, sessionID uuid.UUID, challenge, code string) (*service.StepUpResponse, error) {
	if s.email2FA.stepUpFn == nil {
		return &service.StepUpResponse{}, nil
	}
	return s.email2FA.stepUpFn(userID, sessionID, challenge, code)
}

// email2FARouter: the real routes behind the real auth middleware.
func email2FARouter(t *testing.T, svc *stubAuthService) (func(method, path, body string) (int, string, map[string]any), uuid.UUID, uuid.UUID) {
	t.Helper()
	r := a2Router(t, svc)
	user, sid := uuid.New(), uuid.New()
	tok := a2Token(t, user, sid, accesstoken.Session{AuthTime: time.Now(), AMR: []string{"pwd"}})
	return func(method, path, body string) (int, string, map[string]any) {
		resp := a2Do(r, method, path, tok, body, nil)
		var env map[string]any
		_ = json.Unmarshal(resp.Body.Bytes(), &env)
		return resp.Code, errCode(t, resp), env
	}, user, sid
}

func TestEmail2FAStartRoute(t *testing.T) {
	var gotUser uuid.UUID
	var gotPurpose string
	svc := &stubAuthService{}
	svc.email2FA.startFn = func(u uuid.UUID, pw, purpose string) (*service.EmailChallenge, error) {
		gotUser, gotPurpose = u, purpose
		if pw != "right" {
			return nil, service.ErrInvalidCredentials
		}
		return &service.EmailChallenge{ChallengeToken: "ch", ExpiresAt: time.Now().Add(5 * time.Minute), EmailMasked: "r***@gmail.com"}, nil
	}
	do, user, _ := email2FARouter(t, svc)

	if code, ec, _ := do("POST", "/v1/auth/2fa/email/start", `{"password":"wrong","purpose":"enable"}`); code != http.StatusUnauthorized || ec != CodeInvalidCredentials {
		t.Fatalf("wrong password: %d %s", code, ec)
	}
	code, _, env := do("POST", "/v1/auth/2fa/email/start", `{"password":"right","purpose":"enable"}`)
	if code != http.StatusOK || gotUser != user || gotPurpose != "enable" {
		t.Fatalf("start: %d user=%v purpose=%q", code, gotUser == user, gotPurpose)
	}
	data, _ := env["data"].(map[string]any)
	if data == nil {
		data = env
	}
	for _, k := range []string{"challenge_token", "expires_at", "email_masked"} {
		if _, ok := data[k]; !ok {
			t.Fatalf("response lacks %q: %v", k, env)
		}
	}
	if code, ec, _ := do("POST", "/v1/auth/2fa/email/start", `{"purpose":"enable"}`); code != http.StatusBadRequest || ec != "INVALID_REQUEST" {
		t.Fatalf("no password: %d %s", code, ec)
	}
}

// Every service refusal maps to its pinned contract code.
func TestEmail2FAErrorCodes(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{service.ErrVerifyCodeInvalid, http.StatusBadRequest, "INVALID_CODE"},
		{service.ErrEmailChallengeInvalid, http.StatusBadRequest, "INVALID_CHALLENGE"},
		{service.ErrTwoFactorEmailUnverified, http.StatusConflict, "EMAIL_NOT_VERIFIED"},
		{service.ErrTwoFactorAlreadyEnabled, http.StatusConflict, "TWO_FACTOR_ALREADY_ENABLED"},
		{service.ErrTwoFactorNotEnabled, http.StatusConflict, "TWO_FACTOR_NOT_ENABLED"},
		{service.ErrTwoFactorMethodMismatch, http.StatusConflict, "TWO_FACTOR_METHOD_MISMATCH"},
		{&service.ErrThrottled{RetryAfter: 30 * time.Second}, http.StatusTooManyRequests, "RATE_LIMITED"},
		{errors.Join(service.ErrEmailDeliveryUnavailable), http.StatusServiceUnavailable, "EMAIL_UNAVAILABLE"},
	}
	for _, tc := range cases {
		svc := &stubAuthService{}
		svc.email2FA.enableFn = func(uuid.UUID, string, string) error { return tc.err }
		do, _, _ := email2FARouter(t, svc)
		if code, ec, _ := do("POST", "/v1/auth/2fa/email/enable", `{"challenge_token":"ch","code":"123456"}`); code != tc.status || ec != tc.code {
			t.Errorf("%v: %d %s, want %d %s", tc.err, code, ec, tc.status, tc.code)
		}
	}
}

func TestEmail2FAEnableRoute(t *testing.T) {
	svc := &stubAuthService{}
	svc.email2FA.enableFn = func(_ uuid.UUID, ch, code string) error {
		if ch != "ch" || code != "123456" {
			return service.ErrVerifyCodeInvalid
		}
		return nil
	}
	do, _, _ := email2FARouter(t, svc)
	code, _, env := do("POST", "/v1/auth/2fa/email/enable", `{"challenge_token":"ch","code":" 123456 "}`)
	if code != http.StatusOK {
		t.Fatalf("enable: %d %v", code, env)
	}
	data, _ := env["data"].(map[string]any)
	if data == nil {
		data = env
	}
	if data["enabled"] != true || data["method"] != "email" {
		t.Fatalf("enable body = %v", env)
	}
	if code, ec, _ := do("POST", "/v1/auth/2fa/email/enable", `{"challenge_token":"ch"}`); code != http.StatusBadRequest || ec != "INVALID_REQUEST" {
		t.Fatalf("no code: %d %s", code, ec)
	}
}

// /2fa/disable routes a challenge to the email path and a password to TOTP.
func TestDisable2FARoutesByBody(t *testing.T) {
	var emailCalls, totpCalls int
	svc := &stubAuthService{}
	svc.email2FA.disableFn = func(uuid.UUID, string, string) error { emailCalls++; return nil }
	svc.email2FA.disableTOTP = func(uuid.UUID, string, string) error { totpCalls++; return service.ErrTwoFactorMethodMismatch }
	do, _, _ := email2FARouter(t, svc)

	if code, _, _ := do("POST", "/v1/auth/2fa/disable", `{"challenge_token":"ch","code":"123456"}`); code != http.StatusOK || emailCalls != 1 {
		t.Fatalf("email disable: %d calls=%d", code, emailCalls)
	}
	if code, ec, _ := do("POST", "/v1/auth/2fa/disable", `{"password":"pw","code":"123456"}`); code != http.StatusConflict || ec != CodeTwoFactorMethodMismatch || totpCalls != 1 {
		t.Fatalf("TOTP disable of an email account: %d %s calls=%d", code, ec, totpCalls)
	}
	if code, _, _ := do("POST", "/v1/auth/2fa/disable", `{"code":"123456"}`); code != http.StatusBadRequest {
		t.Fatalf("neither challenge nor password: %d", code)
	}
}

func TestTOTPSetupRefusedRoute(t *testing.T) {
	svc := &stubAuthService{}
	svc.email2FA.setupErr = service.ErrTOTPNotOffered
	do, _, _ := email2FARouter(t, svc)
	if code, ec, _ := do("POST", "/v1/auth/2fa/setup", `{}`); code != http.StatusForbidden || ec != CodeTOTPNotOffered {
		t.Fatalf("setup: %d %s", code, ec)
	}
	if code, ec, _ := do("POST", "/v1/auth/2fa/verify-setup", `{"code":"123456"}`); code != http.StatusForbidden || ec != CodeTOTPNotOffered {
		t.Fatalf("verify-setup: %d %s", code, ec)
	}
}

// POST /step-up with a challenge goes to the email path on the caller's
// session; with {otp} it stays on TOTP.
func TestStepUpRoutesEmailChallenge(t *testing.T) {
	var gotSID uuid.UUID
	var totp int
	svc := &stubAuthService{stepUpFn: func(uuid.UUID, uuid.UUID, string) (*service.StepUpResponse, error) {
		totp++
		return &service.StepUpResponse{AccessToken: "t"}, nil
	}}
	svc.email2FA.stepUpFn = func(_ uuid.UUID, sid uuid.UUID, ch, code string) (*service.StepUpResponse, error) {
		gotSID = sid
		if code != "123456" {
			return nil, service.ErrVerifyCodeInvalid
		}
		now := time.Now()
		return &service.StepUpResponse{AccessToken: "t", StepUpAt: now, StepUpValidUntil: now.Add(accesstoken.StepUpValidity)}, nil
	}
	svc.email2FA.stepUpStartFn = func(_ uuid.UUID, sid uuid.UUID) (*service.EmailChallenge, error) {
		return &service.EmailChallenge{ChallengeToken: "ch"}, nil
	}
	do, _, sid := email2FARouter(t, svc)

	if code, _, _ := do("POST", "/v1/auth/step-up/email/start", ``); code != http.StatusOK {
		t.Fatalf("step-up start: %d", code)
	}
	if code, ec, _ := do("POST", "/v1/auth/step-up", `{"challenge_token":"ch","code":"000000"}`); code != http.StatusBadRequest || ec != CodeInvalidCode {
		t.Fatalf("wrong code: %d %s", code, ec)
	}
	code, _, env := do("POST", "/v1/auth/step-up", `{"challenge_token":"ch","code":"123456"}`)
	if code != http.StatusOK || gotSID != sid || totp != 0 {
		t.Fatalf("email step-up: %d sid=%v totp=%d", code, gotSID == sid, totp)
	}
	data, _ := env["data"].(map[string]any)
	if data == nil {
		data = env
	}
	if _, ok := data["step_up_at"]; !ok {
		t.Fatalf("no step_up_at: %v", env)
	}
	if code, _, _ := do("POST", "/v1/auth/step-up", `{"otp":"123456"}`); code != http.StatusOK || totp != 1 {
		t.Fatalf("TOTP step-up: %d totp=%d", code, totp)
	}
}

func TestResend2FARoute(t *testing.T) {
	svc := &stubAuthService{}
	svc.email2FA.resendFn = func(p string) (*service.EmailCodeSent, error) {
		switch p {
		case "live":
			return &service.EmailCodeSent{Sent: true, ResendAfterSeconds: 60}, nil
		case "early":
			return nil, &service.ErrThrottled{RetryAfter: 42 * time.Second}
		}
		return nil, service.ErrPending2FAInvalid
	}
	r := a2Router(t, svc)
	// Public: no token.
	if resp := a2Do(r, "POST", "/v1/auth/2fa/resend", "", `{"pending_token":"live"}`, nil); resp.Code != http.StatusOK {
		t.Fatalf("resend: %d", resp.Code)
	}
	resp := a2Do(r, "POST", "/v1/auth/2fa/resend", "", `{"pending_token":"early"}`, nil)
	if resp.Code != http.StatusTooManyRequests || resp.Header().Get("Retry-After") != "42" {
		t.Fatalf("cooldown: %d Retry-After=%q", resp.Code, resp.Header().Get("Retry-After"))
	}
	if resp := a2Do(r, "POST", "/v1/auth/2fa/resend", "", `{"pending_token":"gone"}`, nil); resp.Code != http.StatusUnauthorized || errCode(t, resp) != "AUTH_FAILED" {
		t.Fatalf("expired pending: %d %s", resp.Code, errCode(t, resp))
	}
}

// GET /me tells the client which second factor the account uses.
func TestMeExposesTwoFactorMethod(t *testing.T) {
	for _, tc := range []struct {
		user   store.User
		method string
	}{
		{store.User{TwoFactorEnabled: true, TwoFactorMethod: store.TwoFactorMethodEmail}, "email"},
		{store.User{TwoFactorEnabled: true}, "totp"}, // legacy row with no method
		{store.User{}, ""},
	} {
		u := tc.user
		svc := &stubAuthService{}
		svc.email2FA.contact = &u
		do, _, _ := email2FARouter(t, svc)
		code, _, env := do("GET", "/v1/auth/me", "")
		data, _ := env["data"].(map[string]any)
		if data == nil {
			data = env
		}
		if code != http.StatusOK || data["two_factor_method"] != tc.method || data["two_factor_enabled"] != u.TwoFactorEnabled {
			t.Fatalf("/me = %d %v, want method %q", code, env, tc.method)
		}
	}
}
