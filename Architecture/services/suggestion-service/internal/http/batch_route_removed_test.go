package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// SG-L1: GET /v1/suggestions/batch let any caller start a platform-wide
// recomputation (which then died with the request's context). The tickers
// run the batch; the route is gone.
func TestBatchTriggerRouteIsGone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	(&Handler{}).RegisterRoutes(r)
	req := httptest.NewRequest(http.MethodGet, "/v1/suggestions/batch", nil)
	req.Header.Set("X-User-Id", uuid.NewString())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET /v1/suggestions/batch: status %d, want 404", w.Code)
	}
}
