package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-user-service/internal/service"
	"github.com/atpost/identity-user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func modulesRouter(t *testing.T, svc *stubUserService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(svc, nil)
	h.RegisterRoutes(r, func(c *gin.Context) { c.Next() }, func(c *gin.Context) { c.Next() })
	return r
}

func doJSON(t *testing.T, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-User-Id", uuid.New().String())
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	return resp
}

func TestGetMyModulesReturnsServiceShape(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	svc := &stubUserService{
		getModulesFn: func(id uuid.UUID) (*store.ModulePreferences, error) {
			return &store.ModulePreferences{
				UserID:     id,
				Modules:    []string{"reels", "chat"},
				HomeModule: "reels",
				UpdatedAt:  now,
			}, nil
		},
	}
	resp := doJSON(t, modulesRouter(t, svc), http.MethodGet, "/v1/users/me/modules", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", resp.Code, resp.Body.String())
	}
	var envelope struct {
		Data struct {
			Modules               []string   `json:"modules"`
			HomeModule            string     `json:"home_module"`
			OnboardingCompletedAt *time.Time `json:"onboarding_completed_at"`
			UpdatedAt             time.Time  `json:"updated_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal: %v — body: %s", err, resp.Body.String())
	}
	if len(envelope.Data.Modules) != 2 || envelope.Data.HomeModule != "reels" {
		t.Errorf("unexpected data: %+v", envelope.Data)
	}
	if envelope.Data.OnboardingCompletedAt != nil {
		t.Errorf("onboarding_completed_at should serialise as null, got %v", envelope.Data.OnboardingCompletedAt)
	}
	// The contract does not include user_id.
	if strings.Contains(resp.Body.String(), "user_id") {
		t.Errorf("user_id leaked into the modules response: %s", resp.Body.String())
	}
}

func TestUpdateMyModulesErrorMapping(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		svcErr   error
		wantCode string
	}{
		{"unknown module", `{"modules":["banana"]}`, service.ErrInvalidModule, "INVALID_MODULE"},
		{"bad home module", `{"modules":["reels"],"home_module":"chat"}`, service.ErrInvalidHomeModule, "INVALID_HOME_MODULE"},
		{"modules omitted entirely", `{"home_module":"feed"}`, nil, "INVALID_REQUEST"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubUserService{
				updateModulesFn: func(uuid.UUID, []string, string, bool) (*store.ModulePreferences, error) {
					return nil, tc.svcErr
				},
			}
			resp := doJSON(t, modulesRouter(t, svc), http.MethodPut, "/v1/users/me/modules", tc.body)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", resp.Code, resp.Body.String())
			}
			if !strings.Contains(resp.Body.String(), tc.wantCode) {
				t.Errorf("body %s does not carry code %s", resp.Body.String(), tc.wantCode)
			}
		})
	}
}

func TestUpdateMyModulesForwardsCompleteOnboarding(t *testing.T) {
	var gotModules []string
	var gotHome string
	var gotComplete bool
	svc := &stubUserService{
		updateModulesFn: func(_ uuid.UUID, modules []string, home string, complete bool) (*store.ModulePreferences, error) {
			gotModules, gotHome, gotComplete = modules, home, complete
			now := time.Now().UTC()
			return &store.ModulePreferences{Modules: modules, HomeModule: home, OnboardingCompletedAt: &now, UpdatedAt: now}, nil
		},
	}
	body := `{"modules":["reels","chat"],"home_module":"reels","complete_onboarding":true}`
	resp := doJSON(t, modulesRouter(t, svc), http.MethodPut, "/v1/users/me/modules", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", resp.Code, resp.Body.String())
	}
	if len(gotModules) != 2 || gotHome != "reels" || !gotComplete {
		t.Errorf("service saw modules=%v home=%q complete=%v", gotModules, gotHome, gotComplete)
	}
	if !strings.Contains(resp.Body.String(), "onboarding_completed_at") {
		t.Errorf("response missing onboarding_completed_at: %s", resp.Body.String())
	}
}

func TestUpdateMyRegionMapping(t *testing.T) {
	t.Run("invalid region maps to 400 INVALID_REGION", func(t *testing.T) {
		svc := &stubUserService{
			setRegionFn: func(uuid.UUID, string) (string, error) { return "", service.ErrInvalidRegion },
		}
		resp := doJSON(t, modulesRouter(t, svc), http.MethodPut, "/v1/users/me/region", `{"country_code":"XYZ"}`)
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("status %d, want 400: %s", resp.Code, resp.Body.String())
		}
		if !strings.Contains(resp.Body.String(), "INVALID_REGION") {
			t.Errorf("body %s does not carry INVALID_REGION", resp.Body.String())
		}
	})
	t.Run("valid region echoes the normalised value", func(t *testing.T) {
		svc := &stubUserService{
			setRegionFn: func(_ uuid.UUID, code string) (string, error) { return "IN", nil },
		}
		resp := doJSON(t, modulesRouter(t, svc), http.MethodPut, "/v1/users/me/region", `{"country_code":"in"}`)
		if resp.Code != http.StatusOK {
			t.Fatalf("status %d, want 200: %s", resp.Code, resp.Body.String())
		}
		if !strings.Contains(resp.Body.String(), `"region":"IN"`) {
			t.Errorf("body %s does not carry region IN", resp.Body.String())
		}
	})
}

// Module 3 — the handler must no longer refuse "private". Enforcement moved
// to the service validator ({public, private}) and to graph-service, which
// consumes the settings-changed event carrying the new value.
func TestPrivateAccountIsAcceptedByTheHandler(t *testing.T) {
	var persisted *store.UserSettings
	svc := &stubUserService{
		getSettingsFn: func(uuid.UUID) (*store.UserSettings, error) {
			return &store.UserSettings{AccountVisibility: "public"}, nil
		},
		updateFn: func(s *store.UserSettings) (*store.UserSettings, error) {
			cp := *s
			persisted = &cp
			return s, nil
		},
	}
	resp := doJSON(t, modulesRouter(t, svc), http.MethodPut, "/v1/users/me/settings", `{"account_visibility":"private"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 — the handler still refuses private accounts: %s", resp.Code, resp.Body.String())
	}
	if persisted == nil || persisted.AccountVisibility != "private" {
		t.Fatalf("persisted = %+v, want account_visibility=private", persisted)
	}
}

// An out-of-range visibility is still rejected — by the service validator,
// mapped to 400 by the handler.
func TestInvalidVisibilityStillMapsTo400(t *testing.T) {
	svc := &stubUserService{
		getSettingsFn: func(uuid.UUID) (*store.UserSettings, error) {
			return &store.UserSettings{AccountVisibility: "public"}, nil
		},
		updateFn: func(*store.UserSettings) (*store.UserSettings, error) {
			return nil, service.ErrInvalidPrivacySetting
		},
	}
	resp := doJSON(t, modulesRouter(t, svc), http.MethodPut, "/v1/users/me/settings", `{"account_visibility":"followers"}`)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", resp.Code, resp.Body.String())
	}
}
