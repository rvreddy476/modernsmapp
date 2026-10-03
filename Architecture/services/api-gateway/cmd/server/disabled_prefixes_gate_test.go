package main

// NOTE: new files under cmd/server/ were git-ignored by the root `server` rule
// before the `!server/` exception; add with `git add -f` if in doubt.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/atpost/api-gateway/pkg/disabledprefixes"
)

// launchDisabledPrefixes is the GATEWAY_DISABLED_PREFIXES value the first
// production deployment sets: upstreams that are not deployed at launch.
// /v1/food is deliberately absent — food-service IS deployed (gated inside
// the service), so its prefix keeps proxying.
const launchDisabledPrefixes = "/v1/memories,/v1/flags,/v1/admin/flags,/v1/reviewer"

// Route inventory for the launch: these prefixes are in the route table and
// point at the services the deployment plan names, so the switch above has
// something to close. /v1/live must NOT be in the table (410 is served
// before matching).
func TestRouteInventoryForLaunch(t *testing.T) {
	table := map[string]string{}
	for _, rd := range routeDefinitions() {
		table[rd.prefix] = rd.target
	}
	wantHost := map[string]string{
		"/v1/food":        "food-service",
		"/v1/reviewer":    "reviewer-service",
		"/v1/flags":       "feature-flag-service",
		"/v1/admin/flags": "feature-flag-service",
		"/v1/memories":    "memories-service",
	}
	for prefix, host := range wantHost {
		target, ok := table[prefix]
		if !ok {
			t.Fatalf("route table no longer has %s; update GATEWAY_DISABLED_PREFIXES in the runbook and this inventory", prefix)
		}
		u, err := url.Parse(target)
		if err != nil {
			t.Fatalf("%s target %q: %v", prefix, target, err)
		}
		if u.Hostname() != host {
			t.Fatalf("%s -> %s, inventory expects host %s", prefix, target, host)
		}
	}
	if _, ok := table["/v1/live"]; ok {
		t.Fatal("/v1/live is retired and must not be proxied; serveLiveV1Retired answers 410")
	}
	if _, err := disabledprefixes.Parse(launchDisabledPrefixes, prefixesOf(routeDefinitions())); err != nil {
		t.Fatalf("launch value does not parse against the route table: %v", err)
	}
}

// unreachableRoutes builds the real route table with every upstream pointed
// at a closed port, so anything that proxies fails fast and visibly.
func unreachableRoutes(t *testing.T) []route {
	t.Helper()
	target, _ := url.Parse("http://127.0.0.1:1")
	var routes []route
	for _, rd := range routeDefinitions() {
		routes = append(routes, newRoute(rd.prefix, target, ""))
	}
	return routes
}

func decodeErrorCode(t *testing.T, res *httptest.ResponseRecorder, path string) string {
	t.Helper()
	var parsed struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("%s: body is not JSON: %v (%d %q)", path, err, res.Code, res.Body.String())
	}
	return parsed.Error.Code
}

// The core handler answers a disabled prefix itself: 404 NOT_AVAILABLE, JSON,
// no proxy attempt. Live v1 keeps its 410, and a prefix not on the list
// (food) still reaches the proxy.
func TestDisabledPrefixesAnsweredBeforeProxy(t *testing.T) {
	disabled, err := disabledprefixes.Parse(launchDisabledPrefixes, prefixesOf(routeDefinitions()))
	if err != nil {
		t.Fatal(err)
	}
	// reviewerPublicEnabled=false: the reviewer launch gate (503) is also
	// armed, and the disabled switch must win for an undeployed service.
	core := newCoreHandler(unreachableRoutes(t), nil, false, nil, disabled)

	closed := []string{
		"/v1/memories", "/v1/memories/today",
		"/v1/flags", "/v1/flags/evaluate",
		"/v1/admin/flags", "/v1/admin/flags/some-flag",
		"/v1/reviewer", "/v1/reviewer/assignments/next",
	}
	for _, path := range closed {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			res := httptest.NewRecorder()
			core.ServeHTTP(res, httptest.NewRequest(method, path, nil))
			if res.Code != http.StatusNotFound {
				t.Fatalf("%s %s: status %d, want 404 (%q)", method, path, res.Code, res.Body.String())
			}
			if code := decodeErrorCode(t, res, path); code != disabledprefixes.Code {
				t.Fatalf("%s %s: code %q, want %q", method, path, code, disabledprefixes.Code)
			}
		}
	}

	// Live v1 is retired, not "not available": the 410 contract stands.
	res := httptest.NewRecorder()
	core.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/live/streams", nil))
	if res.Code != http.StatusGone || decodeErrorCode(t, res, "/v1/live/streams") != "LIVE_V1_RETIRED" {
		t.Fatalf("/v1/live/streams: %d %q, want 410 LIVE_V1_RETIRED", res.Code, res.Body.String())
	}

	// Food is deployed (gated inside the service): it must still be proxied,
	// which against the closed port shows up as the proxy's 502 — anything
	// but the edge's own 404 NOT_AVAILABLE.
	res = httptest.NewRecorder()
	core.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/food/restaurants", nil))
	if res.Code == http.StatusNotFound {
		t.Fatalf("/v1/food/restaurants: answered 404 at the edge (%q); food must proxy", res.Body.String())
	}
	if res.Code != http.StatusBadGateway {
		t.Fatalf("/v1/food/restaurants: expected the proxy's 502 against a closed port, got %d %q", res.Code, res.Body.String())
	}
}

// With the switch unset nothing changes: the undeployed prefixes fall
// through to the proxy exactly as before (and the reviewer gate still
// answers its own 503).
func TestDisabledPrefixesUnsetIsNoOp(t *testing.T) {
	disabled, err := disabledprefixes.Parse("", prefixesOf(routeDefinitions()))
	if err != nil {
		t.Fatal(err)
	}
	core := newCoreHandler(unreachableRoutes(t), nil, false, nil, disabled)

	res := httptest.NewRecorder()
	core.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/memories/today", nil))
	if res.Code != http.StatusBadGateway {
		t.Fatalf("/v1/memories/today unset: got %d %q, want the proxy's 502", res.Code, res.Body.String())
	}

	res = httptest.NewRecorder()
	core.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/reviewer/assignments/next", nil))
	if res.Code != http.StatusServiceUnavailable || decodeErrorCode(t, res, "/v1/reviewer") != "REVIEWER_PROGRAM_UNAVAILABLE" {
		t.Fatalf("/v1/reviewer unset: %d %q, want the reviewer launch gate's 503", res.Code, res.Body.String())
	}
}

// A misspelt prefix must refuse boot rather than leave the route open.
func TestDisabledPrefixesTypoRefusesBoot(t *testing.T) {
	if _, err := disabledprefixes.Parse("/v1/memory,/v1/flags", prefixesOf(routeDefinitions())); err == nil {
		t.Fatal("expected an error for /v1/memory (not a route prefix)")
	}
}
