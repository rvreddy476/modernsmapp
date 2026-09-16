package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-auth-service/pkg/accesstoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const a2Secret = "a2-http-test-secret"

// a2Router mounts the real routes behind the REAL auth middleware, so the
// session claims reach the service exactly as in production.
func a2Router(t *testing.T, svc *stubAuthService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(svc, &config.Config{InternalServiceKey: permsInternalKey}, nil, nil)
	h.RegisterRoutes(r, AuthMiddlewareWithKeys(JWTKeySet{ActiveSecret: a2Secret}, nil), noopMiddleware())
	return r
}

func a2Token(t *testing.T, user, sid uuid.UUID, sess accesstoken.Session) string {
	t.Helper()
	tok, _, err := accesstoken.MintSession(accesstoken.Config{HS256Secret: a2Secret, TTL: time.Hour},
		nil, user, sid, "superadmin", sess, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func a2Do(r *gin.Engine, method, path, token, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	return resp
}

func errCode(t *testing.T, resp *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &env)
	return env.Error.Code
}

// The middleware hands the verified claims to the service on the context.
func TestAuthMiddlewareCarriesSessionClaims(t *testing.T) {
	user, sid := uuid.New(), uuid.New()
	authTime := time.Now().Add(-time.Hour).Truncate(time.Second)
	stepUp := time.Now().Truncate(time.Second)
	var got service.SessionAuth
	var ok bool
	svc := &stubAuthService{forceLogoutFn: func(ctx context.Context, _, _ uuid.UUID, _ string) (int, error) {
		got, ok = service.SessionAuthFrom(ctx)
		return 3, nil
	}}
	tok := a2Token(t, user, sid, accesstoken.Session{AuthTime: authTime, AMR: []string{"pwd", "otp"}, AdminMFA: true, StepUpAt: stepUp})
	resp := a2Do(a2Router(t, svc), http.MethodPost, "/v1/auth/admin/users/"+uuid.NewString()+"/sessions/revoke", tok, `{"reason":"x"}`, nil)
	if resp.Code != http.StatusOK || !bytes.Contains(resp.Body.Bytes(), []byte(`"sessions_revoked":3`)) {
		t.Fatalf("got %d %s", resp.Code, resp.Body.String())
	}
	if !ok || got.SessionID != sid || !got.AdminMFA || got.StepUpAt != stepUp.Unix() || got.AuthTime != authTime.Unix() ||
		len(got.AMR) != 2 || got.AMR[1] != "otp" {
		t.Fatalf("session auth on ctx = %+v (ok=%v)", got, ok)
	}
}

// A client-supplied header cannot stand in for the claims.
func TestSessionClaimsIgnoreHeaders(t *testing.T) {
	var got service.SessionAuth
	svc := &stubAuthService{forceLogoutFn: func(ctx context.Context, _, _ uuid.UUID, _ string) (int, error) {
		got, _ = service.SessionAuthFrom(ctx)
		return 0, nil
	}}
	tok := a2Token(t, uuid.New(), uuid.New(), accesstoken.Session{AMR: []string{"pwd"}})
	a2Do(a2Router(t, svc), http.MethodPost, "/v1/auth/admin/users/"+uuid.NewString()+"/sessions/revoke", tok, `{"reason":"x"}`,
		map[string]string{"X-Admin-MFA": "true", "X-Step-Up-At": "9999999999"})
	if got.AdminMFA || got.StepUpAt != 0 {
		t.Fatalf("headers leaked into session auth: %+v", got)
	}
}

// Role changes and force logout surface the stable codes.
func TestPrivilegedRoutesStableCodes(t *testing.T) {
	tok := a2Token(t, uuid.New(), uuid.New(), accesstoken.Session{AdminMFA: true})
	for _, tc := range []struct {
		err  error
		code string
	}{
		{service.ErrStepUpRequired, CodeStepUpRequired},
		{service.ErrMFARequired, CodeMFARequired},
	} {
		svc := &stubAuthService{
			grantRoleFn:   func(uuid.UUID, service.RoleChangeRequest) error { return tc.err },
			forceLogoutFn: func(context.Context, uuid.UUID, uuid.UUID, string) (int, error) { return 0, tc.err },
		}
		r := a2Router(t, svc)
		body := `{"user_id":"` + uuid.NewString() + `","role":"moderator","app":"dating","reason":"x"}`
		if resp := a2Do(r, http.MethodPost, "/v1/auth/admin/roles", tok, body, nil); resp.Code != http.StatusForbidden || errCode(t, resp) != tc.code {
			t.Fatalf("grant: got %d %s want 403 %s", resp.Code, resp.Body.String(), tc.code)
		}
		if resp := a2Do(r, http.MethodPost, "/v1/auth/admin/users/"+uuid.NewString()+"/sessions/revoke", tok, `{"reason":"x"}`, nil); resp.Code != http.StatusForbidden || errCode(t, resp) != tc.code {
			t.Fatalf("force logout: got %d %s want 403 %s", resp.Code, resp.Body.String(), tc.code)
		}
	}
}

func TestStepUpRoute(t *testing.T) {
	user, sid := uuid.New(), uuid.New()
	tok := a2Token(t, user, sid, accesstoken.Session{AMR: []string{"pwd"}})

	t.Run("success uses the token's session and sets the cookie", func(t *testing.T) {
		svc := &stubAuthService{stepUpFn: func(u, s uuid.UUID, code string) (*service.StepUpResponse, error) {
			if u != user || s != sid || code != "123456" {
				t.Fatalf("forwarded user=%s sid=%s code=%q", u, s, code)
			}
			now := time.Now()
			return &service.StepUpResponse{AccessToken: "new-access", ExpiresAt: now.Add(15 * time.Minute), StepUpAt: now, AdminMFA: true}, nil
		}}
		resp := a2Do(a2Router(t, svc), http.MethodPost, "/v1/auth/step-up", tok, `{"otp":"123456"}`, nil)
		if resp.Code != http.StatusOK || !strings.Contains(resp.Header().Get("Set-Cookie"), "access_token=new-access") {
			t.Fatalf("got %d %s cookie=%q", resp.Code, resp.Body.String(), resp.Header().Get("Set-Cookie"))
		}
	})
	t.Run("requires a token", func(t *testing.T) {
		if resp := a2Do(a2Router(t, &stubAuthService{}), http.MethodPost, "/v1/auth/step-up", "", `{"otp":"1"}`, nil); resp.Code != http.StatusUnauthorized {
			t.Fatalf("got %d", resp.Code)
		}
	})
	t.Run("otp required", func(t *testing.T) {
		if resp := a2Do(a2Router(t, &stubAuthService{}), http.MethodPost, "/v1/auth/step-up", tok, `{}`, nil); resp.Code != http.StatusBadRequest {
			t.Fatalf("got %d", resp.Code)
		}
	})
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{service.ErrInvalidOTP, http.StatusUnauthorized, CodeInvalidOTP},
		{service.ErrTOTPReplay, http.StatusUnauthorized, CodeOTPReplayed},
		{service.ErrTOTPNotEnrolled, http.StatusForbidden, CodeMFANotEnrolled},
		{service.ErrSessionNotLive, http.StatusUnauthorized, "SESSION_REVOKED"},
		{&service.ErrThrottled{RetryAfter: time.Minute, Reason: "step_up"}, http.StatusTooManyRequests, "RATE_LIMITED"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			svc := &stubAuthService{stepUpFn: func(uuid.UUID, uuid.UUID, string) (*service.StepUpResponse, error) { return nil, tc.err }}
			resp := a2Do(a2Router(t, svc), http.MethodPost, "/v1/auth/step-up", tok, `{"otp":"123456"}`, nil)
			if resp.Code != tc.status || errCode(t, resp) != tc.code {
				t.Fatalf("got %d %s want %d %s", resp.Code, resp.Body.String(), tc.status, tc.code)
			}
		})
	}
}

