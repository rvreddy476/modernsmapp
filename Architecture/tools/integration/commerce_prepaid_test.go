//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestE2E_Commerce_PrepaidStub drives the ONLINE payment path end to end
// through the gateway with the stub gateway (PAYMENTS_ALLOW_STUB=true on
// both commerce-service and payments-service):
//
//	Customer: checkout (upi) → order parks in payment_pending/pending
//	Customer: POST /v1/payments/intents (payee = seller's user id)
//	Stranger: POST …/payment/confirm → 403 (ownership)
//	Customer: POST …/payment/confirm gateway=stub → 204, order paid/confirmed
//	Customer: POST …/payment/confirm again → 204, no-op
//	Customer: PATCH /v1/payments/intents/:id/status → 404 (route gone);
//	          PATCH /v1/payments/internal/… → 403 (gateway admin gate)
//
// then the WEBHOOK path on a second order:
//
//	Razorpay: POST payments-service /v1/payments/webhook payment.captured
//	          (HMAC-signed when ATPOST_RAZORPAY_WEBHOOK_SECRET is set)
//	          → outbox → Kafka → commerce consumer → order paid/confirmed
//	Razorpay: same webhook again (same + new event id) → order unchanged
//
// Needs api-gateway, commerce-service, admin-service, payments-service and
// Kafka. ATPOST_PAYMENTS_URL defaults to http://localhost:8102 (the webhook
// is dialled directly, as Razorpay would — it cannot carry a JWT).
func TestE2E_Commerce_PrepaidStub(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	paymentsURL := envOr("ATPOST_PAYMENTS_URL", "http://localhost:8102")
	SkipIfDown(t, urls.APIGateway, paymentsURL)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	seller := NewHTTPClient(urls.APIGateway, uuid.New())
	admin := NewHTTPClient(urls.APIGateway, uuid.New()).WithAdminRole()
	customer := NewHTTPClient(urls.APIGateway, uuid.New())
	stranger := NewHTTPClient(urls.APIGateway, uuid.New())
	t.Logf("customer=%s seller=%s", customer.UserID, seller.UserID)

	// ── Catalog setup (same as the COD journey) ───────────────────
	onboarding := must(t, "seller onboarding/start",
		seller.MustDo(t, ctx, "POST", "/v1/commerce/onboarding/start", map[string]any{
			"store_name": "E2E Prepaid Store", "email": "seller-" + uuid.NewString()[:8] + "@example.com",
			"seller_type": "individual", "business_type": "individual",
		}))
	sellerID := field(t, onboarding, "id")
	seller.MustDo(t, ctx, "PUT", "/v1/commerce/onboarding/step/fulfillment", map[string]any{
		"delivery_modes": []string{"standard"}, "cod_enabled": false, "dispatch_sla_hours": 24,
		"return_supported": true, "return_window_days": 7,
	})
	must(t, "admin approve seller", admin.MustDo(t, ctx, "POST", "/v1/admin/commerce/sellers/"+sellerID+"/approve", map[string]any{"notes": "e2e"}))
	prod := must(t, "create product", seller.MustDo(t, ctx, "POST", "/v1/commerce/products", map[string]any{
		"title": "E2E Prepaid Widget", "product_type": "physical", "condition": "new",
		"return_policy_type": "7_days", "return_policy_days": 7,
		"variants": []map[string]any{{"sku": "E2E-PP-" + uuid.NewString()[:8], "mrp": 1000, "selling_price": 900, "stock_qty": 50}},
	}))
	productID := field(t, prod, "id")
	variantID := firstID(t, must(t, "list variants", seller.MustDo(t, ctx, "GET", "/v1/commerce/products/"+productID+"/variants", nil)))
	must(t, "submit product", seller.MustDo(t, ctx, "POST", "/v1/commerce/products/"+productID+"/submit", nil))
	must(t, "admin approve product", admin.MustDo(t, ctx, "POST", "/v1/admin/commerce/products/"+productID+"/approve", map[string]any{"notes": "e2e"}))
	addr := must(t, "add address", customer.MustDo(t, ctx, "POST", "/v1/commerce/addresses", map[string]any{
		"address_type": "home", "full_name": "E2E Buyer", "phone": "9999999999", "address_line_1": "1 Integration Way",
		"city": "Bengaluru", "state": "Karnataka", "postal_code": "560001", "country": "IN", "is_default": true,
	}))
	addressID := field(t, addr, "id")

	checkoutPrepaid := func(label string) (orderID string, amountMinor int64) {
		must(t, label+": add to cart", customer.MustDo(t, ctx, "POST", "/v1/commerce/cart/items", map[string]any{"variant_id": variantID, "quantity": 1}))
		order := must(t, label+": checkout (upi)", customer.MustDo(t, ctx, "POST", "/v1/commerce/orders/checkout", map[string]any{
			"address_id": addressID, "payment_method": "upi", "idempotency_key": uuid.NewString(),
		}))
		var o struct {
			ID            string  `json:"id"`
			Status        string  `json:"status"`
			PaymentStatus string  `json:"payment_status"`
			FinalAmount   float64 `json:"final_amount"`
		}
		if err := json.Unmarshal(order, &o); err != nil {
			t.Fatalf("parse order: %v", err)
		}
		if o.Status != "payment_pending" || o.PaymentStatus != "pending" {
			t.Fatalf("%s: fresh prepaid order is %s/%s, want payment_pending/pending", label, o.Status, o.PaymentStatus)
		}
		t.Logf("%s: order %s final_amount=%.2f", label, o.ID, o.FinalAmount)
		return o.ID, int64(math.Round(o.FinalAmount * 100))
	}
	createIntent := func(label, orderID string, amountMinor int64) (intentID, providerRef string) {
		in := must(t, label+": create payment intent", customer.MustDo(t, ctx, "POST", "/v1/payments/intents", map[string]any{
			"payee_id": seller.UserID.String(), "reference_type": "order", "reference_id": orderID,
			"amount_minor": amountMinor, "currency": "INR", "method": "upi",
		}))
		var i struct {
			ID          string `json:"id"`
			ProviderRef string `json:"provider_ref"`
			Status      string `json:"status"`
		}
		if err := json.Unmarshal(in, &i); err != nil || i.ID == "" || i.ProviderRef == "" {
			t.Fatalf("%s: intent response unusable: %s (%v)", label, string(in), err)
		}
		t.Logf("%s: intent %s provider_ref=%s status=%s", label, i.ID, i.ProviderRef, i.Status)
		return i.ID, i.ProviderRef
	}
	// orderState returns (status, payment_status, payment_id). The paid
	// transition lands the order in "confirmed", but the fulfillment worker +
	// stub courier move it on to packed/shipped within milliseconds, so
	// assertions accept any post-payment status (paidState) rather than the
	// transient "confirmed", and use payment_id as the "unchanged" witness.
	orderState := func(orderID string) (status, paymentStatus, paymentID string) {
		env := customer.MustDo(t, ctx, "GET", "/v1/commerce/orders/"+orderID, nil)
		var o struct {
			Status        string `json:"status"`
			PaymentStatus string `json:"payment_status"`
			PaymentID     string `json:"payment_id"`
		}
		_ = json.Unmarshal(env.Data, &o)
		return o.Status, o.PaymentStatus, o.PaymentID
	}
	paidState := func(status string) bool {
		switch status {
		case "confirmed", "packed", "shipped", "out_for_delivery", "delivered":
			return true
		}
		return false
	}

	// ── Path 1: customer confirm ──────────────────────────────────
	orderID, amountMinor := checkoutPrepaid("confirm-path")
	intentID, providerRef := createIntent("confirm-path", orderID, amountMinor)
	confirmBody := map[string]any{
		"payment_intent_id": intentID, "razorpay_order_id": providerRef, "razorpay_payment_id": "pay_e2e_" + uuid.NewString()[:8],
		"razorpay_signature": "stub-signature", "amount_minor": amountMinor, "gateway": "stub",
	}
	if env := stranger.MustDo(t, ctx, "POST", "/v1/commerce/orders/"+orderID+"/payment/confirm", confirmBody); env.Status != 403 {
		t.Fatalf("stranger confirm: want 403, got %d", env.Status)
	}
	t.Log("stranger confirm -> 403")
	must(t, "customer confirm (stub)", customer.MustDo(t, ctx, "POST", "/v1/commerce/orders/"+orderID+"/payment/confirm", confirmBody))
	s, ps, pid := orderState(orderID)
	if !paidState(s) || ps != "paid" || pid != confirmBody["razorpay_payment_id"] {
		t.Fatalf("after confirm: order is %s/%s payment_id=%q, want confirmed+/paid/%s", s, ps, pid, confirmBody["razorpay_payment_id"])
	}
	t.Logf("order after confirm -> %s/%s payment_id=%s", s, ps, pid)
	// Idempotent replay with a DIFFERENT payment id: must 204 and must not
	// overwrite the recorded payment.
	replay := map[string]any{}
	for k, v := range confirmBody {
		replay[k] = v
	}
	replay["razorpay_payment_id"] = "pay_e2e_replay_" + uuid.NewString()[:8]
	must(t, "customer confirm again (idempotent)", customer.MustDo(t, ctx, "POST", "/v1/commerce/orders/"+orderID+"/payment/confirm", replay))
	if s2, ps2, pid2 := orderState(orderID); !paidState(s2) || ps2 != "paid" || pid2 != pid {
		t.Fatalf("second confirm changed the order: %s/%s payment_id %s -> %s", s2, ps2, pid, pid2)
	}
	t.Log("second confirm -> 204, payment unchanged")

	// Intent status via the user-facing route (customer is the payer).
	in := must(t, "customer reads own intent", customer.MustDo(t, ctx, "GET", "/v1/payments/intents/"+intentID, nil))
	if st := field(t, in, "status"); st != "succeeded" {
		t.Fatalf("intent status = %s, want succeeded", st)
	}
	if env := stranger.MustDo(t, ctx, "GET", "/v1/payments/intents/"+intentID, nil); env.Status != 403 {
		t.Fatalf("stranger reads intent: want 403, got %d", env.Status)
	}
	// Service-only mutations must not be reachable with a user JWT.
	patch := map[string]any{"old_status": "pending", "new_status": "succeeded"}
	if env := customer.MustDo(t, ctx, "PATCH", "/v1/payments/intents/"+intentID+"/status", patch); env.Status != 404 {
		t.Fatalf("PATCH /intents/:id/status via user route: want 404, got %d", env.Status)
	}
	if env := customer.MustDo(t, ctx, "PATCH", "/v1/payments/internal/intents/"+intentID+"/status", patch); env.Status != 403 {
		t.Fatalf("PATCH /internal/… with user JWT: want 403 (gateway admin gate), got %d", env.Status)
	}
	if env := customer.MustDo(t, ctx, "POST", "/v1/payments/internal/intents/"+intentID+"/verify", map[string]any{
		"razorpay_order_id": providerRef, "razorpay_payment_id": "x", "razorpay_signature": "x"}); env.Status != 403 {
		t.Fatalf("POST /internal/…/verify with user JWT: want 403, got %d", env.Status)
	}
	t.Log("user JWT cannot reach status PATCH / verify (404 on old path, 403 on /internal)")

	// ── Path 2: Razorpay webhook → outbox → Kafka → consumer ──────
	orderID2, amountMinor2 := checkoutPrepaid("webhook-path")
	_, providerRef2 := createIntent("webhook-path", orderID2, amountMinor2)
	secret := os.Getenv("ATPOST_RAZORPAY_WEBHOOK_SECRET")
	postWebhook := func(eventID, paymentID string) int {
		body, _ := json.Marshal(map[string]any{
			"id": eventID, "event": "payment.captured",
			"payload": map[string]any{"payment": map[string]any{"entity": map[string]any{
				"id": paymentID, "order_id": providerRef2, "amount": amountMinor2, "status": "captured"}}},
		})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, paymentsURL+"/v1/payments/webhook", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if secret != "" {
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write(body)
			req.Header.Set("X-Razorpay-Signature", hex.EncodeToString(mac.Sum(nil)))
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("webhook: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	evt := "evt_e2e_" + uuid.NewString()[:8]
	payID := "pay_e2e_wh_" + uuid.NewString()[:8]
	if code := postWebhook(evt, payID); code != 200 {
		t.Fatalf("webhook payment.captured: want 200, got %d (signed=%v)", code, secret != "")
	}
	Eventually(t, 30*time.Second, "webhook order should become paid via outbox→Kafka→consumer", func() bool {
		s, ps, _ := orderState(orderID2)
		return paidState(s) && ps == "paid"
	})
	s2, ps2, pid2 := orderState(orderID2)
	if pid2 != payID {
		t.Fatalf("webhook order recorded payment_id %q, want %q", pid2, payID)
	}
	t.Logf("webhook order -> %s/%s payment_id=%s", s2, ps2, pid2)

	// Duplicate deliveries: identical event id (payments-service dedups) and a
	// re-delivery under a new id with a NEW payment id (state machine refuses
	// succeeded→succeeded, so no event is published and the order's recorded
	// payment stays the original).
	if code := postWebhook(evt, payID); code != 200 {
		t.Fatalf("duplicate webhook: want 200, got %d", code)
	}
	if code := postWebhook("evt_e2e_redeliver_"+uuid.NewString()[:8], "pay_e2e_dup_"+uuid.NewString()[:8]); code != 200 {
		t.Fatalf("re-delivered webhook: want 200, got %d", code)
	}
	time.Sleep(3 * time.Second) // give a (wrong) second event time to surface
	if s3, ps3, pid3 := orderState(orderID2); !paidState(s3) || ps3 != "paid" || pid3 != payID {
		t.Fatalf("duplicate webhook changed the order: %s/%s payment_id %s -> %s", s3, ps3, payID, pid3)
	}
	t.Log("duplicate + re-delivered webhooks -> 200, order payment unchanged")
	t.Log("✓ prepaid stub journey (confirm path + webhook path + duplicates) passed")
}
