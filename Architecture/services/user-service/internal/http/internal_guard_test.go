package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// internalGuardRouter drives the REAL route table. The handler has no store
// or service, so a request that got past the guard would panic the test —
// a clean 503/401 is also proof the handler never ran.
func internalGuardRouter(key string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := &Handler{pageAdmins: map[string]bool{}}
	h.WithInternalRoutes(key)
	r := gin.New()
	h.RegisterRoutes(r)
	return r
}

var internalGuardRoutes = []struct{ method, path string }{
	{http.MethodPost, "/internal/users/11111111-1111-1111-1111-111111111111/ensure"},
	{http.MethodGet, "/internal/dlq"},
	{http.MethodPost, "/internal/dlq/1/replay"},
	{http.MethodGet, "/internal/projection/health"},
	{http.MethodGet, "/internal/channels/11111111-1111-1111-1111-111111111111/subscriber-ids"},
}

func serveInternal(r *gin.Engine, method, path, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if key != "" {
		req.Header.Set(internalServiceKeyHeader, key)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestInternalRoutesFailClosedWithoutAConfiguredKey(t *testing.T) {
	r := internalGuardRouter("")
	for _, rt := range internalGuardRoutes {
		for _, presented := range []string{"", "anything"} {
			w := serveInternal(r, rt.method, rt.path, presented)
			if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), CodeInternalKeyNotConfigured) {
				t.Errorf("%s %s (presented %q): %d %s, want 503 %s",
					rt.method, rt.path, presented, w.Code, w.Body.String(), CodeInternalKeyNotConfigured)
			}
		}
	}
}

func TestInternalRoutesRefuseAWrongOrMissingKey(t *testing.T) {
	r := internalGuardRouter("right-key")
	for _, rt := range internalGuardRoutes {
		for _, presented := range []string{"", "wrong-key", "right-ke", "right-key-"} {
			if w := serveInternal(r, rt.method, rt.path, presented); w.Code != http.StatusUnauthorized {
				t.Errorf("%s %s (presented %q): %d %s, want 401", rt.method, rt.path, presented, w.Code, w.Body.String())
			}
		}
	}
}

func TestInternalRouteGuardAdmitsTheRightKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/internal")
	g.Use(requireInternalRouteKey("right-key"))
	g.GET("/ping", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	if w := serveInternal(r, http.MethodGet, "/internal/ping", "right-key"); w.Code != http.StatusNoContent {
		t.Fatalf("right key: %d, want 204", w.Code)
	}
}
