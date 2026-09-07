package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/roles"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// meStubService is stubAuthService with a real GetUserContact, which /me needs
// before it will render anything.
type meStubService struct {
	stubAuthService
}

func (s *meStubService) GetUserContact(_ context.Context, uid uuid.UUID) (*store.User, error) {
	email := "someone@example.com"
	return &store.User{
		ID: uid, Email: &email, Phone: "+910000000000",
		EmailVerified: true, AccountStatus: "active",
	}, nil
}

func meRouter(t *testing.T, held []string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	svc := &meStubService{}
	svc.resolveRolesFn = func(uuid.UUID) []string { return held }
	h := New(svc, &config.Config{}, nil, nil)
	h.RegisterRoutes(r, noopMiddleware(), noopMiddleware())
	return r
}

func getAs(t *testing.T, r *gin.Engine, path string, uid uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if uid != uuid.Nil {
		req.Header.Set("X-User-Id", uid.String())
	}
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	return resp
}

// TestMeCarriesRoles — /v1/auth/me returned NO roles at all, so a client had to
// read them out of the token (and therefore could not see a role granted since
// that token was minted) or ask four other services. Identity is the single
// authority, so /me answers.
func TestMeCarriesRoles(t *testing.T) {
	uid := uuid.New()
	resp := getAs(t, meRouter(t, []string{"seller", "rider_partner"}), "/v1/auth/me", uid)
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
	if env.Data.UserID != uid.String() {
		t.Fatalf("user_id=%q want %q", env.Data.UserID, uid)
	}
	if want := []string{"seller", "rider_partner"}; !reflect.DeepEqual(env.Data.Roles, want) {
		t.Fatalf("roles=%v want %v", env.Data.Roles, want)
	}
}

// A roleless account must get [] rather than null — a client that has to
// distinguish "no roles" from "field missing" will get it wrong.
func TestMeRolesIsAlwaysAnArray(t *testing.T) {
	resp := getAs(t, meRouter(t, []string{}), "/v1/auth/me", uuid.New())
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	raw, ok := env.Data["roles"]
	if !ok {
		t.Fatal("/me is missing the roles field entirely")
	}
	if string(raw) != "[]" {
		t.Fatalf("roles = %s, want []", raw)
	}
}

// TestMeCapabilities — one request answers "which hats does this person wear".
func TestMeCapabilities(t *testing.T) {
	uid := uuid.New()
	resp := getAs(t, meRouter(t, []string{"moderator", "seller"}), "/v1/auth/me/capabilities", uid)
	if resp.Code != http.StatusOK {
		t.Fatalf("got %d want 200 (body %s)", resp.Code, resp.Body.String())
	}

	var env struct {
		Data struct {
			UserID       string          `json:"user_id"`
			Roles        []string        `json:"roles"`
			IsCustomer   bool            `json:"is_customer"`
			Capabilities map[string]bool `json:"capabilities"`
			Switcher     []struct {
				Role  string `json:"role"`
				Label string `json:"label"`
			} `json:"switcher"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v (body %s)", err, resp.Body.String())
	}
	if env.Data.UserID != uid.String() {
		t.Fatalf("user_id=%q want %q", env.Data.UserID, uid)
	}
	if !env.Data.IsCustomer {
		t.Fatal("is_customer must be true — every account is a customer")
	}
	if len(env.Data.Capabilities) != len(roles.All()) {
		t.Fatalf("capabilities=%v want one entry per role", env.Data.Capabilities)
	}
	if _, ok := env.Data.Capabilities["customer"]; ok {
		t.Fatal("`customer` must not appear as a capability key — it is not a role")
	}
	if !env.Data.Capabilities["seller"] || !env.Data.Capabilities["moderator"] {
		t.Fatalf("capabilities=%v", env.Data.Capabilities)
	}
	if env.Data.Capabilities["admin"] || env.Data.Capabilities["restaurant_owner"] {
		t.Fatalf("capabilities=%v — unheld roles must be explicitly false", env.Data.Capabilities)
	}
	if len(env.Data.Switcher) != 3 || env.Data.Switcher[0].Role != "customer" {
		t.Fatalf("switcher=%v want the customer row plus the two roles held", env.Data.Switcher)
	}
	for _, s := range env.Data.Switcher {
		if s.Label == "" {
			t.Fatalf("switcher row %q has no label — a blank row is unrenderable", s.Role)
		}
	}
}

func TestMeCapabilitiesRequiresIdentity(t *testing.T) {
	resp := getAs(t, meRouter(t, nil), "/v1/auth/me/capabilities", uuid.Nil)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("got %d want 401", resp.Code)
	}
}
