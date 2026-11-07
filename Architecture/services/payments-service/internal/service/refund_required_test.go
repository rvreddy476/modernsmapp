package service

import (
	"testing"

	"github.com/atpost/payments-service/internal/store/postgres"
)

// How the worker refunds an owed capture, or why it parks it.
func TestRouteOwedRefund(t *testing.T) {
	owed := func(mut func(*postgres.OwedCapture)) postgres.OwedCapture {
		o := postgres.OwedCapture{ID: 7, ProviderOrderID: "order_X", ProviderPaymentID: "pay_B",
			AmountMinor: 118000, Currency: "INR", Reason: "duplicate_capture"}
		if mut != nil {
			mut(&o)
		}
		return o
	}
	for _, tc := range []struct {
		name           string
		o              postgres.OwedCapture
		stub, provider bool
		route          owedRefundRoute
		code           string
	}{
		{"a real duplicate capture goes to the provider", owed(nil), false, true, owedRouteProvider, ""},
		{"a partly refunded one too", owed(func(o *postgres.OwedCapture) { o.RefundedMinor = 18000 }), false, true, owedRouteProvider, ""},
		{"fully refunded: nothing left", owed(func(o *postgres.OwedCapture) { o.RefundedMinor = 118000 }), false, true, owedRouteNothing, ""},
		{"no amount recorded", owed(func(o *postgres.OwedCapture) { o.AmountMinor = 0 }), false, true, owedRoutePark, owedFailNoAmount},
		{"keyed by event, no payment id", owed(func(o *postgres.OwedCapture) { o.ProviderPaymentID = "event:evt_1" }), false, true, owedRoutePark, owedFailNoPaymentID},
		{"blank payment id", owed(func(o *postgres.OwedCapture) { o.ProviderPaymentID = " " }), false, true, owedRoutePark, owedFailNoPaymentID},
		{"a duplicate order capture keyed by its intent, no payment id",
			owed(func(o *postgres.OwedCapture) {
				o.ProviderPaymentID = postgres.OwedKeyIntentPrefix + "22222222-2222-4222-8222-222222222222"
				o.Reason = "duplicate_order_capture"
			}), false, true, owedRoutePark, owedFailNoPaymentID},
		{"a duplicate order capture with its payment goes to the provider",
			owed(func(o *postgres.OwedCapture) { o.Reason = "duplicate_order_capture" }), false, true, owedRouteProvider, ""},
		{"no provider adapter", owed(nil), false, false, owedRoutePark, RefundFailNoProviderAdapter},
		{"stub payment on a stub deployment", owed(func(o *postgres.OwedCapture) { o.ProviderOrderID = "order_stub_1" }), true, false, owedRouteStub, ""},
		{"stub payment on a real provider", owed(func(o *postgres.OwedCapture) { o.ProviderOrderID = "order_stub_1" }), false, true, owedRoutePark, RefundFailStubOnRealProvider},
	} {
		t.Run(tc.name, func(t *testing.T) {
			route, code := routeOwedRefund(tc.o, tc.stub, tc.provider)
			if route != tc.route || code != tc.code {
				t.Fatalf("routeOwedRefund = %v/%q, want %v/%q", route, code, tc.route, tc.code)
			}
		})
	}
}
