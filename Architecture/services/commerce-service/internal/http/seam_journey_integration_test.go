//go:build integration

package http

// The journey with NOTHING seeded past a seam.
//
// Every other suite here builds its world with INSERTs — `seedJourney` writes
// a seller with `state='KA'` and a product with `approval_status='approved'`
// straight into PostgreSQL, then exercises the routes downstream of that. The
// routes it exercises are real, so the tests are honest about what they cover.
// What none of them covered is the step that PRODUCES those rows.
//
// Four launch defects lived in exactly that gap, all found by driving the app's
// own journey against a running server and none reachable by any existing test:
//
//	1. `ApproveProductByAdmin` wrote approval_status='live'. Both sale gates
//	   require 'approved'. Every product the moderation queue approved was
//	   visible in the catalogue and refused at add-to-cart. No test had ever
//	   called that function — the fixtures wrote 'approved' themselves.
//
//	2. `PUT /seller/address` writes `seller_addresses`; the GST place-of-supply
//	   read `sellers.state`, which only the wizard step the app never calls
//	   populates. Every quote from an app-onboarded seller failed. The fixtures
//	   set `sellers.state` in the INSERT, so the column was never empty here.
//
//	3. Checkout resolved the seller's state before the store's idempotency
//	   replay. Resolving it reads the cart, and a retry arrives after the cart
//	   is cleared — so the retry got ErrCartEmpty instead of its order. The
//	   existing idempotency proofs replay a key WITHOUT a preceding successful
//	   checkout having emptied the cart.
//
//	4. `GET /orders` answered `data: [...]` while the client is declared as
//	   `data: {items, next_cursor}`, carrying a rupee column that migration 007
//	   stopped maintaining — so every order rendered as ₹0, if it parsed. The
//	   envelope test asserts `data` exists, which an array satisfies.
//
// So this file has one rule: it may not write to the database except where a
// route genuinely does not exist yet. The single exception is the KYC document
// row, because the route that writes it verifies the media id against
// media-service, which this package has no stub for — and that verification has
// its own two proofs in the onboarding suite.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// realJourney drives a seller from "no account" to "product on sale" using
// only registered HTTP routes, and returns the ids a buyer needs.
type realJourney struct {
	sellerUserID uuid.UUID
	sellerID     uuid.UUID
	productID    uuid.UUID
	variantID    uuid.UUID
}

func openAShopForReal(t *testing.T, r *gin.Engine) realJourney {
	t.Helper()
	ctx := context.Background()
	j := realJourney{sellerUserID: uuid.New()}

	mustCall := func(method, path string, body any, want ...int) []byte {
		t.Helper()
		w := call(t, r, method, path, j.sellerUserID, body)
		for _, code := range want {
			if w.Code == code {
				return w.Body.Bytes()
			}
		}
		t.Fatalf("%s %s: status %d, want one of %v\n%s", method, path, w.Code, want, w.Body.String())
		return nil
	}

	mustCall(http.MethodPost, "/v1/commerce/onboarding/start", map[string]any{
		"store_name": "Seam Shop " + uuid.New().String()[:6],
		"email":      "seam-" + uuid.New().String()[:6] + "@example.test",
	}, http.StatusOK, http.StatusCreated)

	j.sellerID = sellerIDOf(t, j.sellerUserID)

	// The pickup address, through the route the app uses. This is defect 2:
	// it lands in `seller_addresses`, and the tax path used to look elsewhere.
	mustCall(http.MethodPut, "/v1/commerce/seller/address", map[string]any{
		"contact_name": "Warehouse Desk", "phone": "9000000000",
		"address_line_1": "1 Warehouse Rd", "city": "Bengaluru",
		"state": "KA", "postal_code": "560001",
	}, http.StatusNoContent, http.StatusOK)

	mustCall(http.MethodPut, "/v1/commerce/onboarding/step/payout", map[string]any{
		"account_holder_name": "A Seller", "bank_name": "Test Bank",
		"account_number": "000111222333", "ifsc_code": "TEST0000001",
	}, http.StatusOK, http.StatusNoContent)

	// The one permitted INSERT — see the file header.
	if _, err := edgePool.Exec(ctx, `
		INSERT INTO seller_documents (seller_id, document_type, media_id)
		VALUES ($1, 'pan_card', gen_random_uuid())`, j.sellerID); err != nil {
		t.Fatal(err)
	}

	mustCall(http.MethodPost, "/v1/commerce/onboarding/submit", nil,
		http.StatusOK, http.StatusNoContent)

	// Approved by an admin, through the admin route — not by an UPDATE.
	mustCall(http.MethodPost,
		"/v1/commerce/internal/sellers/"+j.sellerID.String()+"/approve",
		map[string]any{"notes": "seam test"}, http.StatusNoContent, http.StatusOK)

	created := mustCall(http.MethodPost, "/v1/commerce/products",
		createBody(t, "Seam Journey Lamp", map[string]any{
			"mrp_minor": 250000, "selling_price_minor": 199900, "stock_qty": 3,
		}), http.StatusCreated)
	j.productID = uuid.MustParse(createdProductID(t, created))

	// A photograph, because the submit gate now requires one: a listing with
	// no image renders as a blank tile in every grid and nothing anywhere
	// reports it as an error, which is why it is the one built-in requirement
	// whose failure is otherwise silent.
	//
	// Written directly rather than through POST …/media for the same reason
	// the PAN document above is: the media route verifies the id against
	// media-service, which is not running in this journey, and this seam is
	// about checkout rather than about media ownership.
	if _, err := edgePool.Exec(ctx, `
		INSERT INTO product_media (product_id, media_id, media_type, sort_order)
		VALUES ($1, gen_random_uuid(), 'image', 0)`, j.productID); err != nil {
		t.Fatal(err)
	}

	mustCall(http.MethodPost,
		"/v1/commerce/products/"+j.productID.String()+"/submit", nil,
		http.StatusNoContent, http.StatusOK)

	// Defect 1 lives here: this used to leave the product unsellable.
	mustCall(http.MethodPost,
		"/v1/commerce/internal/products/"+j.productID.String()+"/approve",
		map[string]any{}, http.StatusNoContent, http.StatusOK)

	if err := edgePool.QueryRow(ctx,
		`SELECT id FROM product_variants WHERE product_id = $1`, j.productID).
		Scan(&j.variantID); err != nil {
		t.Fatal(err)
	}
	return j
}

