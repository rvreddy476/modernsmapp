package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// Tube channel subscriptions moved to post-service (2026-09-12). The public
// subscribe / subscription / list routes here answer 410 and point at the
// replacement, so a deployed client fails visibly instead of writing a
// subscription that nothing fans out.

func TestRetiredSubscriptionRoutesAnswer410WithLocation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	(&Handler{}).RegisterRoutes(r)

	const channel = "22222222-2222-4222-8222-222222222222"
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/v1/channels/" + channel + "/subscribe"},
		{http.MethodDelete, "/v1/channels/" + channel + "/subscribe"},
		{http.MethodGet, "/v1/channels/" + channel + "/subscription"},
		{http.MethodGet, "/v1/channels/" + channel + "/subscribers"},
		{http.MethodGet, "/v1/users/11111111-1111-4111-8111-111111111111/subscriptions"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"notify_on":"all"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-Id", "11111111-1111-4111-8111-111111111111")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusGone {
			t.Errorf("%s %s: status %d, want 410 Gone: %s", tc.method, tc.path, rec.Code, rec.Body.String())
			continue
		}
		if loc := rec.Header().Get("Location"); loc != canonicalSubscriptionsRoute {
			t.Errorf("%s %s: Location %q, want %q", tc.method, tc.path, loc, canonicalSubscriptionsRoute)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "ROUTE_RETIRED") || !strings.Contains(body, "/v1/channels") {
			t.Errorf("%s %s: body does not name the retirement and the replacement: %s", tc.method, tc.path, body)
		}
	}
}

// The replacement must live in post-service's channel surface. Pointing it
// back at a user-service path would relocate the duplicate, not remove it.
func TestCanonicalSubscriptionsRouteIsPostService(t *testing.T) {
	if !strings.HasPrefix(canonicalSubscriptionsRoute, "/v1/channels/") {
		t.Fatalf("canonical route %q is not under /v1/channels", canonicalSubscriptionsRoute)
	}
	if strings.Contains(canonicalSubscriptionsRoute, "/v1/users") {
		t.Fatalf("canonical route %q still points back into user-service", canonicalSubscriptionsRoute)
	}
}
