package http

// The one settlement path a dev stack has, and the switch that is the only
// thing standing between it and production.
//
// ─── THE DEFECT ─────────────────────────────────────────────────────────
//
// payments-service in stub mode builds `provider = nil` — a RazorpayProvider
// on fake credentials would place real HTTP calls to Razorpay, and a stub
// cannot verify a signature anyway — so POST /v1/payments/webhook answers
// 503. The P0 fence had also removed POST /orders/:id/payment/confirm, whose
// own comment in payments-service/cmd/server/main.go still named it as what
// settles a stub order.
//
// Between them, the dev stack had NO way to move a prepaid order out of
// payment_pending. Not a slow way, not an awkward way: none. Refunds,
// shipment booking and delivery all begin at `paid`, so none of them could
// be exercised on the only environment where they are exercised.
//
// ─── WHAT IS PROVEN HERE ────────────────────────────────────────────────
//
// The route is reachable ONLY with the stub flag, in both halves of the
// mechanism — registration and fence — and every other fenced money route is
// untouched by the flag. That last part is the one that matters: a switch
// that opened the fence generally, rather than this one pattern, would be a
// far worse defect than the one it fixes.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

const anOrder = "8a1f0b3c-0000-4000-8000-000000000001"

// stubEngine is productionEngine with PAYMENTS_ALLOW_STUB set — the dev
// stack's wiring, both halves in agreement, exactly as cmd/server/main.go
// builds it from the one env var.
func stubEngine(t *testing.T) *gin.Engine {
	t.Helper()
	r := gin.New()
	r.Use(FenceMiddlewareWithStubSettlement(true))
	h := (&Handler{}).WithStubSettlement(true)
	h.RegisterRoutes(r)
	h.RegisterP0Routes(r)
	return r
}

// Production: registered nowhere, and 404 at the fence even so.
func TestConfirmIsAbsentWithoutTheStubFlag(t *testing.T) {
	r := productionEngine(t)

	for _, ri := range r.Routes() {
		if ri.Method == http.MethodPost && ri.Path == StubSettlementPattern {
			t.Fatalf("%s %s is registered without PAYMENTS_ALLOW_STUB; with a real gateway "+
				"this route lets the payer assert that they paid", ri.Method, ri.Path)
		}
	}
	if why := FencedRouteReason(http.MethodPost, "/v1/commerce/orders/"+anOrder+"/payment/confirm"); why == "" {
		t.Fatal("the confirm route is not fenced by the PRODUCTION fence list; " +
			"FencedRouteReason must never apply the stub exemption")
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost,
		"/v1/commerce/orders/"+anOrder+"/payment/confirm", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("confirm answered %d without the stub flag, want 404", w.Code)
	}
}

// Dev stack: registered, and the fence lets it through.
//
// The handler is reached — which is all this level can prove; that it then
// requires ownership, a payable state, gateway="stub" and a verified
// advisory callback is Service.ConfirmPayment's contract and is proven in
// internal/service. What matters here is that the request stops being
// swallowed by the fence, because a 404 is indistinguishable from a deploy
// problem and is what sent the E2E journey looking for a missing route.
func TestConfirmIsReachableWithTheStubFlag(t *testing.T) {
	r := stubEngine(t)

	registered := false
	for _, ri := range r.Routes() {
		if ri.Method == http.MethodPost && ri.Path == StubSettlementPattern {
			registered = true
		}
	}
	if !registered {
		t.Fatal("confirm is not registered with PAYMENTS_ALLOW_STUB set; a stub order can " +
			"never reach `paid`, and refunds/shipments/delivery cannot be tested at all")
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost,
		"/v1/commerce/orders/"+anOrder+"/payment/confirm", nil))
	if w.Code == http.StatusNotFound {
		t.Fatalf("confirm still answers 404 with the stub flag set: the route is registered "+
			"but the fence is still refusing it, so the two halves of the flag disagree\n%s",
			w.Body.String())
	}
}

// The flag opens ONE pattern. Everything else the fence holds, it keeps
// holding — including the legacy float checkout and return creation, which
// have nothing to do with stub settlement.
func TestTheStubFlagOpensNothingElse(t *testing.T) {
	r := stubEngine(t)

	for _, tc := range []struct{ method, path, what string }{
		{http.MethodPost, "/v1/commerce/orders/checkout",
			"the legacy float checkout that could produce a payable order holding only some of its stock"},
		{http.MethodPost, "/v1/commerce/orders/" + anOrder + "/returns",
			"return creation, which trusts caller-supplied order and seller ids"},
		{http.MethodGet, "/v1/commerce/seller/earnings", "the fenced earnings surface"},
		{http.MethodGet, "/v1/commerce/payout/preview", "the fenced payout surface"},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d with the stub flag set, want 404 — the flag must open the "+
				"confirm route and nothing else (%s)", tc.method, tc.path, w.Code, tc.what)
		}
	}
}

// And the exemption is scoped to the pattern, not to the prefix or the
// method: a neighbouring path under the same order must stay fenced.
func TestTheStubExemptionIsScopedToTheConfirmPattern(t *testing.T) {
	base := "/v1/commerce/orders/" + anOrder
	if why := fencedRouteReason(http.MethodPost, base+"/payment/confirm", true); why != "" {
		t.Errorf("the confirm route is still fenced with the flag on: %s", why)
	}
	if fencedRouteReason(http.MethodPost, base+"/returns", true) == "" {
		t.Error("return creation lost its fence when the stub flag was set")
	}
	if fencedRouteReason(http.MethodPost, "/v1/commerce/orders/checkout", true) == "" {
		t.Error("the legacy checkout lost its fence when the stub flag was set")
	}
}
