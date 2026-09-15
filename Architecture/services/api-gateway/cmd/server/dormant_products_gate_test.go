package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

var (
	groupsAndCommunitiesPaths = []string{"/v1/groups", "/v1/groups/abc/posts", "/v1/communities", "/v1/communities/x/join"}
	riderPaths                = []string{"/v1/rider", "/v1/rider/rides", "/v1/rider/admin/dashboard", "/v1/rider/partners/me/aadhaar/start"}
	datingPaths               = []string{"/v1/dating", "/v1/dating/profile", "/v1/dating/admin/reports", "/v1/dating/pulse/today", "/v1/dating/premium/webhook"}
)

// The dev test accounts call_a and call_b, plus a user on no allowlist.
const (
	pilotCallA   = "2d598287-eee7-40b4-a7f5-b46b9412e4e7"
	pilotCallB   = "66668bc2-a3f6-40a5-9cdd-c998dcf72f29"
	outsiderUser = "11111111-1111-4111-8111-111111111111"
)

// productsFrom builds the table the way a development gateway boots.
func productsFrom(t *testing.T, m map[string]string) []dormantProduct {
	t.Helper()
	products, err := dormantProductsFromEnv(envOf(m), false)
	if err != nil {
		t.Fatalf("dormantProductsFromEnv: %v", err)
	}
	return products
}

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

// Groups, communities, Mopedu and dating have no working client on any
// platform, so their public prefixes are closed by default and open only by an
// explicit flag. The gate must answer 404, not 503, so the edge does not
// confirm the product exists.
func TestDormantProductsAreClosedByDefault(t *testing.T) {
	products := productsFrom(t, nil)
	var all []string
	all = append(all, groupsAndCommunitiesPaths...)
	all = append(all, riderPaths...)
	all = append(all, datingPaths...)
	for _, path := range all {
		if !gated(t, products, path) {
			t.Errorf("%s was not gated", path)
		}
	}
}

func TestDormantProductsGateDoesNotTouchNeighbours(t *testing.T) {
	products := productsFrom(t, nil)
	for _, path := range []string{
		"/v1/groupsx", "/v1/broadcast-channels", "/v1/community-service", "/v1/chat/requests",
		"/v1/riders", "/v1/ride", "/v1/food", "/v1/datingx", "/v1/date",
	} {
		if gated(t, products, path) {
			t.Errorf("%s was gated but is not a dormant product", path)
		}
	}
}

func TestDormantProductsOpenWhenEnabled(t *testing.T) {
	products := productsFrom(t, map[string]string{"DORMANT_PRODUCTS_ENABLED": "true"})
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
	groupsOpen := productsFrom(t, map[string]string{"DORMANT_PRODUCTS_ENABLED": "true"})
	for _, path := range riderPaths {
		if !gated(t, groupsOpen, path) {
			t.Errorf("DORMANT_PRODUCTS_ENABLED=true opened %s", path)
		}
	}

	riderOpen := productsFrom(t, map[string]string{"RIDER_PUBLIC_ENABLED": "true"})
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

// Dating opens on DATING_PUBLIC_ENABLED alone, and that flag opens nothing else.
func TestDatingGateIsIndependentOfOtherFlags(t *testing.T) {
	othersOpen := productsFrom(t, map[string]string{"DORMANT_PRODUCTS_ENABLED": "true", "RIDER_PUBLIC_ENABLED": "true"})
	for _, path := range datingPaths {
		if !gated(t, othersOpen, path) {
			t.Errorf("another product's flag opened %s", path)
		}
	}

	datingOpen := productsFrom(t, map[string]string{"DATING_PUBLIC_ENABLED": "true"})
	for _, path := range datingPaths {
		if gated(t, datingOpen, path) {
			t.Errorf("DATING_PUBLIC_ENABLED=true still gates %s", path)
		}
	}
	for _, path := range append(append([]string{}, groupsAndCommunitiesPaths...), riderPaths...) {
		if !gated(t, datingOpen, path) {
			t.Errorf("DATING_PUBLIC_ENABLED=true opened %s", path)
		}
	}
}

// gatewayResult is what the edge did with one request.
type gatewayResult struct {
	status  int
	reached bool
}

// throughGateway runs a request through the same order main wires: token
// verification first, then the dormant-products gate, then the upstream.
func throughGateway(t *testing.T, env map[string]string, path string, prepare func(*http.Request)) gatewayResult {
	t.Helper()
	products := productsFrom(t, env)
	keys := jwtKeySet{activeKID: "v1", activeSecret: "secret"}
	var reached bool
	core := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveDormantProductGate(w, r, products) {
			return
		}
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if prepare != nil {
		prepare(req)
	}
	jwtExtractMiddleware(keys, devTestPolicy(), core).ServeHTTP(res, req)
	return gatewayResult{status: res.Code, reached: reached}
}

// bearer signs a token for userID with the given secret. The gateway under
// test only accepts "secret", so any other secret is a forgery.
func bearer(t *testing.T, userID, secret string) func(*http.Request) {
	t.Helper()
	token := signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"}, map[string]any{
		"user_id": userID,
		"exp":     time.Now().Add(time.Hour).Unix(),
	}, secret)
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