func TestInternalPermissionHoldersRoute(t *testing.T) {
	requester := uuid.New()
	svc := &stubAuthService{holdersFn: func(p string, exclude uuid.UUID) (int, error) {
		switch {
		case p == "db:down":
			return 0, errors.New("db down")
		case p == "casino:x":
			return 0, service.ErrInvalidPermission
		case p != "payments:refund.issue" || exclude != requester:
			t.Fatalf("forwarded %q %s", p, exclude)
		}
		return 1, nil
	}}
	r := a2Router(t, svc)
	key := map[string]string{"X-Internal-Service-Key": permsInternalKey}
	path := func(p, q string) string { return "/v1/auth/internal/permissions/" + p + "/holders" + q }

	if resp := a2Do(r, http.MethodGet, path("payments:refund.issue", "?exclude_user_id="+requester.String()), "", "", key); resp.Code != http.StatusOK ||
		!bytes.Contains(resp.Body.Bytes(), []byte(`"count":1`)) {
		t.Fatalf("got %d %s", resp.Code, resp.Body.String())
	}
	if resp := a2Do(r, http.MethodGet, path("payments:refund.issue", ""), "", "", key); resp.Code != http.StatusBadRequest {
		t.Fatalf("missing exclude_user_id: got %d", resp.Code)
	}
	if resp := a2Do(r, http.MethodGet, path("payments:refund.issue", "?exclude_user_id="+requester.String()), "", "", nil); resp.Code != http.StatusUnauthorized {
		t.Fatalf("no key: got %d", resp.Code)
	}
	withUser := map[string]string{"X-Internal-Service-Key": permsInternalKey, "X-User-Id": requester.String()}
	if resp := a2Do(r, http.MethodGet, path("payments:refund.issue", "?exclude_user_id="+requester.String()), "", "", withUser); resp.Code != http.StatusForbidden ||
		errCode(t, resp) != CodeUserCallerRefused {
		t.Fatalf("user identity: got %d %s", resp.Code, resp.Body.String())
	}
	if resp := a2Do(r, http.MethodGet, path("casino:x", "?exclude_user_id="+requester.String()), "", "", key); resp.Code != http.StatusBadRequest {
		t.Fatalf("invalid permission: got %d", resp.Code)
	}
	if resp := a2Do(r, http.MethodGet, path("db:down", "?exclude_user_id="+requester.String()), "", "", key); resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("db failure: got %d", resp.Code)
	}
}