// TestAnAdminApprovedProductCanActuallyBeBought is defect 1, at its narrowest.
//
// The product is approved through the moderation route and then added to a
// cart. While approval wrote 'live', the catalogue showed it and add-to-cart
// answered "no longer available" — approved and unbuyable at once.
func TestAnAdminApprovedProductCanActuallyBeBought(t *testing.T) {
	r := journeyEngine(t, 4900)
	j := openAShopForReal(t, r)
	buyer := uuid.New()

	if w := call(t, r, http.MethodPost, "/v1/commerce/cart/items", buyer,
		map[string]any{"variant_id": j.variantID.String(), "quantity": 1}); w.Code != http.StatusOK {
		t.Fatalf("an admin-approved product cannot be added to a cart: status %d\n%s\n"+
			"Every product that goes through the moderation queue reaches buyers this way.",
			w.Code, w.Body.String())
	}

	var approval string
	if err := edgePool.QueryRow(context.Background(),
		`SELECT approval_status FROM products WHERE id = $1`, j.productID).Scan(&approval); err != nil {
		t.Fatal(err)
	}
	if approval != "approved" {
		t.Fatalf("the admin approval route left approval_status=%q; both sale gates "+
			"require 'approved', so anything else is approved-and-unsellable", approval)
	}
}

