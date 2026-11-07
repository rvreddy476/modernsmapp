package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// US-L1, US-L2, US-L4: the endorsement, compatibility, About-write and
// anonymous-counter routes answer 410 for an active account, and the About
// writes name profile-service's routes. (For a deactivated account the
// :userId ones still answer 404 first: TestSubResourceReadsAreGuarded.)
func TestRetiredSocialRoutesAnswer410(t *testing.T) {
	r := newPrivacyRouter(newPrivacyFixture(), nil)
	owner := "/v1/users/" + pvOwnerID.String()
	for _, tc := range []struct {
		method, path, canonical string
	}{
		{http.MethodGet, owner + "/reputation", ""},
		{http.MethodGet, owner + "/endorsements", ""},
		{http.MethodPost, owner + "/endorse", ""},
		{http.MethodGet, owner + "/compatibility", ""},
		{http.MethodPost, owner + "/qr/scan", ""},
		{http.MethodPost, "/v1/users/links/website/click?user_id=" + pvOwnerID.String(), ""},
		{http.MethodPut, "/v1/users/me/about/work", canonicalAboutUpsertRoute},
		{http.MethodDelete, "/v1/users/me/about/work/" + pvOwnerID.String(), canonicalAboutDeleteRoute},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"skill_tag":"go","data":{}}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-User-Id", pvStrangerID.String())
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req) // a live handler would reach a nil service and panic
			if w.Code != http.StatusGone || !strings.Contains(w.Body.String(), "ROUTE_RETIRED") {
				t.Fatalf("status %d body %s, want 410 ROUTE_RETIRED", w.Code, w.Body.String())
			}
			if tc.canonical != "" && !strings.Contains(w.Body.String(), tc.canonical) {
				t.Fatalf("does not name %s: %s", tc.canonical, w.Body.String())
			}
		})
	}
}

// The About replacements live in profile-service, never back in user-service.
func TestCanonicalAboutRoutesAreProfileService(t *testing.T) {
	for _, r := range []string{canonicalAboutUpsertRoute, canonicalAboutDeleteRoute} {
		if !strings.Contains(r, "/v1/profiles/me/about/") || strings.Contains(r, "/v1/users") {
			t.Fatalf("canonical About route %q does not point at profile-service", r)
		}
	}
}
