//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestE2E_Commerce_PrepaidStub pins who can move a prepaid order to paid on a
// stub-gateway stack (PAYMENTS_ALLOW_STUB=true on commerce-service and
// payments-service), through the gateway:
//
//	Customer: checkout (upi) → the order parks in payment_pending/pending
//	Customer: POST /v1/commerce/orders/:id/payment/intent → commerce opens
//	          the intent at payments-service for the ORDER's amount
//	Stranger: cannot read the payment, open an intent or confirm
//	Customer: a confirm for the wrong amount or another intent → refused,
//	          order untouched
//	Customer: POST …/payment/confirm gateway=stub → 204, order paid
//	Customer: the same confirm again with another payment id → 204, no change
//
// and that nothing else can:
//
//	Edge    : /v1/payments/* is not routed at all (it used to be, and a user
//	          token could then drive an intent to succeeded)
//	Webhook : payments-service in stub mode has no provider adapter, so
//	          POST /v1/payments/webhook answers 503 and a second, unpaid order
//	          stays unpaid. The signed provider webhook → outbox → consumer
//	          path needs real provider credentials and is not exercised here.
//
// Needs the stack of the full journey (see TestE2E_Commerce_FullJourney) and
// payments-service. ATPOST_PAYMENTS_URL defaults to http://localhost:8102: the
// webhook is dialled directly, as the provider would.
func TestE2E_Commerce_PrepaidStub(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	paymentsURL := envOr("ATPOST_PAYMENTS_URL", "http://localhost:8102")
	SkipIfDown(t, urls.APIGateway, paymentsURL)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	seller := NewHTTPClient(urls.APIGateway, uuid.New())
	customer := NewHTTPClient(urls.APIGateway, uuid.New())
	stranger := NewHTTPClient(urls.APIGateway, uuid.New())
	admin := commerceAdmin(t, urls.APIGateway)
	t.Logf("customer=%s seller=%s", customer.UserID, seller.UserID)

	variantID := commerceLiveListing(t, ctx, admin, seller, "E2E Prepaid Store")
	addressID := commerceAddAddress(t, ctx, customer)

	// ── The order and its intent ──────────────────────────────────
	order := commercePlaceOrder(t, ctx, customer, "confirm-path", variantID, addressID)
	intent := commerceOpenIntent(t, ctx, customer, "confirm-path", order)
	if intent.ClientSession["provider"] != "stub" {
		t.Fatalf("the stack's payment provider is %q; this flow settles only on the stub", intent.ClientSession["provider"])
	}
	t.Logf("order %s total=%d intent %s status=%s", order.OrderID, order.TotalMinor, intent.ID, intent.Status)

	orderPath := "/v1/commerce/orders/" + order.OrderID
	var polled struct {
		OrderStatus   string `json:"order_status"`
		PaymentStatus string `json:"payment_status"`
	}
	if err := json.Unmarshal(must(t, "customer polls the payment status",
		customer.MustDo(t, ctx, "GET", orderPath+"/payment/status", nil)), &polled); err != nil ||
		polled.OrderStatus != "payment_pending" || polled.PaymentStatus != "pending" {
		t.Fatalf("payment status before paying is %s/%s, want payment_pending/pending (err=%v)", polled.OrderStatus, polled.PaymentStatus, err)
	}
	if st, _ := commercePaymentState(t, ctx, customer, order.OrderID); st != "confirming" {
		t.Fatalf("the customer's payment read is %q before paying, want confirming", st)
	}

	// ── A stranger has no part in it ──────────────────────────────
	paymentID := "pay_e2e_" + uuid.NewString()[:8]
	confirm := commerceStubConfirmBody(intent, paymentID, order.TotalMinor)
	commerceRefused(t, "stranger reads the payment",
		stranger.MustDo(t, ctx, "GET", orderPath+"/payment", nil), 403, "FORBIDDEN")
	commerceRefused(t, "stranger opens an intent on the order",
		stranger.MustDo(t, ctx, "POST", orderPath+"/payment/intent", nil), 404, "ORDER_NOT_FOUND")
	commerceRefused(t, "stranger confirms the payment",
		stranger.MustDo(t, ctx, "POST", orderPath+"/payment/confirm", confirm), 403, "FORBIDDEN")

	// ── The customer cannot name their own amount or another intent ──
	commerceRefused(t, "customer confirms for one paisa",
		customer.MustDo(t, ctx, "POST", orderPath+"/payment/confirm", commerceStubConfirmBody(intent, paymentID, 1)),
		400, "PAYMENT_VERIFY_FAILED")
	other := intent
	other.ID = uuid.NewString()
	commerceRefused(t, "customer confirms with an intent that is not the order's",
		customer.MustDo(t, ctx, "POST", orderPath+"/payment/confirm", commerceStubConfirmBody(other, paymentID, order.TotalMinor)),
		400, "PAYMENT_VERIFY_FAILED")
	if s, ps := commerceOrderState(t, ctx, customer, order.OrderID); s != "payment_pending" || ps != "pending" {
		t.Fatalf("the refused confirms moved the order to %s/%s", s, ps)
	}

	// ── The stub confirm settles it, once ─────────────────────────
	must(t, "customer confirms the payment (stub)",
		customer.MustDo(t, ctx, "POST", orderPath+"/payment/confirm", confirm))
	if st, amount := commercePaymentState(t, ctx, customer, order.OrderID); st != "paid" || amount != order.TotalMinor {
		t.Fatalf("payment is %s for %d after the confirm, want paid for %d", st, amount, order.TotalMinor)
	}
	if s, ps := commerceOrderState(t, ctx, customer, order.OrderID); s != "confirmed" || ps != "paid" {
		t.Fatalf("the order is %s/%s after the confirm, want confirmed/paid", s, ps)
	}
	// A replay naming a different payment id answers 204 and changes nothing.
	must(t, "customer confirms again (idempotent)",
		customer.MustDo(t, ctx, "POST", orderPath+"/payment/confirm",
			commerceStubConfirmBody(intent, "pay_e2e_replay_"+uuid.NewString()[:8], order.TotalMinor)))
	if st, amount := commercePaymentState(t, ctx, customer, order.OrderID); st != "paid" || amount != order.TotalMinor {
		t.Fatalf("the second confirm changed the payment to %s for %d", st, amount)
	}
	// A paid order takes no new intent.
	commerceRefused(t, "customer opens an intent on a paid order",
		customer.MustDo(t, ctx, "POST", orderPath+"/payment/intent", nil), 409, "ORDER_NOT_PAYABLE")

	// ── payments-service is not reachable from the edge ───────────
	for _, call := range []struct{ method, path string }{
		{"GET", "/v1/payments/intents/" + intent.ID},
		{"POST", "/v1/payments/intents"},
		{"PATCH", "/v1/payments/intents/" + intent.ID + "/status"},
		{"PATCH", "/v1/payments/internal/intents/" + intent.ID + "/status"},
		{"POST", "/v1/payments/internal/intents/" + intent.ID + "/verify"},
	} {
		env := customer.MustDo(t, ctx, call.method, call.path, map[string]any{"old_status": "pending", "new_status": "succeeded"})
		if env.Status != 404 {
			t.Fatalf("%s %s through the gateway: want 404, got %d", call.method, call.path, env.Status)
		}
	}
	t.Log("no /v1/payments route answers at the edge")

	// ── A webhook cannot settle an order on a stub stack ──────────
	unpaid := commercePlaceOrder(t, ctx, customer, "webhook-path", variantID, addressID)
	unpaidIntent := commerceOpenIntent(t, ctx, customer, "webhook-path", unpaid)
	body, _ := json.Marshal(map[string]any{
		"id": "evt_e2e_" + uuid.NewString()[:8], "event": "payment.captured",
		"payload": map[string]any{"payment": map[string]any{"entity": map[string]any{
			"id": "pay_e2e_wh_" + uuid.NewString()[:8], "order_id": unpaidIntent.ProviderRef,
			"amount": unpaid.TotalMinor, "status": "captured"}}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, paymentsURL+"/v1/payments/webhook", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build webhook: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("webhook: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("webhook on a stub payments-service: want 503 (no provider adapter), got %d", resp.StatusCode)
	}
	time.Sleep(3 * time.Second) // give a (wrong) payment event time to surface
	if s, ps := commerceOrderState(t, ctx, customer, unpaid.OrderID); s != "payment_pending" || ps != "pending" {
		t.Fatalf("an unverifiable webhook moved the order to %s/%s", s, ps)
	}
	t.Log("webhook -> 503, the second order is still unpaid")

	// ── The buyer cancels the paid, unshipped order (com-1) ───────
	// Nothing shipped it by itself (the 3 s above included), so the buyer may
	// still cancel, and a refund is owed.
	if s, ps := commerceOrderState(t, ctx, customer, order.OrderID); s != "confirmed" || ps != "paid" {
		t.Fatalf("the paid order is %s/%s before the cancel, want confirmed/paid: it must not ship by itself", s, ps)
	}
	if env := customer.MustDo(t, ctx, "POST", orderPath+"/cancel", map[string]any{"reason": "e2e: ordered by mistake"}); env.Status != 204 {
		must(t, "customer cancels the paid order", env)
		t.Fatalf("customer cancels the paid order -> %d, want 204", env.Status)
	}
	if s, ps := commerceOrderState(t, ctx, customer, order.OrderID); s != "cancelled" || (ps != "refund_pending" && ps != "refunded") {
		t.Fatalf("the cancelled paid order is %s/%s, want cancelled with a refund owed (refund_pending or refunded)", s, ps)
	}
	t.Log("prepaid stub flow (intent, ownership, stub confirm, replay, closed edge, webhook, buyer cancel) passed")
}
