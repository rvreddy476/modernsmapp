package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Account control — deactivate and delete-with-window at the HTTP boundary.
//
// These prove the contract the mobile client codes against: the password is
// forwarded for re-verification, a mismatch is 401 INVALID_PASSWORD, a missing
// password never reaches the service, success clears the caller's cookies
// (every session was revoked) and DELETE answers the scheduled-purge shape.

type lifecycleStubService struct {
	stubAuthService
	deactivateCalls []struct {
		userID   uuid.UUID
		password string
	}
	deleteCalls []struct {
		userID   uuid.UUID
		password string
	}
	deactivateErr error
	deleteErr     error
	purgeDate     time.Time
}

func (s *lifecycleStubService) DeactivateAccount(_ context.Context, userID uuid.UUID, password string) error {
	s.deactivateCalls = append(s.deactivateCalls, struct {
		userID   uuid.UUID
		password string
	}{userID, password})
	return s.deactivateErr
}

func (s *lifecycleStubService) DeleteAccount(_ context.Context, userID uuid.UUID, password string) (*service.DeletionSchedule, error) {
	s.deleteCalls = append(s.deleteCalls, struct {
		userID   uuid.UUID
		password string
	}{userID, password})
	if s.deleteErr != nil {
		return nil, s.deleteErr
	}
	return &service.DeletionSchedule{
		AccountStatus:      "pending_deletion",
		ScheduledPurgeDate: s.purgeDate,
		CancelByLoggingIn:  true,
	}, nil
}

func lifecycleRouter(t *testing.T, svc *lifecycleStubService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(svc, &config.Config{}, nil, nil)
	h.RegisterRoutes(r, noopMiddleware(), noopMiddleware())
	return r
}

func accountControlRequest(t *testing.T, r *gin.Engine, method, path string, body any, userID uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", userID.String())
	// Pretend the caller is signed in, so we can prove the cookies get cleared.
	req.AddCookie(&http.Cookie{Name: accessTokenCookieName, Value: "live-access"})
	req.AddCookie(&http.Cookie{Name: refreshTokenCookieName, Value: "live-refresh"})
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	return resp
}

type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decodeError(t *testing.T, resp *httptest.ResponseRecorder) errorEnvelope {
	t.Helper()
	var e errorEnvelope
	if err := json.Unmarshal(resp.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode %q: %v", resp.Body.String(), err)
	}
	return e
}

func assertAuthCookiesCleared(t *testing.T, resp *httptest.ResponseRecorder) {
	t.Helper()
	cleared := map[string]bool{}
	for _, c := range resp.Result().Cookies() {
		if (c.Name == accessTokenCookieName || c.Name == refreshTokenCookieName) && c.MaxAge < 0 {
			cleared[c.Name] = true
		}
	}
	if !cleared[accessTokenCookieName] || !cleared[refreshTokenCookieName] {
		t.Fatalf("auth cookies were not cleared after every session was revoked: %+v", resp.Result().Cookies())
	}
}

// ── Deactivate ──────────────────────────────────────────────────────────────

