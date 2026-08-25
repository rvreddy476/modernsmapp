package http

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// Module 4 M4-P0-2 acceptance criterion 1 — every registered story route must
// carry an explicit policy classification.
//
// WHY A TEST AND NOT A CONVENTION
//
// The defect this closes was not a wrong check, it was a MISSING one: nobody
// noticed that GET /v1/stories/:storyId enforced nothing while its sibling
// routes filtered expiry. The same shape appeared in Module 3, where
// resolve-handle bypassed a gate every neighbouring route applied. Both were
// found by a human reading code, which does not scale and did not catch them
// for months.
//
// So the inventory is derived from the REAL gin router. A new story route
// appears here automatically and fails until someone states its policy.

// storyRoutePolicy is what a story route does about the viewer.
type storyRoutePolicy string

const (
	// policyViewerScoped: resolves a target and must run the full visibility
	// policy for a verified viewer.
	policyViewerScoped storyRoutePolicy = "viewer_scoped"
	// policyOwnerScoped: acts only on the authenticated caller's own content.
	policyOwnerScoped storyRoutePolicy = "owner_scoped"
)

// storyRoutes is the classification. Adding a route without adding it here
// fails TestEveryStoryRouteIsClassified.
var storyRoutes = map[string]storyRoutePolicy{
	"POST /v1/stories":                 policyOwnerScoped,
	"GET /v1/stories/feed":             policyViewerScoped,
	"GET /v1/stories/mine":             policyOwnerScoped,
	"GET /v1/stories/author/:authorId": policyViewerScoped,
	"GET /v1/stories/:storyId":         policyViewerScoped,
	"DELETE /v1/stories/:storyId":      policyOwnerScoped,
	"POST /v1/stories/:storyId/view":   policyViewerScoped,
}

// registeredStoryRoutes reads the actual router.
func registeredStoryRoutes(t *testing.T) []string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{}
	h.RegisterRoutes(r)

	var out []string
	for _, info := range r.Routes() {
		if strings.HasPrefix(info.Path, "/v1/stories") {
			out = append(out, info.Method+" "+info.Path)
		}
	}
	return out
}

func TestEveryStoryRouteIsClassified(t *testing.T) {
	for _, route := range registeredStoryRoutes(t) {
		if _, ok := storyRoutes[route]; !ok {
			t.Errorf("story route %q is registered but has no policy classification.\n"+
				"Every story surface resolves a target or mutates state, so it must state "+
				"whether it is viewer-scoped (runs the full visibility policy) or "+
				"owner-scoped (acts only on the caller's own content). Add it to "+
				"storyRoutes and give it a behavioural test.", route)
		}
	}
}

// The inverse: a classification for a route that no longer exists is stale and
// gives false confidence that something is covered.
func TestStoryRouteTableHasNoStaleEntries(t *testing.T) {
	live := map[string]bool{}
	for _, route := range registeredStoryRoutes(t) {
		live[route] = true
	}
	for route := range storyRoutes {
		if !live[route] {
			t.Errorf("storyRoutes classifies %q, which is not registered. "+
				"A stale entry hides the fact that nothing tests it.", route)
		}
	}
}

// Every viewer-scoped route must require a verified viewer. This is the
// property that the deleted followed_ids branch violated: it answered without
// reading X-User-Id at all.
func TestEveryViewerScopedStoryRouteRequiresAViewer(t *testing.T) {
	for route, policy := range storyRoutes {
		if policy != policyViewerScoped {
			continue
		}
		t.Run(route, func(t *testing.T) {
			parts := strings.SplitN(route, " ", 2)
			method, path := parts[0], parts[1]
			// Substitute concrete ids for the path parameters.
			path = strings.ReplaceAll(path, ":storyId", "8b1a9953-4c2f-4b1e-9f1a-2b3c4d5e6f70")
			path = strings.ReplaceAll(path, ":authorId", "9c2b8864-5d3f-4c2e-8f2b-3c4d5e6f7081")

			w := performStoryRequest(t, method, path, nil)
			if w.Code != 401 {
				t.Errorf("%s answered %d to a request with no X-User-Id; want 401.\n"+
					"An anonymous caller must not reach a story surface: an empty 200 "+
					"tells them they are allowed in, and it is the exact shape the "+
					"removed followed_ids branch used to bypass identity entirely.",
					route, w.Code)
			}
		})
	}
}

// performStoryRequest drives the real router with no identity header.
func performStoryRequest(t *testing.T, method, path string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{}
	h.RegisterRoutes(r)

	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
