package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// A series has to be able to shrink (2026-09-10).
//
// Only four routes were registered under /v1/video-series: create, read,
// list episodes, add episode. There was no delete for an episode and none for
// a series, so every DELETE answered gin's unrouted "404 page not found":
// a series created by mistake was permanent and an episode added by mistake
// could only be overwritten. Playlists, registered a few lines below in the
// same file, have had both deletes all along — an omission, not a decision.
//
// The inventory is read from the REAL router (the same technique
// story_route_inventory_test.go uses) so this cannot pass by someone writing
// a handler nobody wired up.

func registeredVideoSeriesRoutes(t *testing.T) map[string]bool {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{}
	h.RegisterRoutes(r)

	out := map[string]bool{}
	for _, info := range r.Routes() {
		if strings.HasPrefix(info.Path, "/v1/video-series") {
			out[info.Method+" "+info.Path] = true
		}
	}
	return out
}

func TestVideoSeriesHasRemovalRoutes(t *testing.T) {
	routes := registeredVideoSeriesRoutes(t)
	for _, want := range []string{
		"DELETE /v1/video-series/:seriesId",
		"DELETE /v1/video-series/:seriesId/episodes/:episodeRef",
	} {
		if !routes[want] {
			t.Errorf("%s is not registered.\nA video series can be created and added to but never "+
				"shrunk: a series made by mistake is permanent and an episode added by mistake can "+
				"only be overwritten. Registered video-series routes: %v", want, keysOf(routes))
		}
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Removal is owner-scoped, so an anonymous caller must be refused at the
// handler — before anything reaches the service. (The Handler here has a nil
// svc: a request that got past the identity check would panic rather than
// quietly pass.)
func TestVideoSeriesRemovalRequiresIdentity(t *testing.T) {
	seriesID := uuid.NewString()
	cases := []struct{ method, path string }{
		{http.MethodDelete, "/v1/video-series/" + seriesID},
		{http.MethodDelete, "/v1/video-series/" + seriesID + "/episodes/2"},
		{http.MethodDelete, "/v1/video-series/" + seriesID + "/episodes/" + uuid.NewString()},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := performVideoSeriesRequest(t, tc.method, tc.path, nil)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s with no X-User-Id: status=%d want 401 (body %s)",
					tc.method, tc.path, w.Code, w.Body.String())
			}
		})
	}
}

// The episode reference is either an episode number or a post id. Gin cannot
// route on the shape of a segment, so the handler decides — and anything that
// is neither must be a 400, not a 500 and not a silent no-op.
func TestDeleteVideoSeriesEpisodeRejectsUnusableReference(t *testing.T) {
	userID := uuid.NewString()
	path := "/v1/video-series/" + uuid.NewString() + "/episodes/not-a-ref"
	w := performVideoSeriesRequest(t, http.MethodDelete, path, map[string]string{"X-User-Id": userID})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("episode reference %q: status=%d want 400 (body %s)", "not-a-ref", w.Code, w.Body.String())
	}
}

func TestIsEpisodeNumber(t *testing.T) {
	for _, ref := range []string{"1", "12", "003"} {
		if !isEpisodeNumber(ref) {
			t.Errorf("%q should read as an episode number", ref)
		}
	}
	for _, ref := range []string{"", "-1", "1a", uuid.NewString()} {
		if isEpisodeNumber(ref) {
			t.Errorf("%q should not read as an episode number", ref)
		}
	}
}

func performVideoSeriesRequest(t *testing.T, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{}
	h.RegisterRoutes(r)

	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