func both(prepares ...func(*http.Request)) func(*http.Request) {
	return func(r *http.Request) {
		for _, p := range prepares {
			p(r)
		}
	}
}

func spoofUserHeaders(userID string) func(*http.Request) {
	return func(r *http.Request) {
		r.Header.Set("X-User-Id", userID)
		r.Header.Set("X-Verified-User-Id", userID)
	}
}

func TestDatingGateWhileClosed(t *testing.T) {
	pilot := map[string]string{"DATING_PILOT_USER_IDS": pilotCallA + "," + pilotCallB}

	closedCases := []struct {
		name    string
		path    string
		prepare func(*http.Request)
	}{
		{"anonymous", "/v1/dating/profile", nil},
		{"anonymous on admin", "/v1/dating/admin/reports", nil},
		{"anonymous on the webhook", "/v1/dating/premium/webhook", nil},
		{"non-allowlisted user on admin", "/v1/dating/admin/reports", bearer(t, outsiderUser, "secret")},
		{"non-allowlisted user on pulse", "/v1/dating/pulse/today", bearer(t, outsiderUser, "secret")},
		{"anonymous claiming an allowlisted X-User-Id", "/v1/dating/profile", spoofUserHeaders(pilotCallA)},
		{"non-allowlisted user claiming an allowlisted X-User-Id", "/v1/dating/pulse/today",
			both(bearer(t, outsiderUser, "secret"), spoofUserHeaders(pilotCallA))},
	}
	for _, tc := range closedCases {
		got := throughGateway(t, pilot, tc.path, tc.prepare)
		if got.reached || got.status != http.StatusNotFound {
			t.Errorf("%s: %s gave status %d, reached upstream=%v; want 404, not reached", tc.name, tc.path, got.status, got.reached)
		}
	}

	// A token signed with the wrong key, carrying an allowlisted id, is refused
	// by verification before the gate, and never reaches dating.
	for _, forged := range []func(*http.Request){
		bearer(t, pilotCallA, "not-the-gateway-secret"),
		func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer eyJhbGciOiJub25lIn0.eyJ1c2VyX2lkIjoiMmQ1OTgyODctZWVlNy00MGI0LWE3ZjUtYjQ2Yjk0MTJlNGU3In0.")
		},
	} {
		got := throughGateway(t, pilot, "/v1/dating/profile", both(forged, spoofUserHeaders(pilotCallA)))
		if got.reached || (got.status != http.StatusUnauthorized && got.status != http.StatusNotFound) {
			t.Errorf("forged token: status %d, reached upstream=%v; want 401 or 404, not reached", got.status, got.reached)
		}
	}

	for _, user := range []string{pilotCallA, pilotCallB} {
		for _, path := range []string{"/v1/dating/profile", "/v1/dating/admin/reports", "/v1/dating/pulse/today"} {
			got := throughGateway(t, pilot, path, bearer(t, user, "secret"))
			if !got.reached {
				t.Errorf("allowlisted %s on %s: status %d, upstream not reached", user, path, got.status)
			}
		}
	}
}

