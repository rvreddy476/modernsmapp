package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Module 3 M3-P0-1 / SR-3 — the duplicate social graph must be unreachable.
//
// A 410 that still writes is worse than no change at all, so these tests do
// not merely check the status code: the service under them PANICS if any
// retired route reaches a graph method. Asserting the response alone would
// pass even if the handler wrote the row and then returned 410.

// tripwireProfileService fails the test if a retired graph method is called.
type tripwireProfileService struct {
	stubProfileService
	t *testing.T
}

func (s *tripwireProfileService) reached(method string) {
	s.t.Helper()
	s.t.Fatalf("retired route reached %s: the shadow graph is still being written, "+
		"and a block written there protects nobody", method)
}

// The username MUST resolve, or the retired handlers would 404 before
// reaching the graph write and the tripwire would never fire — the test would
// then pass on a route that still writes.
func (s *tripwireProfileService) GetProfileByUsername(_ context.Context, username string) (*store.Profile, error) {
	return &store.Profile{UserID: uuid.New(), Username: &username}, nil
}

func (s *tripwireProfileService) FollowUser(_ context.Context, _, _ uuid.UUID) (*store.Follow, error) {
	s.reached("FollowUser")
	return nil, nil
}

func (s *tripwireProfileService) UnfollowUser(_ context.Context, _, _ uuid.UUID) error {
	s.reached("UnfollowUser")
	return nil
}

func (s *tripwireProfileService) BlockUser(_ context.Context, _, _ uuid.UUID) error {
	s.reached("BlockUser")
	return nil
}

func (s *tripwireProfileService) UnblockUser(_ context.Context, _, _ uuid.UUID) error {
	s.reached("UnblockUser")
	return nil
}

func retiredRouteRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&tripwireProfileService{t: t}, nil)
	h.RegisterRoutes(r, func(c *gin.Context) { c.Next() }, func(c *gin.Context) { c.Next() })
	return r
}

func TestRetiredGraphRoutesAnswer410AndDoNotWrite(t *testing.T) {
	victim := uuid.New().String()

	cases := []struct{ method, path string }{
		{http.MethodPost, "/v1/profiles/someone/follow"},
		{http.MethodDelete, "/v1/profiles/someone/follow"},
		{http.MethodPost, "/v1/profiles/someone/block"},
		{http.MethodDelete, "/v1/profiles/someone/block"},
		{http.MethodGet, "/v1/profiles/me/blocks"},
		{http.MethodGet, "/v1/profiles/" + victim + "/followers"},
		{http.MethodGet, "/v1/profiles/" + victim + "/following"},
		{http.MethodGet, "/v1/profiles/" + victim + "/relationship"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			r := retiredRouteRouter(t)
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("X-User-Id", uuid.New().String())
			resp := httptest.NewRecorder()
			r.ServeHTTP(resp, req)

			if resp.Code != http.StatusGone {
				t.Fatalf("status %d, want 410 Gone. A route that still answers 2xx "+
					"keeps writing a graph no other service enforces.", resp.Code)
			}

			// The response must name the replacement. A bare 410 leaves a
			// client author guessing, and guessing produces a new shadow graph.
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
					Details struct {
						CanonicalRoute string `json:"canonical_route"`
					} `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body %q: %v", resp.Body.String(), err)
			}
			if body.Error.Code != "ROUTE_RETIRED" {
				t.Errorf("error code = %q, want ROUTE_RETIRED", body.Error.Code)
			}
			if !strings.Contains(body.Error.Details.CanonicalRoute, "/v1/graph") {
				t.Errorf("response does not name a graph-service replacement: %q",
					body.Error.Details.CanonicalRoute)
			}
		})
	}
}

// Every retirement must name a canonical replacement, and that replacement
// must be a graph-service route — not another profile-service path, which
// would just relocate the duplicate.
func TestEveryRetiredRouteNamesAGraphServiceReplacement(t *testing.T) {
	if len(canonicalGraphRoutes) == 0 {
		t.Fatal("no routes recorded as retired")
	}
	for retired, canonical := range canonicalGraphRoutes {
		if !strings.Contains(canonical, "/v1/graph") {
			t.Errorf("%s → %q does not point at graph-service", retired, canonical)
		}
		if strings.Contains(canonical, "/v1/profiles") {
			t.Errorf("%s → %q still points back into profile-service", retired, canonical)
		}
	}
}

// The profile READ path must keep working. Retiring the graph must not take
// the profile with it — that would be a regression dressed up as a fix.
func TestProfileReadsStillWorkAfterGraphRetirement(t *testing.T) {
	r := retiredRouteRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/profiles/health", nil)
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code == http.StatusGone {
		t.Fatal("a non-graph route was retired by mistake")
	}
}
