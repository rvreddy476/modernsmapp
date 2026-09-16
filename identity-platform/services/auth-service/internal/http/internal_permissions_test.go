package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/permissions"
	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const permsInternalKey = "test-internal-key"

func permissionsRouter(t *testing.T, svc *stubAuthService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(svc, &config.Config{InternalServiceKey: permsInternalKey}, nil, nil)
	h.RegisterRoutes(r, noopMiddleware(), noopMiddleware())
	return r
}

func TestInternalPermissionsRoute(t *testing.T) {
	target := uuid.New()
	svc := &stubAuthService{}
	svc.permissionsFn = func(uid uuid.UUID) (permissions.Admin, error) {
		if uid != target {
			t.Fatalf("resolved %s want %s", uid, target)
		}
		return permissions.Resolve([]permissions.Grant{{Role: "moderator", App: "dating"}}, time.Now()), nil
	}
	r := permissionsRouter(t, svc)
	path := "/v1/auth/internal/users/" + target.String() + "/permissions"

	do := func(headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp := httptest.NewRecorder()
		r.ServeHTTP(resp, req)
		return resp
	}

	t.Run("anonymous refused", func(t *testing.T) {
		if resp := do(nil); resp.Code != http.StatusUnauthorized {
			t.Fatalf("got %d want 401", resp.Code)
		}
	})
	t.Run("wrong key refused", func(t *testing.T) {
		if resp := do(map[string]string{"X-Internal-Service-Key": "nope"}); resp.Code != http.StatusUnauthorized {
			t.Fatalf("got %d want 401", resp.Code)
		}
	})
	for _, hdr := range []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role"} {
		t.Run("user identity refused even with key: "+hdr, func(t *testing.T) {
			resp := do(map[string]string{"X-Internal-Service-Key": permsInternalKey, hdr: uuid.NewString()})
			if resp.Code != http.StatusForbidden || !bytes.Contains(resp.Body.Bytes(), []byte(CodeUserCallerRefused)) {
				t.Fatalf("got %d %s want 403 %s", resp.Code, resp.Body.String(), CodeUserCallerRefused)
			}
		})
	}
	t.Run("service caller gets the map", func(t *testing.T) {
		resp := do(map[string]string{"X-Internal-Service-Key": permsInternalKey})
		if resp.Code != http.StatusOK {
			t.Fatalf("got %d %s", resp.Code, resp.Body.String())
		}
		var env struct {
			Data struct {
				UserID string            `json:"user_id"`
				Admin  permissions.Admin `json:"admin"`
			} `json:"data"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.Data.UserID != target.String() || !env.Data.Admin.Has("dating:reports.act") || env.Data.Admin.Has("food:reviews.moderate") {
			t.Fatalf("body %s", resp.Body.String())
		}
	})
	t.Run("lookup failure is 503, not a smaller map", func(t *testing.T) {
		svc.permissionsFn = func(uuid.UUID) (permissions.Admin, error) {
			return permissions.Admin{}, errors.New("db down")
		}
		if resp := do(map[string]string{"X-Internal-Service-Key": permsInternalKey}); resp.Code != http.StatusServiceUnavailable {
			t.Fatalf("got %d want 503", resp.Code)
		}
	})
}

func TestInternalPermissionsUnconfiguredKeyAdmitsNobody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(&stubAuthService{}, &config.Config{}, nil, nil).RegisterRoutes(r, noopMiddleware(), noopMiddleware())
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/internal/users/"+uuid.NewString()+"/permissions", nil)
	req.Header.Set("X-Internal-Service-Key", "")
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("got %d want 401", resp.Code)
	}
}

func TestGrantRoleHandler(t *testing.T) {
	actor, target := uuid.New(), uuid.New()
	var got service.RoleChangeRequest
	svc := &stubAuthService{}
	r := permissionsRouter(t, svc)

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/admin/roles", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-Id", actor.String())
		resp := httptest.NewRecorder()
		r.ServeHTTP(resp, req)
		return resp
	}

	svc.grantRoleFn = func(a uuid.UUID, req service.RoleChangeRequest) error {
		got = req
		return nil
	}
	resp := post(`{"user_id":"` + target.String() + `","role":"moderator","app":"dating",
		"expires_at":"2030-01-01T00:00:00Z","reason":"pilot"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("got %d %s", resp.Code, resp.Body.String())
	}
	if got.TargetID != target || got.App != "dating" || got.Reason != "pilot" || got.ExpiresAt == nil {
		t.Fatalf("service got %+v", got)
	}
	if !bytes.Contains(resp.Body.Bytes(), []byte(`"app":"dating"`)) {
		t.Fatalf("body %s", resp.Body.String())
	}

	for _, tc := range []struct {
		err  error
		code int
		tag  string
	}{
		{service.ErrSelfGrant, http.StatusForbidden, "SELF_GRANT_REFUSED"},
		{service.ErrReasonRequired, http.StatusBadRequest, "REASON_REQUIRED"},
		{service.ErrInvalidApp, http.StatusBadRequest, "INVALID_APP"},
		{service.ErrInvalidRole, http.StatusBadRequest, "BAD_REQUEST"},
		{service.ErrLastSuperadmin, http.StatusConflict, "LAST_SUPERADMIN"},
		{&service.EnvBootstrapRoleError{Role: "admin", EnvVar: "ADMIN_USER_IDS"}, http.StatusConflict, "ENV_BOOTSTRAP_ROLE"},
		{service.ErrNotSuperadmin, http.StatusForbidden, "FORBIDDEN"},
	} {
		err := tc.err
		svc.grantRoleFn = func(uuid.UUID, service.RoleChangeRequest) error { return err }
		resp := post(`{"user_id":"` + target.String() + `","role":"moderator","reason":"x"}`)
		if resp.Code != tc.code || !bytes.Contains(resp.Body.Bytes(), []byte(tc.tag)) {
			t.Errorf("%v: got %d %s want %d %s", tc.err, resp.Code, resp.Body.String(), tc.code, tc.tag)
		}
	}

	svc.grantRoleFn = nil
	if resp := post(`{"user_id":"` + target.String() + `","role":"moderator","expires_at":"tomorrow"}`); resp.Code != http.StatusBadRequest {
		t.Fatalf("malformed expires_at: got %d", resp.Code)
	}
}