func TestDatingGateEmptyAllowlistLetsNobodyIn(t *testing.T) {
	for _, env := range []map[string]string{
		nil,
		{"DATING_PILOT_USER_IDS": ""},
		{"DATING_PILOT_USER_IDS": " , ,"},
	} {
		for _, user := range []string{pilotCallA, outsiderUser} {
			got := throughGateway(t, env, "/v1/dating/profile", bearer(t, user, "secret"))
			if got.reached || got.status != http.StatusNotFound {
				t.Errorf("allowlist %q, user %s: status %d, reached=%v; want 404", env["DATING_PILOT_USER_IDS"], user, got.status, got.reached)
			}
		}
	}
}

func TestDatingGateOpenBehavesAsBefore(t *testing.T) {
	open := map[string]string{"DATING_PUBLIC_ENABLED": "true"}
	for _, prepare := range []func(*http.Request){nil, bearer(t, outsiderUser, "secret")} {
		for _, path := range datingPaths {
			if got := throughGateway(t, open, path, prepare); !got.reached {
				t.Errorf("DATING_PUBLIC_ENABLED=true: %s gave status %d, upstream not reached", path, got.status)
			}
		}
	}
}

// The pilot allowlist belongs to dating only. A pilot user is still shut out of
// every other closed product.
func TestDatingPilotDoesNotOpenOtherProducts(t *testing.T) {
	pilot := map[string]string{"DATING_PILOT_USER_IDS": pilotCallA}
	for _, path := range append(append([]string{}, groupsAndCommunitiesPaths...), riderPaths...) {
		if got := throughGateway(t, pilot, path, bearer(t, pilotCallA, "secret")); got.reached {
			t.Errorf("dating pilot user reached %s", path)
		}
	}
}

// The gate reads the identity jwtExtractMiddleware verified, never a header.
// This calls the gate directly with gateway-looking headers and no verified
// identity, which the full chain cannot produce because it strips them first.
func TestDatingGateIgnoresIdentityHeaders(t *testing.T) {
	products := productsFrom(t, map[string]string{"DATING_PILOT_USER_IDS": pilotCallA})
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/dating/profile", nil)
	spoofUserHeaders(pilotCallA)(req)
	if !serveDormantProductGate(res, req, products) || res.Code != http.StatusNotFound {
		t.Fatalf("an X-User-Id header opened the dating gate (status %d)", res.Code)
	}
}

func TestDatingPilotAllowlistParsing(t *testing.T) {
	const withInvalid = pilotCallA + ", call_a ,66668BC2-A3F6-40A5-9CDD-C998DCF72F29,2d598287eee740b4a7f5b46b9412e4e7"

	if _, err := dormantProductsFromEnv(envOf(map[string]string{"DATING_PILOT_USER_IDS": withInvalid}), true); err == nil {
		t.Error("production accepted an invalid DATING_PILOT_USER_IDS entry; want a boot error")
	} else if !strings.Contains(err.Error(), "DATING_PILOT_USER_IDS") {
		t.Errorf("boot error does not name the variable: %v", err)
	}

	valid := pilotCallA + "," + pilotCallB
	if _, err := dormantProductsFromEnv(envOf(map[string]string{"DATING_PILOT_USER_IDS": valid}), true); err != nil {
		t.Errorf("production rejected a valid allowlist: %v", err)
	}
	if _, err := dormantProductsFromEnv(envOf(nil), true); err != nil {
		t.Errorf("production rejected an unset allowlist: %v", err)
	}

	// Development drops the bad entries and keeps the good ones, case-insensitively.
	dev := map[string]string{"DATING_PILOT_USER_IDS": withInvalid}
	for _, user := range []string{pilotCallA, pilotCallB} {
		if got := throughGateway(t, dev, "/v1/dating/profile", bearer(t, user, "secret")); !got.reached {
			t.Errorf("dev: valid allowlisted %s was not let through (status %d)", user, got.status)
		}
	}
	if got := throughGateway(t, dev, "/v1/dating/profile", bearer(t, "call_a", "secret")); got.reached {
		t.Error("dev: an invalid allowlist entry was honoured")
	}
}
