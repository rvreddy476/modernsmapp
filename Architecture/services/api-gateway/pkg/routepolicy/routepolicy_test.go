package routepolicy

import "testing"

// The regression that matters: someone re-adds the payments proxy. Before
// LB-1 this exact line existed in main.go and was the root of the
// zero-rupee "paid order" chain.
func TestGuardRejectsPaymentsProxy(t *testing.T) {
	err := GuardRouteTable([]Route{
		{Prefix: "/v1/commerce", Target: "http://commerce-service:8109"},
		{Prefix: "/v1/payments", Target: "http://payments-service:8102"},
	})
	if err == nil {
		t.Fatal("the gateway must refuse to start with a payments route in the table")
	}
}

// Moving the routes under /internal/ is NOT a fix — /internal/ is gated on
// an admin/moderator scope in a user JWT, which is a client surface, not a
// service surface. The guard must reject it too.
func TestGuardRejectsInternalPaymentsPath(t *testing.T) {
	if err := GuardRouteTable([]Route{
		{Prefix: "/v1/payments/internal", Target: "http://payments-service:8102"},
	}); err == nil {
		t.Fatal("/v1/payments/internal is still edge-reachable and must be rejected")
	}
}

func TestGuardAllowsTheRestOfTheTable(t *testing.T) {
	if err := GuardRouteTable([]Route{
		{Prefix: "/v1/commerce", Target: "http://commerce-service:8109"},
		{Prefix: "/v1/media", Target: "http://media-service:8087"},
		{Prefix: "/v1/wallet", Target: "http://wallet-service:8114"},
	}); err != nil {
		t.Fatalf("unrelated routes must be unaffected: %v", err)
	}
}

func TestIsForbidden(t *testing.T) {
	for _, p := range []string{
		"/v1/payments",
		"/v1/payments/intents",
		"/v1/payments/intents/abc/refund",
		"/v1/payments/internal/intents",
		"/v1/payments/webhook",
	} {
		if !IsForbidden(p) {
			t.Fatalf("%s must be forbidden at the edge", p)
		}
	}
}

// Segment-boundary matching, not raw prefix. A hypothetical
// `/v1/paymentsummary` upstream is a different service and must not be
// caught; the mirror of the near-prefix defect this repo already had with
// /v1/graph vs /v1/graphql.
func TestIsForbiddenDoesNotOverMatch(t *testing.T) {
	for _, p := range []string{
		"/v1/paymentsummary",
		"/v1/payments-report",
		"/v1/commerce/orders/x/payment/intent",
		"/v1/wallet",
	} {
		if IsForbidden(p) {
			t.Fatalf("%s must NOT be treated as a payments route", p)
		}
	}
}

// The commerce-side replacement must remain reachable — otherwise the fix
// would take checkout down with it.
func TestCommercePaymentIntentRouteStaysReachable(t *testing.T) {
	if IsForbidden("/v1/commerce/orders/11111111-1111-1111-1111-111111111111/payment/intent") {
		t.Fatal("commerce's server-authored intent endpoint must stay reachable")
	}
}

// Review §6.4 — the guard classified only the PREFIX and ignored the TARGET,
// so renaming the path restored the entire LB-1 exploit. This is the rename.
func TestGuardRejectsAPaymentsUpstreamUnderAnyPrefix(t *testing.T) {
	err := GuardRouteTable([]Route{
		{Prefix: "/v1/commerce", Target: "http://commerce-service:8109"},
		{Prefix: "/v1/money", Target: "http://payments-service:8102"},
	})
	if err == nil {
		t.Fatal("a route pointing at payments-service must be refused whatever prefix it uses")
	}
}

// And the same defect via an env-substituted URL with no obvious path clue.
func TestGuardRejectsPaymentsUpstreamViaOpaquePrefix(t *testing.T) {
	if err := GuardRouteTable([]Route{
		{Prefix: "/v1/checkout-gateway", Target: "https://payments-service.prod.svc.cluster.local"},
	}); err == nil {
		t.Fatal("payments-service must not be reachable from the edge under any prefix")
	}
}

// The target check must not over-match an unrelated upstream.
func TestGuardAllowsNonPaymentsUpstreams(t *testing.T) {
	if err := GuardRouteTable([]Route{
		{Prefix: "/v1/commerce", Target: "http://commerce-service:8109"},
		{Prefix: "/v1/billpay", Target: "http://bill-pay-service:8115"},
		{Prefix: "/v1/wallet", Target: "http://wallet-service:8114"},
	}); err != nil {
		t.Fatalf("unrelated upstreams must be unaffected: %v", err)
	}
}