// TestASellerWhoOnlyUsedTheAppCanBeCheckedOutFrom is defect 2.
//
// The seller's ONLY address is the one the app's pickup-address route wrote.
// `sellers.state` is empty for them, as it is for every seller onboarded
// through the app, because the wizard step that fills it is not in the journey.
func TestASellerWhoOnlyUsedTheAppCanBeCheckedOutFrom(t *testing.T) {
	ctx := context.Background()
	r := journeyEngine(t, 4900)
	j := openAShopForReal(t, r)
	buyer := uuid.New()

	var registeredState string
	if err := edgePool.QueryRow(ctx,
		`SELECT COALESCE(state,'') FROM sellers WHERE id = $1`, j.sellerID).Scan(&registeredState); err != nil {
		t.Fatal(err)
	}
	if registeredState != "" {
		t.Fatalf("this seller has sellers.state=%q, so the test is no longer proving "+
			"what it claims: the app's journey must leave it empty", registeredState)
	}

	if w := call(t, r, http.MethodPost, "/v1/commerce/cart/items", buyer,
		map[string]any{"variant_id": j.variantID.String(), "quantity": 2}); w.Code != http.StatusOK {
		t.Fatalf("add to cart: %d\n%s", w.Code, w.Body.String())
	}
	addrID := buyerAddress(t, r, buyer)

	w := call(t, r, http.MethodPost, "/v1/commerce/checkout/quote", buyer,
		map[string]any{"address_id": addrID, "payment_method": "upi"})
	if w.Code != http.StatusOK {
		t.Fatalf("quote: status %d\n%s\nNo seller onboarded through the app can be "+
			"bought from if this fails — the place of supply must come from the "+
			"pickup address they actually gave.", w.Code, w.Body.String())
	}
	q := decode[quoteBody](t, w)

	// KA seller, KA buyer: an intrastate sale. If the place of supply were
	// read as empty, "" != "KA" would make this interstate and charge IGST —
	// the wrong tax, on a real invoice.
	var cgst, sgst, igst int64
	if err := edgePool.QueryRow(ctx, `
		SELECT COALESCE(cgst_minor,0), COALESCE(sgst_minor,0), COALESCE(igst_minor,0)
		  FROM orders WHERE id = $1`,
		checkoutForReal(t, r, buyer, addrID, q)).Scan(&cgst, &sgst, &igst); err != nil {
		t.Fatal(err)
	}
	if igst != 0 || cgst == 0 || sgst == 0 {
		t.Fatalf("a Karnataka seller selling to a Karnataka buyer was taxed "+
			"CGST=%d SGST=%d IGST=%d; an intrastate sale is CGST+SGST and a blank "+
			"place of supply is what makes it look interstate", cgst, sgst, igst)
	}
}

