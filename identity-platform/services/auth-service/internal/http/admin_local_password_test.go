package http

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestLocalAdminLoginUsesCookiesWithoutClaimingMFA(t *testing.T) {
	resp := verifiedAdmin(uuid.New())
	resp.LocalAdminPasswordLogin = true
	svc := &stubAuthService{admin: adminSessionStub{loginFn: func(string, string) (*service.AdminLoginChallenge, error) {
		return &service.AdminLoginChallenge{LocalSession: resp}, nil
	}}}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	key := strings.Repeat("k", 32)
	h := New(svc, &config.Config{RuntimeEnvironment: "local", AdminLocalPasswordLoginKey: key}, nil, nil)
	r.POST("/login", h.AdminSessionLogin)
	w := adminDo(r, http.MethodPost, "/login", `{"identifier":"admin@example.test","password":"password"}`, withHeader("X-Admin-Local-Password-Key", key))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	assertAdminCookieSet(t, w, false)
	// Explicit JSON names, because underscore names are not Go field aliases.
	var decoded map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	data, ok := decoded["data"].(map[string]any)
	if !ok || data["admin_mfa"] != false || data["requires_2fa"] != false || data["local_password_login"] != true {
		t.Fatalf("body %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "admin-access-value") || strings.Contains(w.Body.String(), "admin-refresh-value") {
		t.Fatal("token leaked in JSON")
	}
	raw, err := json.Marshal(&service.AdminLoginChallenge{LocalSession: resp})
	if err != nil || strings.Contains(string(raw), "admin-access-value") {
		t.Fatal("challenge serializes tokens")
	}
}

func TestLocalAdminLoginRefusesUnkeyedAndProductionResponses(t *testing.T) {
	key := strings.Repeat("k", 32)
	for _, tc := range []struct {
		name, key  string
		production bool
	}{
		{"missing key", "", false}, {"wrong key", strings.Repeat("x", 32), false}, {"production", key, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := verifiedAdmin(uuid.New())
			resp.LocalAdminPasswordLogin = true
			svc := &stubAuthService{admin: adminSessionStub{loginFn: func(string, string) (*service.AdminLoginChallenge, error) {
				return &service.AdminLoginChallenge{LocalSession: resp}, nil
			}}}
			gin.SetMode(gin.TestMode)
			r := gin.New()
			h := New(svc, &config.Config{RuntimeEnvironment: "local", AdminLocalPasswordLoginKey: key, Production: tc.production}, nil, nil)
			r.POST("/login", h.AdminSessionLogin)
			w := adminDo(r, http.MethodPost, "/login", `{"identifier":"admin@example.test","password":"password"}`, withHeader("X-Admin-Local-Password-Key", tc.key))
			if w.Code != http.StatusForbidden || len(setCookies(w)) != 0 {
				t.Fatalf("unsafe local login: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
