package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/roles"
	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const testInternalKey = "test-internal-key"

func internalRouter(t *testing.T, svc *stubAuthService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(svc, &config.Config{InternalServiceKey: testInternalKey}, nil, nil)
	h.RegisterRoutes(r, noopMiddleware(), noopMiddleware())
	return r
}

func internalRoleCall(t *testing.T, r *gin.Engine, method, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/v1/auth/internal/roles", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	return resp
}

func grantBody(userID, role string) string {
	return `{"user_id":"` + userID + `","role":"` + role + `","service":"commerce-service"}`
}

// TestInternalRolesRequireServiceKey — the route is only usable by something
// that already holds INTERNAL_SERVICE_KEY. The api-gateway strips
// X-Internal-Service-Key from every inbound request and injects its own, so a
// browser cannot supply it.
func TestInternalRolesRequireServiceKey(t *testing.T) {
	r := internalRouter(t, &stubAuthService{})
	target := uuid.New().String()

	for _, tc := range []struct{ name, key string }{
		{"no key", ""},
		{"wrong key", "guessed-key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, method := range []string{http.MethodPost, http.MethodDelete} {
				resp := internalRoleCall(t, r, method, grantBody(target, "seller"), tc.key)
				if resp.Code != http.StatusUnauthorized {
					t.Fatalf("%s: got %d want %d", method, resp.Code, http.StatusUnauthorized)
				}
			}
		})
	}
}

// TestInternalGrantRefusesPlatformRoles is the security assertion this
// endpoint exists to keep true: a service may never mint an admin.
//
// The refusal is enforced in the service layer, so the handler is checked for
// the thing the caller sees — a 403 with a machine-readable code and the list
// of roles it IS allowed to grant.
func TestInternalGrantRefusesPlatformRoles(t *testing.T) {
	target := uuid.New().String()

	for _, role := range []string{"superadmin", "admin", "moderator"} {
		called := false
		svc := &stubAuthService{
			grantEcosystemFn: func(_ string, _ uuid.UUID, gotRole, _ string) error {
				called = true
				// Mirror the real service layer.
				if !roles.IsEcosystem(gotRole) {
					return service.ErrRoleNotGrantableByService
				}
				return nil
			},
		}
		r := internalRouter(t, svc)
		resp := internalRoleCall(t, r, http.MethodPost, grantBody(target, role), testInternalKey)

		if resp.Code != http.StatusForbidden {
			t.Fatalf("grant %q: got %d want %d (body %s)", role, resp.Code, http.StatusForbidden, resp.Body.String())
		}
		if !called {
			t.Fatalf("grant %q never reached the service layer — the boundary must live there, "+
				"not only in the handler", role)
		}
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Details struct {
					GrantableRoles []string `json:"grantable_roles"`
				} `json:"details"`
			} `json:"error"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode: %v (body %s)", err, resp.Body.String())
		}
		if env.Error.Code != "ROLE_NOT_GRANTABLE" {
			t.Fatalf("grant %q: code=%q want ROLE_NOT_GRANTABLE", role, env.Error.Code)
		}
		if len(env.Error.Details.GrantableRoles) != len(roles.Ecosystem()) {
			t.Fatalf("grant %q: grantable_roles=%v want the four ecosystem roles",
				role, env.Error.Details.GrantableRoles)
		}
	}
}

// TestInternalRevokeRefusesPlatformRoles — a service must not be able to strip
// a superadmin either.
func TestInternalRevokeRefusesPlatformRoles(t *testing.T) {
	target := uuid.New().String()
	svc := &stubAuthService{
		revokeEcosystemFn: func(_ string, _ uuid.UUID, gotRole, _ string) error {
			if !roles.IsEcosystem(gotRole) {
				return service.ErrRoleNotGrantableByService
			}
			return nil
		},
	}
	r := internalRouter(t, svc)
	resp := internalRoleCall(t, r, http.MethodDelete, grantBody(target, "superadmin"), testInternalKey)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("got %d want %d (body %s)", resp.Code, http.StatusForbidden, resp.Body.String())
	}
}

// TestInternalGrantForwardsContract pins the request shape the next stream
// codes against and proves every field reaches the service layer.
func TestInternalGrantForwardsContract(t *testing.T) {
	target := uuid.New()
	var gotService, gotRole, gotReason string
	var gotTarget uuid.UUID

	svc := &stubAuthService{
		grantEcosystemFn: func(callingService string, tgt uuid.UUID, role, reason string) error {
			gotService, gotTarget, gotRole, gotReason = callingService, tgt, role, reason
			return nil
		},
	}
	r := internalRouter(t, svc)

	body := `{"user_id":"` + target.String() + `","role":"seller",` +
		`"service":"commerce-service","reason":"seller application SA-1042 approved"}`
	resp := internalRoleCall(t, r, http.MethodPost, body, testInternalKey)

	if resp.Code != http.StatusOK {
		t.Fatalf("got %d want 200 (body %s)", resp.Code, resp.Body.String())
	}
	if gotService != "commerce-service" || gotTarget != target ||
		gotRole != "seller" || gotReason != "seller application SA-1042 approved" {
		t.Fatalf("forwarded service=%q target=%s role=%q reason=%q",
			gotService, gotTarget, gotRole, gotReason)
	}

	var env struct {
		Data struct {
			Status string `json:"status"`
			UserID string `json:"user_id"`
			Role   string `json:"role"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v (body %s)", err, resp.Body.String())
	}
	if env.Data.Status != "granted" || env.Data.UserID != target.String() || env.Data.Role != "seller" {
		t.Fatalf("response = %+v (body %s)", env.Data, resp.Body.String())
	}
}

