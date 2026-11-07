package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// Audit 2026-11-07, S-H1 / S-L1: routes that must not exist.
//
// POST /v1/search/users/bulk-sync let anyone, signed out included, write
// any document into users_v1, and its id was spliced into the bulk body
// raw. GET /v1/search/events and /messages searched indices nothing writes.
// The gateway stamps the internal key on /v1/search, so the engine-wide
// key check never stopped any of them.
var removedRoutes = []struct{ method, path string }{
	{http.MethodPost, "/v1/search/users/bulk-sync"},
	{http.MethodGet, "/v1/search/events"},
	{http.MethodGet, "/v1/search/messages"},
}

func registeredEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(nil, nil).WithInternalKey("test-internal-key").RegisterRoutes(r)
	return r
}

func TestRemovedRoutesAreNotRegistered(t *testing.T) {
	r := registeredEngine(t)
	registered := map[string]bool{}
	for _, rt := range r.Routes() {
		registered[rt.Method+" "+rt.Path] = true
		if strings.Contains(rt.Path, "bulk-sync") {
			t.Fatalf("%s %s is registered", rt.Method, rt.Path)
		}
	}
	for _, rt := range removedRoutes {
		if registered[rt.method+" "+rt.path] {
			t.Fatalf("%s %s is registered again", rt.method, rt.path)
		}
	}
	// Not vacuous: the engine does carry the search routes.
	for _, want := range []string{"GET /v1/search", "POST /v1/search/internal/reindex/users"} {
		if !registered[want] {
			t.Fatalf("%s missing; this test is not looking at the real route table", want)
		}
	}
}

// Edge-shaped: the internal key the gateway stamps on every /v1/search
// request, no identity, a bulk body. The answer is 404.
func TestBulkSync_EdgeShapedRequestIsNotFound(t *testing.T) {
	r := registeredEngine(t)
	for _, rt := range removedRoutes {
		req := httptest.NewRequest(rt.method, rt.path+"?q=x",
			strings.NewReader(`{"users":[{"user_id":"x\"}}\n{\"delete\":{\"_index\":\"author_fences_v1\",\"_id\":\"v\"}}"}]}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Service-Key", "test-internal-key")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s %s answered %d, want 404", rt.method, rt.path, w.Code)
		}
	}
}
