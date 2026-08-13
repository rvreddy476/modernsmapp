package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// Module 3 M3-P0-1 / SR-3 — one writer for profile fields.
//
// `PUT /v1/users/me` and `PUT /v1/profiles/me` both wrote the same fields into
// different stores with no merge rule. The two that matter are date of birth,
// which the 18+ gate reads, and username, which impersonation checks read: a
// value settable through a second unaudited path is not a value any policy can
// depend on.

func TestRetiredUserProfileWriteAnswers410AndNamesTheReplacement(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PUT("/v1/users/me", retiredProfileWrite)

	req := httptest.NewRequest(http.MethodPut, "/v1/users/me",
		strings.NewReader(`{"display_name":"x","dob":"1990-01-01"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "11111111-1111-4111-8111-111111111111")
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusGone {
		t.Fatalf("status %d, want 410 Gone", resp.Code)
	}
	body := resp.Body.String()
	if !strings.Contains(body, "ROUTE_RETIRED") {
		t.Errorf("error code missing from body: %s", body)
	}
	if !strings.Contains(body, canonicalProfileWriteRoute) {
		t.Errorf("response does not name the canonical writer (%s): %s",
			canonicalProfileWriteRoute, body)
	}
}

// The canonical replacement must live in profile-service. Pointing it back at
// user-service would relocate the duplicate rather than remove it.
func TestCanonicalProfileWriteRouteIsProfileService(t *testing.T) {
	if !strings.Contains(canonicalProfileWriteRoute, "/v1/profiles") {
		t.Fatalf("canonical route %q does not point at profile-service", canonicalProfileWriteRoute)
	}
	if strings.Contains(canonicalProfileWriteRoute, "/v1/users") {
		t.Fatalf("canonical route %q still points back into user-service", canonicalProfileWriteRoute)
	}
}
