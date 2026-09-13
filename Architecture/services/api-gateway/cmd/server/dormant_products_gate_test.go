package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

var (
	groupsAndCommunitiesPaths = []string{"/v1/groups", "/v1/groups/abc/posts", "/v1/communities", "/v1/communities/x/join"}
	riderPaths                = []string{"/v1/rider", "/v1/rider/rides", "/v1/rider/admin/dashboard", "/v1/rider/partners/me/aadhaar/start"}
)

func gated(t *testing.T, products []dormantProduct, path string) bool {
	t.Helper()
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if !serveDormantProductGate(res, req, products) {
		return false
	}
	if res.Code != http.StatusNotFound {
		t.Fatalf("%s: gated with status %d, want 404", path, res.Code)
	}
	return true
}

// Groups, communities and Mopedu have no working client on any platform, so
// their public prefixes are closed by default and open only by an explicit
// flag. The gate must answer 404, not 503, so the edge does not confirm the
// product exists.
func TestDormantProductsAreClosedByDefault(t *testing.T) {
	products := dormantProductsFromEnv(envOf(nil))
	for _, path := range append(append([]string{}, groupsAndCommunitiesPaths...), riderPaths...) {
		if !gated(t, products, path) {
			t.Errorf("%s was not gated", path)
		}
	}
}

func TestDormantProductsGateDoesNotTouchNeighbours(t *testing.T) {
	products := dormantProductsFromEnv(envOf(nil))
	for _, path := range []string{
		"/v1/groupsx", "/v1/broadcast-channels", "/v1/community-service", "/v1/chat/requests",
		"/v1/riders", "/v1/ride", "/v1/food",
	} {
		if gated(t, products, path) {
			t.Errorf("%s was gated but is not a dormant product", path)
		}
	}
}

func TestDormantProductsOpenWhenEnabled(t *testing.T) {
	products := dormantProductsFromEnv(envOf(map[string]string{"DORMANT_PRODUCTS_ENABLED": "true"}))
	for _, path := range groupsAndCommunitiesPaths {
		if gated(t, products, path) {
			t.Errorf("enabled dormant product %s was intercepted", path)
		}
	}
}

// Each product opens on its own flag. The shared flag is all-or-nothing (see
// docs/runbooks/communities-invite-only-pilot.md), so launching Mopedu must not
// open groups and communities, and opening those must not open Mopedu.
func TestRiderGateIsIndependentOfDormantProductsFlag(t *testing.T) {
	groupsOpen := dormantProductsFromEnv(envOf(map[string]string{"DORMANT_PRODUCTS_ENABLED": "true"}))
	for _, path := range riderPaths {
		if !gated(t, groupsOpen, path) {
			t.Errorf("DORMANT_PRODUCTS_ENABLED=true opened %s", path)
		}
	}

	riderOpen := dormantProductsFromEnv(envOf(map[string]string{"RIDER_PUBLIC_ENABLED": "true"}))
	for _, path := range riderPaths {
		if gated(t, riderOpen, path) {
			t.Errorf("RIDER_PUBLIC_ENABLED=true still gates %s", path)
		}
	}
	for _, path := range groupsAndCommunitiesPaths {
		if !gated(t, riderOpen, path) {
			t.Errorf("RIDER_PUBLIC_ENABLED=true opened %s", path)
		}
	}
}
