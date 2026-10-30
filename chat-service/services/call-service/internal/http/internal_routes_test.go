package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/chat-call-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const testInternalKey = "test-internal-key"

func internalRouter(key string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(nil, nil).WithCallsEnabled(true).WithInternalServiceKey(key).RegisterRoutes(r)
	return r
}

func serve(r http.Handler, method, path string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(`{"summary_text":"forged"}`))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// Audit H1: the user-facing POST /v1/calls/:id/summary is gone. A signed-in
// user (the auth middleware has already set a user id) gets 404, never the
// handler.
func TestUserFacingSummaryWriteIsGone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(userIDKey, uuid.NewString()); c.Next() })
	New(nil, nil).WithCallsEnabled(true).WithInternalServiceKey(testInternalKey).RegisterRoutes(r)

	rec := serve(r, http.MethodPost, "/v1/calls/"+uuid.NewString()+"/summary", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("JWT-only summary POST: status %d, want 404", rec.Code)
	}
}

func TestInternalSummaryRouteRequiresInternalKey(t *testing.T) {
	path := "/internal/v1/calls/" + uuid.NewString() + "/summary"
	cases := []struct {
		name       string
		configured string
		header     map[string]string
	}{
		{"no key", testInternalKey, nil},
		{"wrong key", testInternalKey, map[string]string{internalServiceKeyHeader: "nope"}},
		{"bearer only", testInternalKey, map[string]string{"Authorization": "Bearer user-token"}},
		{"unconfigured key", "", map[string]string{internalServiceKeyHeader: ""}},
		{"unconfigured key, any value", "", map[string]string{internalServiceKeyHeader: testInternalKey}},
	}
	for _, tc := range cases {
		rec := serve(internalRouter(tc.configured), http.MethodPost, path, tc.header)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", tc.name, rec.Code)
		}
	}
}

// With the key the request reaches CreateCallSummary (a malformed call id
// is the handler's own 400, answered before any service call).
func TestInternalSummaryRouteAdmitsInternalKey(t *testing.T) {
	rec := serve(internalRouter(testInternalKey), http.MethodPost, "/internal/v1/calls/not-a-uuid/summary",
		map[string]string{internalServiceKeyHeader: testInternalKey})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "INVALID_PARAM") {
		t.Fatalf("keyed internal POST: status %d body %s, want the handler's 400", rec.Code, rec.Body.String())
	}
}

// The engine-wide JWT middleware lets a keyed request through only on the
// internal prefix; on user routes the key is no substitute for a JWT.
func TestAuthMiddlewareInternalKeyBypassIsPrefixScoped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddlewareWithKeys(JWTKeySet{ActiveSecret: "jwt-secret", InternalServiceKey: testInternalKey}, nil))
	reached := func(c *gin.Context) { c.Status(http.StatusTeapot) }
	r.POST("/internal/v1/calls/:id/summary", reached)
	r.GET("/v1/calls/history", reached)

	keyed := map[string]string{internalServiceKeyHeader: testInternalKey}
	if rec := serve(r, http.MethodPost, "/internal/v1/calls/x/summary", keyed); rec.Code != http.StatusTeapot {
		t.Fatalf("keyed internal request: status %d, want it admitted", rec.Code)
	}
	if rec := serve(r, http.MethodPost, "/internal/v1/calls/x/summary", map[string]string{internalServiceKeyHeader: "wrong"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key on internal route: status %d, want 401", rec.Code)
	}
	if rec := serve(r, http.MethodGet, "/v1/calls/history", keyed); rec.Code != http.StatusUnauthorized {
		t.Fatalf("internal key on a user route: status %d, want 401 (no JWT)", rec.Code)
	}
}

// Audit L3: a lobby request is refused with a 400 the client can act on.
func TestGenerateCallLinkLobbyIsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(userIDKey, uuid.NewString()); c.Next() })
	svc := service.New(nil, nil, nil, nil, nil, nil, 30)
	New(svc, nil).WithCallsEnabled(true).RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodPost, "/v1/calls/link",
		strings.NewReader(`{"call_session_id":"`+uuid.NewString()+`","lobby_enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusBadRequest || body.Error.Code != "LOBBY_NOT_SUPPORTED" {
		t.Fatalf("lobby link: status %d code %q, want 400 LOBBY_NOT_SUPPORTED", rec.Code, body.Error.Code)
	}
}