// TestInternalGrantIsIdempotentOverHTTP — granting twice is 200 twice. An
// approval flow that retries must not see a failure.
func TestInternalGrantIsIdempotentOverHTTP(t *testing.T) {
	target := uuid.New().String()
	r := internalRouter(t, &stubAuthService{})

	for i := 0; i < 2; i++ {
		resp := internalRoleCall(t, r, http.MethodPost, grantBody(target, "delivery_partner"), testInternalKey)
		if resp.Code != http.StatusOK {
			t.Fatalf("grant #%d: got %d want 200 (body %s)", i+1, resp.Code, resp.Body.String())
		}
	}
}

func TestInternalRevokeOverHTTP(t *testing.T) {
	target := uuid.New()
	var gotRole, gotReason string
	svc := &stubAuthService{
		revokeEcosystemFn: func(_ string, _ uuid.UUID, role, reason string) error {
			gotRole, gotReason = role, reason
			return nil
		},
	}
	r := internalRouter(t, svc)

	body := `{"user_id":"` + target.String() + `","role":"rider_partner",` +
		`"service":"rider-service","reason":"partner removed"}`
	resp := internalRoleCall(t, r, http.MethodDelete, body, testInternalKey)

	if resp.Code != http.StatusOK {
		t.Fatalf("got %d want 200 (body %s)", resp.Code, resp.Body.String())
	}
	if gotRole != "rider_partner" || gotReason != "partner removed" {
		t.Fatalf("forwarded role=%q reason=%q", gotRole, gotReason)
	}
	// Revoking again is still 200.
	if resp := internalRoleCall(t, r, http.MethodDelete, body, testInternalKey); resp.Code != http.StatusOK {
		t.Fatalf("second revoke: got %d want 200", resp.Code)
	}
}

func TestInternalRoleRejectsBadBody(t *testing.T) {
	r := internalRouter(t, &stubAuthService{})
	for _, body := range []string{
		`{}`,
		`{"role":"seller","service":"commerce-service"}`,                 // no user_id
		`{"user_id":"not-a-uuid","role":"seller","service":"commerce"}`,  // bad user_id
		`{"user_id":"` + uuid.New().String() + `","role":"seller"}`,      // no service
		`{"user_id":"` + uuid.New().String() + `","service":"commerce"}`, // no role
	} {
		resp := internalRoleCall(t, r, http.MethodPost, body, testInternalKey)
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("body %s: got %d want 400", body, resp.Code)
		}
	}
}

// TestInternalListRoles — the read a backfill/reconciliation pass needs.
func TestInternalListRoles(t *testing.T) {
	target := uuid.New()
	svc := &stubAuthService{
		resolveRolesFn: func(uuid.UUID) []string { return []string{"seller"} },
	}
	r := internalRouter(t, svc)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/internal/roles/"+target.String(), nil)
	req.Header.Set("X-Internal-Service-Key", testInternalKey)
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("got %d want 200 (body %s)", resp.Code, resp.Body.String())
	}
	var env struct {
		Data struct {
			UserID string   `json:"user_id"`
			Roles  []string `json:"roles"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v (body %s)", err, resp.Body.String())
	}
	if env.Data.UserID != target.String() || len(env.Data.Roles) != 1 || env.Data.Roles[0] != "seller" {
		t.Fatalf("response = %+v", env.Data)
	}

	// Unauthenticated is refused like the write routes.
	req = httptest.NewRequest(http.MethodGet, "/v1/auth/internal/roles/"+target.String(), nil)
	resp = httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list: got %d want 401", resp.Code)
	}
}