func TestDeactivate_ForwardsPasswordAndClearsCookies(t *testing.T) {
	svc := &lifecycleStubService{}
	r := lifecycleRouter(t, svc)
	uid := uuid.New()

	resp := accountControlRequest(t, r, http.MethodPost, "/v1/auth/account/deactivate",
		map[string]string{"password": "CallTest#2026"}, uid)

	if resp.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.Code, resp.Body.String())
	}
	if len(svc.deactivateCalls) != 1 || svc.deactivateCalls[0].userID != uid || svc.deactivateCalls[0].password != "CallTest#2026" {
		t.Fatalf("service call not forwarded faithfully: %+v", svc.deactivateCalls)
	}
	assertAuthCookiesCleared(t, resp)

	var body struct {
		Data struct {
			AccountStatus string `json:"account_status"`
			Reactivate    bool   `json:"reactivate_by_logging_in"`
		} `json:"data"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &body)
	if body.Data.AccountStatus != "deactivated" || !body.Data.Reactivate {
		t.Fatalf("unexpected body %s", resp.Body.String())
	}
}

func TestDeactivate_WrongPasswordIs401(t *testing.T) {
	svc := &lifecycleStubService{deactivateErr: service.ErrInvalidPassword}
	r := lifecycleRouter(t, svc)

	resp := accountControlRequest(t, r, http.MethodPost, "/v1/auth/account/deactivate",
		map[string]string{"password": "nope"}, uuid.New())

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.Code)
	}
	if e := decodeError(t, resp); e.Error.Code != "INVALID_PASSWORD" {
		t.Fatalf("code %q, want INVALID_PASSWORD", e.Error.Code)
	}
}

func TestDeactivate_MissingPasswordNeverReachesService(t *testing.T) {
	svc := &lifecycleStubService{}
	r := lifecycleRouter(t, svc)

	resp := accountControlRequest(t, r, http.MethodPost, "/v1/auth/account/deactivate",
		map[string]string{}, uuid.New())

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", resp.Code)
	}
	if len(svc.deactivateCalls) != 0 {
		t.Fatal("the service was called without a password to re-verify")
	}
}

func TestDeactivate_StateConflictIs409(t *testing.T) {
	svc := &lifecycleStubService{deactivateErr: service.ErrLifecycleConflict}
	r := lifecycleRouter(t, svc)

	resp := accountControlRequest(t, r, http.MethodPost, "/v1/auth/account/deactivate",
		map[string]string{"password": "CallTest#2026"}, uuid.New())

	if resp.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409", resp.Code)
	}
	if e := decodeError(t, resp); e.Error.Code != "ACCOUNT_STATE_CONFLICT" {
		t.Fatalf("code %q", e.Error.Code)
	}
}

// ── Delete ──────────────────────────────────────────────────────────────────

func TestDelete_SchedulesPurgeAndClearsCookies(t *testing.T) {
	purgeAt := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	svc := &lifecycleStubService{purgeDate: purgeAt}
	r := lifecycleRouter(t, svc)
	uid := uuid.New()

	resp := accountControlRequest(t, r, http.MethodDelete, "/v1/auth/account",
		map[string]string{"password": "CallTest#2026"}, uid)

	if resp.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.Code, resp.Body.String())
	}
	if len(svc.deleteCalls) != 1 || svc.deleteCalls[0].password != "CallTest#2026" || svc.deleteCalls[0].userID != uid {
		t.Fatalf("service call not forwarded faithfully: %+v", svc.deleteCalls)
	}
	assertAuthCookiesCleared(t, resp)

	var body struct {
		Data struct {
			AccountStatus      string    `json:"account_status"`
			ScheduledPurgeDate time.Time `json:"scheduled_purge_date"`
			CancelByLoggingIn  bool      `json:"cancel_by_logging_in"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", resp.Body.String(), err)
	}
	if body.Data.AccountStatus != "pending_deletion" {
		t.Errorf("account_status = %q", body.Data.AccountStatus)
	}
	if !body.Data.ScheduledPurgeDate.Equal(purgeAt) {
		t.Errorf("scheduled_purge_date = %v, want %v", body.Data.ScheduledPurgeDate, purgeAt)
	}
	if !body.Data.CancelByLoggingIn {
		t.Error("cancel_by_logging_in must be true — the client renders the rescue hint from it")
	}
}

func TestDelete_WrongPasswordIs401AndMutatesNothing(t *testing.T) {
	svc := &lifecycleStubService{deleteErr: service.ErrInvalidPassword}
	r := lifecycleRouter(t, svc)

	resp := accountControlRequest(t, r, http.MethodDelete, "/v1/auth/account",
		map[string]string{"password": "wrong"}, uuid.New())

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.Code)
	}
	if e := decodeError(t, resp); e.Error.Code != "INVALID_PASSWORD" {
		t.Fatalf("code %q", e.Error.Code)
	}
	// A refused request must not sign the user out.
	for _, c := range resp.Result().Cookies() {
		if (c.Name == accessTokenCookieName || c.Name == refreshTokenCookieName) && c.MaxAge < 0 {
			t.Fatalf("cookie %s cleared on a refused deletion", c.Name)
		}
	}
}

func TestDelete_MissingPasswordIs400(t *testing.T) {
	svc := &lifecycleStubService{}
	r := lifecycleRouter(t, svc)

	resp := accountControlRequest(t, r, http.MethodDelete, "/v1/auth/account", nil, uuid.New())

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", resp.Code)
	}
	if len(svc.deleteCalls) != 0 {
		t.Fatal("the service was called without a password to re-verify")
	}
}

// ── Login mapping for terminal states ───────────────────────────────────────

func TestLogin_PurgedAndPendingPurgeAre403(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{service.ErrAccountPurged, "ACCOUNT_PURGED"},
		{service.ErrAccountPendingPurge, "ACCOUNT_PENDING_PURGE"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			svc := &lifecycleStubService{}
			svc.loginWithPassFn = func(_, _, _, _, _, _ string) (*service.AuthResponse, error) {
				return nil, tc.err
			}
			r := lifecycleRouter(t, svc)

			var buf bytes.Buffer
			_ = json.NewEncoder(&buf).Encode(map[string]string{"identifier": "x@example.com", "password": "CallTest#2026"})
			req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", &buf)
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			r.ServeHTTP(resp, req)

			if resp.Code != http.StatusForbidden {
				t.Fatalf("status %d, want 403: %s", resp.Code, resp.Body.String())
			}
			if e := decodeError(t, resp); e.Error.Code != tc.code {
				t.Fatalf("code %q, want %q", e.Error.Code, tc.code)
			}
		})
	}
}
