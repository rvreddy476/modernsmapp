package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/rider-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// rider-service authorises on X-User-Id and X-Scopes, which only the gateway
// may set. Without X-Internal-Service-Key on every /v1/rider route, anything
// that can reach the container can be any rider, partner or admin.

const testInternalKey = "test-internal-key"

// newKeyedRouter builds the real route table over a service with no database.
// Every request below is answered by a middleware or by a handler's identity
// check before any store call, and gin.Recovery turns a store call that
// should not have happened into a 500 instead of a crashed test binary.
func newKeyedRouter(key string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/healthz", func(c *gin.Context) { c.Status(http.StatusOK) })
	New(service.New(store.New(nil), nil, service.Config{}), key).RegisterRoutes(r)
	return r
}

type keyProbe struct {
	method, path string
	headers      map[string]string
}

// forgedProbes are requests that assert an identity. The admin one carries a
// forged admin scope: it is the request the key exists to stop.
func forgedProbes() []keyProbe {
	uid := uuid.NewString()
	return []keyProbe{
		{http.MethodGet, "/v1/rider/cities", nil},
		{http.MethodGet, "/v1/rider/rides/me", map[string]string{"X-User-Id": uid}},
		{http.MethodPost, "/v1/rider/partners/me/aadhaar/start", map[string]string{"X-User-Id": uid}},
		{http.MethodGet, "/v1/rider/admin/dashboard", map[string]string{"X-User-Id": uid, "X-Scopes": "admin"}},
		{http.MethodPost, "/v1/rider/admin/partners/" + uid + "/approve", map[string]string{"X-User-Id": uid, "X-Scopes": "admin"}},
	}
}

func serve(r *gin.Engine, p keyProbe, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(p.method, p.path, nil)
	for k, v := range p.headers {
		req.Header.Set(k, v)
	}
	if key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	return res
}

func TestRiderRoutesRejectMissingInternalKey(t *testing.T) {
	r := newKeyedRouter(testInternalKey)
	for _, p := range forgedProbes() {
		res := serve(r, p, "")
		if res.Code != http.StatusUnauthorized || !strings.Contains(res.Body.String(), `"UNAUTHORIZED"`) {
			t.Errorf("%s %s without key: status %d body %s, want 401 UNAUTHORIZED",
				p.method, p.path, res.Code, res.Body.String())
		}
	}
}

func TestRiderRoutesRejectWrongInternalKey(t *testing.T) {
	r := newKeyedRouter(testInternalKey)
	for _, p := range forgedProbes() {
		res := serve(r, p, "not-the-key")
		if res.Code != http.StatusUnauthorized || !strings.Contains(res.Body.String(), `"UNAUTHORIZED"`) {
			t.Errorf("%s %s with wrong key: status %d body %s, want 401 UNAUTHORIZED",
				p.method, p.path, res.Code, res.Body.String())
		}
	}
}

// With the key, a request gets past the key check and is judged by the next
// guard. No X-User-Id is sent, so that guard answers AUTH_REQUIRED, which is
// only reachable once the internal-key middleware has let the request through.
func TestRiderRoutesAdmitCorrectInternalKey(t *testing.T) {
	r := newKeyedRouter(testInternalKey)
	for _, p := range []keyProbe{
		{http.MethodGet, "/v1/rider/rides/me", nil},
		{http.MethodGet, "/v1/rider/admin/dashboard", nil},
	} {
		res := serve(r, p, testInternalKey)
		if res.Code != http.StatusUnauthorized || !strings.Contains(res.Body.String(), "AUTH_REQUIRED") {
			t.Errorf("%s %s with key: status %d body %s, want the identity guard's AUTH_REQUIRED",
				p.method, p.path, res.Code, res.Body.String())
		}
	}
}

func TestProbesAreNotBehindInternalKey(t *testing.T) {
	res := serve(newKeyedRouter(testInternalKey), keyProbe{http.MethodGet, "/healthz", nil}, "")
	if res.Code != http.StatusOK {
		t.Fatalf("/healthz without key: status %d, want 200", res.Code)
	}
}

// Outside production an unset key leaves the routes open, as food-service
// does, so a bare local run still works.
func TestRiderRoutesOpenWhenNoKeyConfigured(t *testing.T) {
	res := serve(newKeyedRouter(""), keyProbe{http.MethodGet, "/v1/rider/admin/dashboard", nil}, "")
	if !strings.Contains(res.Body.String(), "AUTH_REQUIRED") {
		t.Fatalf("no key configured: status %d body %s, want AUTH_REQUIRED", res.Code, res.Body.String())
	}
}

func TestCheckInternalKey(t *testing.T) {
	for _, tc := range []struct {
		production bool
		key        string
		wantErr    bool
	}{
		{true, "", true},
		{true, "   ", true},
		{true, "k", false},
		{false, "", false},
		{false, "k", false},
	} {
		err := CheckInternalKey(tc.production, tc.key)
		if (err != nil) != tc.wantErr {
			t.Errorf("CheckInternalKey(production=%v, key=%q) = %v, wantErr %v", tc.production, tc.key, err, tc.wantErr)
		}
	}
}
