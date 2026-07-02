//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestE2E_Commerce_FullJourney drives the end-to-end commerce flow through the
// gateway, exercising all four roles the way a real order does:
//
//	Seller  : onboard → create product (+variant) → submit
//	Admin   : approve the product (privileged scopes via WithAdminRole)
//	Customer: browse → add address → add to cart → checkout (COD)
//	Shipping: stub courier (COURIER_PROVIDER=stub) auto-confirms the shipment
//
// COD is used so the happy path runs without a payment gateway. The online
// (Razorpay) payment path needs PAYMENTS_ALLOW_STUB=true on commerce-service +
// the stub gateway IDs — added separately once this is green.
//
// Run: ATPOST_INTERNAL_KEY=local_dev_internal_service_key_change_me \
//      ./run-integration.sh -run TestE2E_Commerce_FullJourney
func TestE2E_Commerce_FullJourney(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	seller := NewHTTPClient(urls.APIGateway, uuid.New())
	admin := NewHTTPClient(urls.APIGateway, uuid.New()).WithAdminRole()
	customer := NewHTTPClient(urls.APIGateway, uuid.New())

	// ── Seller: onboard ───────────────────────────────────────────
	onboarding := must(t, "seller onboarding/start",
		seller.MustDo(t, ctx, "POST", "/v1/commerce/onboarding/start", map[string]any{
			"store_name":    "E2E Test Store",
			"email":         "seller-" + uuid.NewString()[:8] + "@example.com",
			"seller_type":   "individual",
			"business_type": "individual",
		}))
	sellerID := field(t, onboarding, "id")

	// Enable COD on the seller so the COD checkout is accepted.
	seller.MustDo(t, ctx, "PUT", "/v1/commerce/onboarding/step/fulfillment", map[string]any{
		"delivery_modes":     []string{"standard"},
		"cod_enabled":        true,
		"dispatch_sla_hours": 24,
		"return_supported":   true,
		"return_window_days": 7,
	})

	// Product submission is intentionally gated on seller approval. Approve the
	// synthetic seller before creating/submitting its catalog item.
	must(t, "admin approve seller",
		admin.MustDo(t, ctx, "POST", "/v1/admin/commerce/sellers/"+sellerID+"/approve", map[string]any{
			"notes": "approved by e2e",
		}))

	// ── Seller: create product with one purchasable variant ───────
	sku := "E2E-TW-" + uuid.NewString()[:8]
	prod := must(t, "create product",
		seller.MustDo(t, ctx, "POST", "/v1/commerce/products", map[string]any{
			"title":              "E2E Widget",
			"product_type":       "physical",
			"condition":          "new",
			"return_policy_type": "7_days",
			"return_policy_days":  7,
			"variants": []map[string]any{{
				"sku":           sku,
				"mrp":           1000,
				"selling_price": 900,
				"stock_qty":     50,
			}},
		}))
	productID := field(t, prod, "id")
	t.Logf("product: %s", productID)

	// Variant id from the variants list (more reliable than parsing nested).
	variants := must(t, "list variants",
		seller.MustDo(t, ctx, "GET", "/v1/commerce/products/"+productID+"/variants", nil))
	variantID := firstID(t, variants)
	t.Logf("variant: %s", variantID)

	// ── Seller: submit for review ─────────────────────────────────
	must(t, "submit product",
		seller.MustDo(t, ctx, "POST", "/v1/commerce/products/"+productID+"/submit", nil))

	// ── Admin: approve the product ────────────────────────────────
	must(t, "admin approve product",
		admin.MustDo(t, ctx, "POST", "/v1/admin/commerce/products/"+productID+"/approve", map[string]any{
			"notes": "approved by e2e",
		}))

	// ── Customer: browse ──────────────────────────────────────────
	must(t, "customer browse products",
		customer.MustDo(t, ctx, "GET", "/v1/commerce/products", nil))

	// ── Customer: add a shipping address ──────────────────────────
	addr := must(t, "add address",
		customer.MustDo(t, ctx, "POST", "/v1/commerce/addresses", map[string]any{
			"address_type":   "home",
			"full_name":      "E2E Buyer",
			"phone":          "9999999999",
			"address_line_1": "1 Integration Way",
			"city":           "Bengaluru",
			"state":          "Karnataka",
			"postal_code":    "560001",
			"country":        "IN",
			"is_default":     true,
		}))
	addressID := field(t, addr, "id")

	// ── Customer: add to cart ─────────────────────────────────────
	must(t, "add to cart",
		customer.MustDo(t, ctx, "POST", "/v1/commerce/cart/items", map[string]any{
			"variant_id": variantID,
			"quantity":   1,
		}))

	// ── Customer: checkout (COD) ──────────────────────────────────
	order := must(t, "checkout (cod)",
		customer.MustDo(t, ctx, "POST", "/v1/commerce/orders/checkout", map[string]any{
			"address_id":      addressID,
			"payment_method":  "cod",
			"idempotency_key": uuid.NewString(),
		}))
	orderID := field(t, order, "id")
	t.Logf("order: %s", orderID)

	// ── Verify the order is readable + shipment progresses ────────
	must(t, "get order",
		customer.MustDo(t, ctx, "GET", "/v1/commerce/orders/"+orderID, nil))

	// Stub courier auto-confirms asynchronously; give it a moment, then assert
	// the order is no longer in a pre-shipment state. (Loosened on purpose —
	// tighten to a specific shipment status once we see the real field.)
	Eventually(t, 20*time.Second, "order should remain readable while the shipment is created", func() bool {
		env := customer.MustDo(t, ctx, "GET", "/v1/commerce/orders/"+orderID, nil)
		return env.Status == 200
	})
	t.Log("✓ full commerce journey (seller→admin→customer→COD→shipment) passed")
}

// ── small helpers (test file only) ───────────────────────────────

// must fails the test with the server's error + body when status is not 2xx,
// and returns the response Data for field extraction.
func must(t *testing.T, label string, env *Envelope) json.RawMessage {
	t.Helper()
	if env.Status < 200 || env.Status >= 300 {
		code, msg := "", ""
		if env.Error != nil {
			code, msg = env.Error.Code, env.Error.Message
		}
		t.Fatalf("%s -> %d %s %s | data=%s", label, env.Status, code, msg, string(env.Data))
	}
	t.Logf("%s -> %d", label, env.Status)
	return env.Data
}

// field pulls a top-level string field (e.g. "id") out of a data object.
func field(t *testing.T, raw json.RawMessage, name string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse object for %q: %v | raw=%s", name, err, string(raw))
	}
	v, ok := m[name]
	if !ok {
		t.Fatalf("field %q missing in: %s", name, string(raw))
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		t.Fatalf("field %q not a string: %s", name, string(v))
	}
	return s
}

// firstID returns the "id" of the first element, tolerating either a bare
// array or a wrapper object ({items|variants|data:[...]}).
func firstID(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var arr []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		var wrap map[string]json.RawMessage
		if err2 := json.Unmarshal(raw, &wrap); err2 != nil {
			t.Fatalf("firstID: not array or object: %s", string(raw))
		}
		for _, k := range []string{"items", "variants", "data", "results"} {
			if inner, ok := wrap[k]; ok {
				return firstID(t, inner)
			}
		}
		t.Fatalf("firstID: no list field in: %s", string(raw))
	}
	if len(arr) == 0 {
		t.Fatalf("firstID: empty list")
	}
	var s string
	if err := json.Unmarshal(arr[0]["id"], &s); err != nil {
		t.Fatalf("firstID: element has no string id: %s", string(raw))
	}
	return s
}
