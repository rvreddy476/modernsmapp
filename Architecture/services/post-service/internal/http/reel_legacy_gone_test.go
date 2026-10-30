package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Audit MEDIUM 6 and 7 (2026-10-30): the legacy per-reel engagement routes
// and the legacy reel feed are retired. Each answers 410 ROUTE_RETIRED with
// its replacement, before reading or writing anything: the handler is
// built with no service at all (New(nil, nil)), so a route that still
// reached its old handler would panic instead.
func TestLegacyReelRoutesAreGone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := New(nil, nil)
	h.RegisterReelEngagementRoutes(router)
	h.RegisterReelFeedRoutes(router)
	reel := uuid.New().String()

	for _, c := range []struct {
		method, path, body, replacement string
	}{
		{http.MethodPost, "/v1/reels/" + reel + "/react", `{"reaction":"like"}`, "POST /v1/posts/{postId}/react"},
		{http.MethodDelete, "/v1/reels/" + reel + "/react", "", "POST /v1/posts/{postId}/react"},
		{http.MethodGet, "/v1/reels/" + reel + "/react/me", "", "GET /v1/posts/{postId} (viewer_reaction)"},
		{http.MethodPost, "/v1/reels/" + reel + "/comments", `{"text":"hi"}`, "POST /v1/posts/{postId}/comments"},
		{http.MethodGet, "/v1/reels/" + reel + "/comments", "", "GET /v1/posts/{postId}/comments"},
		{http.MethodPost, "/v1/reels/" + reel + "/share", `{}`, "POST /v1/posts/{postId}/share"},
		{http.MethodPost, "/v1/reels/" + reel + "/save", "", "POST /v1/posts/{postId}/bookmark"},
		{http.MethodDelete, "/v1/reels/" + reel + "/save", "", "DELETE /v1/posts/{postId}/bookmark"},
		{http.MethodGet, "/v1/reels/" + reel + "/saved", "", "GET /v1/posts/{postId} (viewer state)"},
		{http.MethodGet, "/v1/reels/" + reel + "/counts", "", "GET /v1/posts/{postId} (counts)"},
		{http.MethodPost, "/v1/reels/" + reel + "/view", `{}`, "POST /v1/analytics/events"},
		{http.MethodGet, "/v1/reels/feed?limit=20", "", "GET /v1/feed/reels"},
		{http.MethodPost, "/v1/reels/feed/refresh", "", "GET /v1/feed/reels"},
	} {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-Id", uuid.New().String())
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusGone {
			t.Errorf("%s %s: status %d, want 410; body=%s", c.method, c.path, w.Code, w.Body.String())
			continue
		}
		if !strings.Contains(w.Body.String(), "ROUTE_RETIRED") || !strings.Contains(w.Body.String(), c.replacement) {
			t.Errorf("%s %s: body does not name ROUTE_RETIRED and %q: %s", c.method, c.path, c.replacement, w.Body.String())
		}
	}
}

// The routes that stay are not swallowed by the retirement: the owner's
// liked list (Android's Liked tab) still reaches its handler.
func TestLikedReelsRouteIsKept(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(nil, nil).RegisterReelEngagementRoutes(router)
	req := httptest.NewRequest(http.MethodGet, "/v1/reels/liked", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code == http.StatusGone || w.Code == http.StatusNotFound {
		t.Fatalf("GET /v1/reels/liked: %d; it must stay", w.Code)
	}
}

// The retired per-reel routes used to run the post detail's read gate
// (read_gate_routes_test.go). Retired, they answer 410 to everyone without
// reading the post at all: not the gate, not a store, and a refusal the
// gate would have given cannot be told apart from an allowed post.
func TestRetiredReelRoutesNeverReachThePost(t *testing.T) {
	post, viewer := uuid.New(), uuid.New()
	for _, refuse := range []bool{true, false} {
		g := newGateRig(t, func(uuid.UUID, *uuid.UUID) error {
			if refuse {
				return service.ErrPostNotVisible
			}
			return nil
		}, nil)
		for _, rt := range retiredReelRoutes {
			path := "/v1/reels/" + strings.ReplaceAll(strings.TrimPrefix(rt.path, "/"), ":reelId", post.String())
			for _, caller := range []*uuid.UUID{&viewer, nil} {
				w := g.do(rt.method, path, caller, `{}`)
				if w.Code != http.StatusGone {
					t.Fatalf("%s %s (refuse=%v): %d %s", rt.method, path, refuse, w.Code, w.Body.String())
				}
			}
		}
		if len(g.calls) != 0 {
			t.Fatalf("a retired route ran the read gate: %+v", g.calls)
		}
	}
}
