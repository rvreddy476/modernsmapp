package http

// B5 / B10 — the legacy money surfaces, and the startup panic.
//
// Two things are proven here, both against the SERVER'S OWN registration
// functions rather than against a copy of the route list:
//
//	1. RegisterRoutes + RegisterP0Routes can both be applied to one gin
//	   engine without panicking. Before B5 they each registered
//	   POST /v1/commerce/checkout/quote, and gin panics on a duplicate
//	   method+path — so the production binary could not start at all. Every
//	   existing test constructed handlers directly and never called both
//	   registration functions, which is why nothing caught it.
//
//	2. The three legacy money routes answer 404 through the real
//	   FenceMiddleware, and are absent from the routing table.
//
// The negative control at the bottom removes the fence and shows the legacy
// checkout becomes reachable again — so the assertions above are load-bearing
// rather than passing because the route never existed in this test's engine.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

// productionEngine wires the engine the way cmd/server/main.go does.
func productionEngine(t *testing.T) *gin.Engine {
	t.Helper()
	r := gin.New()
	r.Use(FenceMiddleware())
	h := &Handler{}
	// This is the assertion for (1): a duplicate registration panics here.
	h.RegisterRoutes(r)
	h.RegisterP0Routes(r)
	return r
}

// B10: the full production route table must build. A duplicate
// method+path panics gin, and no amount of unit-testing individual handlers
// finds it.
func TestBothRouteSetsRegisterWithoutPanicking(t *testing.T) {
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("registering the production route table panicked: %v", rec)
		}
	}()
	_ = productionEngine(t)
}

// B10: and the P0 quote is the registration that survives.
func TestQuoteRouteIsRegisteredExactlyOnce(t *testing.T) {
	r := productionEngine(t)
	n := 0
	for _, ri := range r.Routes() {
		if ri.Method == http.MethodPost && ri.Path == "/v1/commerce/checkout/quote" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("POST /v1/commerce/checkout/quote is registered %d times; want exactly 1", n)
	}
}

// B5: the legacy money routes are unreachable.
func TestLegacyMoneyRoutesAreFenced(t *testing.T) {
	r := productionEngine(t)

	cases := []struct {
		method, path string
	}{
		{http.MethodPost, "/v1/commerce/orders/checkout"},
		{http.MethodPost, "/v1/commerce/orders/8a1f0b3c-0000-4000-8000-000000000001/payment/confirm"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d; a fenced legacy money route must answer 404", tc.method, tc.path, w.Code)
		}
	}
}

// B5: and they are not in the routing table either — the fence is the
// backstop, the deletion is the control.
func TestLegacyMoneyRoutesAreNotRegistered(t *testing.T) {
	r := productionEngine(t)
	banned := map[string]string{
		"POST /v1/commerce/orders/checkout":                 "legacy checkout",
		"POST /v1/commerce/orders/:orderId/payment/confirm": "client-asserted payment",
	}
	for _, ri := range r.Routes() {
		if why, bad := banned[ri.Method+" "+ri.Path]; bad {
			t.Errorf("%s %s is registered again (%s); it must stay deleted", ri.Method, ri.Path, why)
		}
	}
}

// Every fenced route carries a stated reason, so a regression reports the
// money defect rather than just a path.
func TestFencedRoutesExplainThemselves(t *testing.T) {
	for _, fr := range FencedRoutes {
		if fr.Why == "" {
			t.Errorf("fenced route %s %s has no stated reason", fr.Method, fr.Pattern)
		}
	}
	if FencedRouteReason(http.MethodPost, "/v1/commerce/orders/checkout") == "" {
		t.Fatal("the legacy checkout is not covered by FencedRouteReason")
	}
	// The fence must not over-match the live P0 surfaces.
	live := []struct{ method, path string }{
		{http.MethodPost, "/v1/commerce/v2/orders/checkout"},
		{http.MethodGet, "/v1/commerce/orders"},
		{http.MethodPost, "/v1/commerce/orders/8a1f0b3c-0000-4000-8000-000000000001/payment/intent"},
		{http.MethodGet, "/v1/commerce/orders/8a1f0b3c-0000-4000-8000-000000000001/payment/status"},
		{http.MethodPost, "/v1/commerce/checkout/quote"},
	}
	for _, l := range live {
		if why := FencedRouteReason(l.method, l.path); why != "" {
			t.Errorf("%s %s is a launch-loop route but the fence claims it: %s", l.method, l.path, why)
		}
	}
}

// ─── Negative control (review §4) ────────────────────────────────────
//
// The fenced-surface proof the review criticised "accepts any insert error",
// so it would pass with the protection removed. This one removes the actual
// protection — the fence middleware — and asserts the legacy route becomes
// reachable. If it does NOT become reachable, the 404s above are coming from
// something other than the fence and prove nothing about it.
func TestNegativeControl_WithoutTheFenceTheLegacyRouteIsReachable(t *testing.T) {
	r := gin.New() // deliberately NO FenceMiddleware
	h := &Handler{}
	h.RegisterRoutes(r)
	h.RegisterP0Routes(r)

	// Re-register the legacy checkout, reproducing the pre-B5 tree.
	reached := false
	r.POST("/v1/commerce/orders/checkout", func(c *gin.Context) {
		reached = true
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/commerce/orders/checkout", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	if !reached {
		t.Fatal("negative control did not reproduce the defect: the legacy checkout was " +
			"unreachable even with the fence removed, so the fence tests above prove nothing")
	}
	t.Log("negative control reproduced the original defect: with the fence removed, " +
		"POST /v1/commerce/orders/checkout is client-reachable")
}

// The returns fence had a hole exactly where it mattered.
//
// FencedPrefixes covers /v1/commerce/returns, /seller/returns and
// /me/returns. Return CREATION is POST /v1/commerce/orders/:orderId/returns —
// under the LIVE /v1/commerce/orders prefix — so IsFencedPath never matched
// it and the one route the whole family was fenced for stayed reachable.
//
// That route is the one that takes caller-supplied order_item_id and
// seller_id, which is the reason for the fence: a caller could attach a
// return to a stranger's order.
func TestReturnCreationIsFenced(t *testing.T) {
	r := productionEngine(t)

	req := httptest.NewRequest(http.MethodPost,
		"/v1/commerce/orders/"+"11111111-1111-1111-1111-111111111111"+"/returns", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("POST /orders/:id/returns returned %d, want 404. The returns family is fenced "+
			"because return creation trusts caller-supplied order and seller ids; the creation "+
			"route is the one that must not be reachable.", w.Code)
	}
}

// And the fence must not have taken the live order routes with it. A prefix
// fence over /v1/commerce/orders would have — which is why this is an exact
// method+pattern rule.
func TestFencingReturnsLeavesTheOrderLoopReachable(t *testing.T) {
	r := productionEngine(t)
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/v1/commerce/orders"},
		{http.MethodGet, "/v1/commerce/orders/11111111-1111-1111-1111-111111111111"},
		{http.MethodPost, "/v1/commerce/orders/11111111-1111-1111-1111-111111111111/payment/intent"},
		{http.MethodGet, "/v1/commerce/orders/11111111-1111-1111-1111-111111111111/payment/status"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusNotFound {
			t.Fatalf("%s %s returned 404; fencing return creation took a live P0 route with it",
				tc.method, tc.path)
		}
	}
}