// TestARetriedCheckoutReturnsTheSameOrder is defect 3.
//
// The retry arrives after the winning checkout has cleared the cart — which is
// the ONLY state a real retry can arrive in, and the one the existing
// idempotency proofs never construct.
func TestARetriedCheckoutReturnsTheSameOrder(t *testing.T) {
	r := journeyEngine(t, 4900)
	j := openAShopForReal(t, r)
	buyer := uuid.New()

	if w := call(t, r, http.MethodPost, "/v1/commerce/cart/items", buyer,
		map[string]any{"variant_id": j.variantID.String(), "quantity": 1}); w.Code != http.StatusOK {
		t.Fatalf("add to cart: %d\n%s", w.Code, w.Body.String())
	}
	addrID := buyerAddress(t, r, buyer)
	qw := call(t, r, http.MethodPost, "/v1/commerce/checkout/quote", buyer,
		map[string]any{"address_id": addrID, "payment_method": "upi"})
	if qw.Code != http.StatusOK {
		t.Fatalf("quote: %d\n%s", qw.Code, qw.Body.String())
	}
	q := decode[quoteBody](t, qw)

	idem := "seam-retry-" + uuid.NewString()
	body := map[string]any{
		"address_id": addrID, "quote_id": q.QuoteID,
		"payment_method": "upi", "expected_total_minor": q.TotalMinor,
	}

	first := callWithIdem(t, r, buyer, idem, body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first checkout: %d\n%s", first.Code, first.Body.String())
	}
	firstOrder := decode[checkoutBody](t, first)

	// The client never saw the response — a timeout, a dropped connection —
	// and retries with the same key. Its cart is now empty.
	second := callWithIdem(t, r, buyer, idem, body)
	if second.Code != http.StatusCreated && second.Code != http.StatusOK {
		t.Fatalf("the retry of a timed-out checkout answered %d:\n%s\n"+
			"Idempotency-Key exists for exactly this request. A client told its "+
			"cart is empty rebuilds the cart and buys twice.",
			second.Code, second.Body.String())
	}
	secondOrder := decode[checkoutBody](t, second)
	if secondOrder.OrderID != firstOrder.OrderID {
		t.Fatalf("the retry produced order %s, the original was %s — one key, two orders",
			secondOrder.OrderID, firstOrder.OrderID)
	}
	// The retry's answer must be the original's answer. The replay path
	// selected only the total, so it returned the right amount beside a zero
	// tax and a zero delivery charge — a breakdown that does not add up, on
	// the screen a buyer reads after a dropped connection.
	if secondOrder.TotalMinor != firstOrder.TotalMinor ||
		secondOrder.TaxMinor != firstOrder.TaxMinor {
		t.Fatalf("the retry described the same order differently: "+
			"total %d vs %d, tax %d vs %d",
			secondOrder.TotalMinor, firstOrder.TotalMinor,
			secondOrder.TaxMinor, firstOrder.TaxMinor)
	}

	var orders int
	if err := edgePool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM orders WHERE customer_user_id = $1`, buyer).Scan(&orders); err != nil {
		t.Fatal(err)
	}
	if orders != 1 {
		t.Fatalf("%d orders exist for one buyer who checked out once", orders)
	}
}

// TestTheOrderListIsShapedTheWayTheClientReadsIt is defect 4.
//
// Asserted against the client's declared contract — `data.items[]` with money
// in minor units — not merely "the response has a data key", which an array of
// zero-rupee cards satisfied.
func TestTheOrderListIsShapedTheWayTheClientReadsIt(t *testing.T) {
	r := journeyEngine(t, 4900)
	j := openAShopForReal(t, r)
	buyer := uuid.New()

	if w := call(t, r, http.MethodPost, "/v1/commerce/cart/items", buyer,
		map[string]any{"variant_id": j.variantID.String(), "quantity": 2}); w.Code != http.StatusOK {
		t.Fatalf("add to cart: %d\n%s", w.Code, w.Body.String())
	}
	addrID := buyerAddress(t, r, buyer)
	qw := call(t, r, http.MethodPost, "/v1/commerce/checkout/quote", buyer,
		map[string]any{"address_id": addrID, "payment_method": "upi"})
	q := decode[quoteBody](t, qw)
	checkoutForReal(t, r, buyer, addrID, q)

	w := call(t, r, http.MethodGet, "/v1/commerce/orders", buyer, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("order list: %d\n%s", w.Code, w.Body.String())
	}

	// Exactly Android's OrderListDto: an OBJECT with `items`, not an array.
	page := decode[struct {
		Items []struct {
			ID          string `json:"id"`
			OrderNumber string `json:"order_number"`
			TotalMinor  int64  `json:"total_minor"`
			TaxMinor    int64  `json:"tax_minor"`
			Currency    string `json:"currency"`
		} `json:"items"`
	}](t, w)

	if len(page.Items) != 1 {
		t.Fatalf("the buyer placed one order and the list carries %d; a `data` that is "+
			"an array instead of {items,next_cursor} does not deserialise into the "+
			"client's OrderListDto at all", len(page.Items))
	}
	got := page.Items[0]
	if got.TotalMinor != q.TotalMinor {
		t.Fatalf("the order list shows total_minor=%d for an order charged %d. "+
			"final_amount, which this used to carry, is the rupee column migration 007 "+
			"stopped maintaining — it is 0.00 on every P0 order.",
			got.TotalMinor, q.TotalMinor)
	}
	if got.TaxMinor != q.TaxMinor {
		t.Fatalf("order list tax_minor=%d, charged %d", got.TaxMinor, q.TaxMinor)
	}
	if got.OrderNumber == "" || got.Currency == "" {
		t.Fatalf("order card is missing its identity or currency: %+v", got)
	}
}

// ─── helpers ────────────────────────────────────────────────────────────

func buyerAddress(t *testing.T, r *gin.Engine, buyer uuid.UUID) string {
	t.Helper()
	w := call(t, r, http.MethodPost, "/v1/commerce/addresses", buyer, map[string]any{
		"contact_name": "A Buyer", "phone": "9111111111",
		"address_line_1": "7 Buyer Lane", "city": "Bengaluru",
		"state": "KA", "postal_code": "560002",
	})
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("address: %d\n%s", w.Code, w.Body.String())
	}
	return decode[struct {
		ID string `json:"id"`
	}](t, w).ID
}

func callWithIdem(t *testing.T, r *gin.Engine, buyer uuid.UUID, idem string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/commerce/v2/orders/checkout", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", buyer.String())
	req.Header.Set("Idempotency-Key", idem)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// checkoutForReal places the order and returns its id.
func checkoutForReal(t *testing.T, r *gin.Engine, buyer uuid.UUID, addrID string, q quoteBody) uuid.UUID {
	t.Helper()
	w := callWithIdem(t, r, buyer, "seam-"+uuid.NewString(), map[string]any{
		"address_id": addrID, "quote_id": q.QuoteID,
		"payment_method": "upi", "expected_total_minor": q.TotalMinor,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("checkout: %d\n%s", w.Code, w.Body.String())
	}
	return uuid.MustParse(decode[checkoutBody](t, w).OrderID)
}
