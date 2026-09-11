package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Plan 5B (issue M-13): POST /v1/reels/:reelId/view was a bare +1 with no
// dedup, no watch-time rule and no self-view exclusion — a view counter
// anyone could spin. It is gone; a view is a play_end (or a finalised
// session) on POST /v1/analytics/events, and the route says so.
func TestReelViewRouteIsGone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(nil, nil).RegisterReelEngagementRoutes(router)

	req := httptest.NewRequest(http.MethodPost, "/v1/reels/"+uuid.New().String()+"/view", strings.NewReader(`{"session_id":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", uuid.New().String())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusGone {
		t.Fatalf("status=%d want 410 Gone; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "/v1/analytics/events") {
		t.Fatalf("410 body does not name the replacement route: %s", w.Body.String())
	}
}
