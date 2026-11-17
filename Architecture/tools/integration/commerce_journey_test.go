//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestE2E_Commerce_FullJourney drives one order end to end through the
// gateway, with every role acting the way it does in the product:
//
//	Seller  : open a shop (draft) → draft a listing → attach an image → submit
//	Admin   : approve the shop, then the listing, on admin-service's own
//	          routes with an admin console session (never a consumer token)
//	Customer: address → cart → quote → checkout (UPI) → pay
//	Payment : commerce opens the intent at payments-service; the dev stack
//	          settles through the stub confirm, which payments-service verifies
//	Shipping: the fulfilment worker books the stub courier; the order ships
//
// It has two halves. The first needs only api-gateway and commerce-service
// and pins what an unapproved shop cannot do and who cannot approve it, with
// exact codes. The second needs an admin (ATPOST_E2E_ADMIN_USER, see
// commerceAdmin), identity-auth for the permission lookup and media-service
// for the listing's image; without the admin it stops there.
//
// Cash on delivery is not a launch payment method, so the journey is prepaid
// (it used to be COD "so the happy path runs without a payment gateway").
func TestE2E_Commerce_FullJourney(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	seller := NewHTTPClient(urls.APIGateway, uuid.New())
	customer := NewHTTPClient(urls.APIGateway, uuid.New())
	anonymous := NewHTTPClient(urls.APIGateway, uuid.Nil)

	// ── Seller: a shop that is not approved ───────────────────────
	sellerID := commerceOpenShop(t, ctx, seller, "E2E Test Store")
	// A shop with no KYC documents and no payout account cannot put itself
	// in the review queue.
	commerceRefused(t, "seller submits an incomplete application",
		seller.MustDo(t, ctx, "POST", "/v1/commerce/onboarding/submit", nil), 409, "APPLICATION_INCOMPLETE")

	commerceRefused(t, "create product without a GST class",
		seller.MustDo(t, ctx, "POST", "/v1/commerce/products",
			commerceProductBody("E2E No Tax Widget", "E2E-NT-"+uuid.NewString()[:8], "")), 400, "TAX_CLASS_REQUIRED")
	productID, variantID := commerceDraftProduct(t, ctx, seller, "E2E Widget")
	t.Logf("seller=%s product=%s variant=%s", sellerID, productID, variantID)

	// The completeness gate comes before everything else on submit, and a
	// listing with no image is not complete.
	incomplete := seller.MustDo(t, ctx, "POST", "/v1/commerce/products/"+productID+"/submit", nil)
	commerceRefused(t, "submit a listing with no image", incomplete, 422, "PRODUCT_INCOMPLETE")
	if !strings.Contains(string(incomplete.Error.Details), `"listing.image"`) {
		t.Fatalf("PRODUCT_INCOMPLETE does not name listing.image: %s", string(incomplete.Error.Details))
	}

	// ── Nobody but an admin console session approves a shop ───────
	approvePath := "/v1/admin/commerce/sellers/" + sellerID + "/approve"
	approveBody := map[string]any{"reason": "e2e", "notes": "must be refused"}
	commerceRefused(t, "approve seller, not signed in",
		anonymous.MustDo(t, ctx, "POST", approvePath, approveBody), 401, "ADMIN_SESSION_REQUIRED")
	// A consumer token is not read on /v1/admin at all, whatever scopes it
	// claims: this is the call the flow used to make.
	commerceRefused(t, "approve seller, consumer token claiming admin scopes",
		seller.WithAdminRole().MustDo(t, ctx, "POST", approvePath, approveBody), 401, "ADMIN_SESSION_REQUIRED")
	commerceRefused(t, "approve seller, consumer token in the admin cookie",
		commerceConsoleClient(urls.APIGateway, mintToken(seller.UserID, "admin superadmin"), true).
			MustDo(t, ctx, "POST", approvePath, approveBody), 401, "WRONG_SESSION")
	commerceRefused(t, "approve seller, admin session without the CSRF header",
		commerceConsoleClient(urls.APIGateway, commerceSessionToken(uuid.New(), "admin"), false).
			MustDo(t, ctx, "POST", approvePath, approveBody), 403, "CSRF_FAILED")
	// commerce-service's own admin routes are service-only: the edge refuses
	// every /internal/ path, for the seller as for anyone.
	for _, p := range []string{
		"/v1/commerce/internal/sellers/" + sellerID + "/approve",
		"/v1/commerce/internal/admin/sellers/" + sellerID + "/approve",
	} {
		commerceRefused(t, "approve seller on "+p, seller.MustDo(t, ctx, "POST", p, approveBody), 404, "NOT_FOUND")
	}
	// An admin session is no use anywhere else either.
	commerceRefused(t, "admin session on a shopper route",
		NewHTTPClient(urls.APIGateway, uuid.Nil).
			WithHeader("Authorization", "Bearer "+commerceSessionToken(uuid.New(), "admin")).
			MustDo(t, ctx, "GET", "/v1/commerce/cart", nil), 401, "WRONG_SESSION")

	if st := commerceShopStatus(t, ctx, seller); st != "draft" {
		t.Fatalf("the shop is %q after the refused approvals, want draft", st)
	}
	// And an unapproved shop's listing does not exist for a shopper.
	commerceRefused(t, "shopper reads an unapproved listing",
		customer.MustDo(t, ctx, "GET", "/v1/commerce/products/"+productID, nil), 404, "PRODUCT_NOT_FOUND")
	commerceRefused(t, "shopper adds an unapproved listing to the cart",
		customer.MustDo(t, ctx, "POST", "/v1/commerce/cart/items", map[string]any{"variant_id": variantID, "quantity": 1}),
		409, "PRODUCT_UNAVAILABLE")
	t.Log("the shop awaits approval: no shopper sees it and no non-admin caller can approve it")

	// ── Admin: approve the shop, then the listing ─────────────────
	admin := commerceAdmin(t, urls.APIGateway)
	// A console session whose user holds no role is refused by admin-service
	// itself, on the permission identity resolves for it.
	commerceRefused(t, "approve seller, admin session with no role",
		commerceConsoleClient(urls.APIGateway, commerceSessionToken(uuid.New(), "admin"), true).
			MustDo(t, ctx, "POST", approvePath, approveBody), 403, "PERMISSION_DENIED")

	// With its image the listing is complete, and the next gate is the shop.
	commerceAttachImage(t, ctx, seller, productID)
	commerceRefused(t, "submit a complete listing from an unapproved shop",
		seller.MustDo(t, ctx, "POST", "/v1/commerce/products/"+productID+"/submit", nil), 409, "SELLER_NOT_APPROVED")

	must(t, "admin approves the seller", commerceApprove(t, ctx, admin, "sellers", sellerID))
	if st := commerceShopStatus(t, ctx, seller); st != "approved" {
		t.Fatalf("the shop is %q after the admin approved it, want approved", st)
	}

	must(t, "submit product", seller.MustDo(t, ctx, "POST", "/v1/commerce/products/"+productID+"/submit", nil))
	// Submitted is not live: products queue for a moderator on this stack
	// (COMMERCE_PRODUCT_AUTO_APPROVE is off in every committed environment).
	commerceRefused(t, "shopper reads a submitted, unapproved listing",
		customer.MustDo(t, ctx, "GET", "/v1/commerce/products/"+productID, nil), 404, "PRODUCT_NOT_FOUND")
	must(t, "admin approves the product", commerceApprove(t, ctx, admin, "products", productID))
	must(t, "shopper reads the live listing",
		customer.MustDo(t, ctx, "GET", "/v1/commerce/products/"+productID, nil))
	must(t, "customer browses products", customer.MustDo(t, ctx, "GET", "/v1/commerce/products", nil))

	// ── Customer: address, cart, quote, checkout ──────────────────
	addressID := commerceAddAddress(t, ctx, customer)
	q := commerceCartAndQuote(t, ctx, customer, "journey", variantID, addressID)
	t.Logf("quote %s: subtotal=%d shipping=%d total=%d courier=%s", q.QuoteID, q.SubtotalMinor, q.ShippingMinor, q.TotalMinor, q.CourierCode)

	const checkout = "/v1/commerce/v2/orders/checkout"
	commerceRefused(t, "checkout without an Idempotency-Key",
		customer.MustDo(t, ctx, "POST", checkout, commerceCheckoutBody(addressID, q, "upi")), 400, "IDEMPOTENCY_KEY_REQUIRED")
	commerceRefused(t, "checkout, cash on delivery",
		customer.WithHeader("Idempotency-Key", uuid.NewString()).
			MustDo(t, ctx, "POST", checkout, commerceCheckoutBody(addressID, q, "cod")), 400, "COD_NOT_SUPPORTED")

	buyer := customer.WithHeader("Idempotency-Key", uuid.NewString())
	var order, replay commerceOrder
	if err := json.Unmarshal(must(t, "checkout (upi)",
		buyer.MustDo(t, ctx, "POST", checkout, commerceCheckoutBody(addressID, q, "upi"))), &order); err != nil || order.OrderID == "" {
		t.Fatalf("checkout answer unusable (err=%v)", err)
	}
	if order.TotalMinor != q.TotalMinor {
		t.Fatalf("order total %d, want the quoted %d", order.TotalMinor, q.TotalMinor)
	}
	// The same key and body is the same order, not a second one.
	if err := json.Unmarshal(must(t, "checkout again, same Idempotency-Key",
		buyer.MustDo(t, ctx, "POST", checkout, commerceCheckoutBody(addressID, q, "upi"))), &replay); err != nil || replay.OrderID != order.OrderID {
		t.Fatalf("a retried checkout answered order %q, want the first order %s (err=%v)", replay.OrderID, order.OrderID, err)
	}
	if s, ps := commerceOrderState(t, ctx, customer, order.OrderID); s != "payment_pending" || ps != "pending" {
		t.Fatalf("a fresh prepaid order is %s/%s, want payment_pending/pending", s, ps)
	}
	t.Logf("order: %s total=%d", order.OrderID, order.TotalMinor)

	// ── Payment: intent at payments-service, settled by the stub ──
	intent := commerceOpenIntent(t, ctx, customer, "journey", order)
	if intent.ClientSession["provider"] != "stub" {
		t.Fatalf("the stack's payment provider is %q; this flow settles only on the stub", intent.ClientSession["provider"])
	}
	paymentID := "pay_e2e_" + uuid.NewString()[:8]
	must(t, "customer confirms the payment (stub)",
		customer.MustDo(t, ctx, "POST", "/v1/commerce/orders/"+order.OrderID+"/payment/confirm",
			commerceStubConfirmBody(intent, paymentID, order.TotalMinor)))
	if st, amount := commercePaymentState(t, ctx, customer, order.OrderID); st != "paid" || amount != order.TotalMinor {
		t.Fatalf("payment is %s for %d after the confirm, want paid for %d", st, amount, order.TotalMinor)
	}
	if s, ps := commerceOrderState(t, ctx, customer, order.OrderID); !commercePaidStatus(s) || ps != "paid" {
		t.Fatalf("the order is %s/%s after payment, want a paid status", s, ps)
	}

	// ── Fulfilment: the worker books the stub courier ─────────────
	Eventually(t, 45*time.Second, "the paid order should ship once the fulfilment worker books the stub courier", func() bool {
		s, _ := commerceOrderState(t, ctx, customer, order.OrderID)
		return s == "shipped" || s == "out_for_delivery" || s == "delivered"
	})
	shipRaw := must(t, "customer reads the shipments",
		customer.MustDo(t, ctx, "GET", "/v1/commerce/orders/"+order.OrderID+"/shipments", nil))
	var ships struct {
		Shipments []struct {
			Shipment struct {
				Courier       string `json:"courier"`
				SellerID      string `json:"seller_id"`
				SellerManaged bool   `json:"seller_managed"`
			} `json:"shipment"`
		} `json:"shipments"`
	}
	if err := json.Unmarshal(shipRaw, &ships); err != nil || len(ships.Shipments) != 1 {
		t.Fatalf("want exactly one shipment for a one-seller order (err=%v raw=%s)", err, string(shipRaw))
	}
	if sh := ships.Shipments[0].Shipment; sh.Courier != "stub" || sh.SellerID != sellerID || sh.SellerManaged {
		t.Fatalf("shipment is courier=%q seller=%s seller_managed=%v, want the stub courier's booking for seller %s",
			sh.Courier, sh.SellerID, sh.SellerManaged, sellerID)
	}
	must(t, "seller reads the order",
		seller.MustDo(t, ctx, "GET", "/v1/commerce/seller/orders/"+order.OrderID, nil))
	// A courier-tracked parcel is delivered by the courier's scan, never by
	// the seller's word.
	commerceRefused(t, "seller marks a courier-tracked parcel delivered",
		seller.MustDo(t, ctx, "POST", "/v1/commerce/seller/orders/"+order.OrderID+"/mark-delivered", nil),
		409, "SHIPMENT_NOT_SELLER_MANAGED")
	t.Log("full commerce journey (seller, admin, customer, stub payment, shipment) passed")
}
