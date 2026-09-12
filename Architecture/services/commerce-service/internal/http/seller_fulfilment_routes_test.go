package http

// The seller fulfilment routes exist, and they are reachable through the
// fence.
//
// Both halves are proven against the server's own registration and fence,
// not a copy: the route table is read back from the engine RegisterRoutes
// built, and each path is sent through FenceMiddleware. A request with no
// X-User-Id reaching the handler answers 401 UNAUTHORIZED; a fenced one would
// answer 404 before routing, and an unregistered one 404 from gin. The 401
// is therefore the proof that the handler ran.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const sellerOrderID = "8a1f0b3c-0000-4000-8000-000000000001"

func TestSellerFulfilmentRoutesAreRegistered(t *testing.T) {
	r := productionEngine(t)
	want := map[string]bool{
		"POST /v1/commerce/seller/orders/:orderId/pack":   false,
		"POST /v1/commerce/seller/orders/:orderId/cancel": false,
		"POST /v1/commerce/seller/orders/:orderId/ship":   false,
		"GET /v1/commerce/seller/orders/:orderId/history": false,
		"POST /v1/commerce/orders/:orderId/shipment":      false,
		"GET /v1/commerce/orders/:orderId/shipments":      false,
		"POST /v1/commerce/orders/:orderId/cancel":        false,
		"GET /v1/commerce/seller/orders":                  false,
	}
	for _, ri := range r.Routes() {
		key := ri.Method + " " + ri.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for key, seen := range want {
		if !seen {
			t.Errorf("%s is not registered; the seller build calls it", key)
		}
	}
}

func TestSellerFulfilmentRoutesAreNotFenced(t *testing.T) {
	paths := []string{
		"/v1/commerce/seller/orders/" + sellerOrderID + "/pack",
		"/v1/commerce/seller/orders/" + sellerOrderID + "/cancel",
		"/v1/commerce/seller/orders/" + sellerOrderID + "/ship",
		"/v1/commerce/seller/orders/" + sellerOrderID + "/history",
		"/v1/commerce/orders/" + sellerOrderID + "/shipment",
	}
	for _, p := range paths {
		if IsFencedPath(p) {
			t.Errorf("%s is inside the P0 fence; the seller cannot reach it", p)
		}
	}
}

// Reachability, end to end through the fence: an anonymous call gets the
// handler's own 401, not the fence's 404 and not gin's.
func TestSellerFulfilmentRoutesReachTheirHandlers(t *testing.T) {
	r := productionEngine(t)
	cases := []struct{ method, path string }{
		{http.MethodPost, "/v1/commerce/seller/orders/" + sellerOrderID + "/pack"},
		{http.MethodPost, "/v1/commerce/seller/orders/" + sellerOrderID + "/cancel"},
		{http.MethodPost, "/v1/commerce/seller/orders/" + sellerOrderID + "/ship"},
		{http.MethodGet, "/v1/commerce/seller/orders/" + sellerOrderID + "/history"},
		{http.MethodPost, "/v1/commerce/orders/" + sellerOrderID + "/shipment"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s anonymous = %d, want 401 from the handler\n%s", tc.method, tc.path, w.Code, w.Body.String())
			continue
		}
		var env struct {
			Error struct{ Code string } `json:"error"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		if env.Error.Code != "UNAUTHORIZED" {
			t.Errorf("%s %s error code = %q, want UNAUTHORIZED", tc.method, tc.path, env.Error.Code)
		}
	}
}
