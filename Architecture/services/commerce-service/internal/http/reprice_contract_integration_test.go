//go:build integration

package http

// Changing the price of a listing that is already selling.
//
// Repricing has one rule: the column checkout READS must move, and the two
// money columns must never end up disagreeing. `selling_price` is a NUMERIC
// mirror; `selling_price_minor` is what the pricing path resolves. A seller
// who lowers a price and moves only the mirror has changed nothing a buyer
// will ever be charged.
//
// The direction changed with the create path. Paise are the authority on the
// way in now, because a price entered exactly as 129999 must not become a
// float on its way through an edit.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/http/... -v

import (
	"context"
	"net/http"
	"testing"
)

// listAt creates a product at a price and returns its variant id.
func listAt(t *testing.T, r interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, s sellerSurface, minor int64) string {
	t.Helper()
	w := call(t, r, http.MethodPost, "/v1/commerce/products", s.sellerUserID,
		createBody(t, "Repriceable", map[string]any{
			"mrp_minor": minor, "selling_price_minor": minor, "stock_qty": 5,
		}))
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status %d\n%s", w.Code, w.Body.String())
	}
	var variantID string
	if err := edgePool.QueryRow(context.Background(),
		`SELECT id FROM product_variants WHERE product_id = $1`,
		createdProductID(t, w.Body.Bytes())).Scan(&variantID); err != nil {
		t.Fatal(err)
	}
	return variantID
}

// effectivePrice is what the pricing path actually resolves.
func effectivePrice(t *testing.T, variantID string) int64 {
	t.Helper()
	var minor int64
	if err := edgePool.QueryRow(context.Background(), `
		SELECT COALESCE(NULLIF(selling_price_minor, 0), ROUND(selling_price * 100))
		  FROM product_variants WHERE id = $1`, variantID).Scan(&minor); err != nil {
		t.Fatal(err)
	}
	return minor
}

// The capability: a seller changes what buyers pay.
func TestRepricingInPaiseMovesWhatTheBuyerPays(t *testing.T) {
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 1)
	variantID := listAt(t, r, s, 129900)

	w := call(t, r, http.MethodPatch, "/v1/commerce/variants/"+variantID, s.sellerUserID,
		map[string]any{"selling_price_minor": 99900})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if got := effectivePrice(t, variantID); got != 99900 {
		t.Fatalf("the pricing path reads %d paise after a reprice to ₹999 — the buyer is "+
			"charged the price from before the change", got)
	}
}

// Both columns move together, always. One of them being stale is the whole
// defect this path exists to prevent.
func TestBothMoneyColumnsMoveTogether(t *testing.T) {
	ctx := context.Background()
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 1)
	variantID := listAt(t, r, s, 129900)

	if w := call(t, r, http.MethodPatch, "/v1/commerce/variants/"+variantID, s.sellerUserID,
		map[string]any{"selling_price_minor": 99900}); w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}

	var mirror float64
	var minor int64
	if err := edgePool.QueryRow(ctx,
		`SELECT selling_price, selling_price_minor FROM product_variants WHERE id = $1`,
		variantID).Scan(&mirror, &minor); err != nil {
		t.Fatal(err)
	}
	if minor != 99900 {
		t.Fatalf("selling_price_minor = %d, want 99900", minor)
	}
	if mirror != 999.0 {
		t.Fatalf("selling_price = %v, want 999 — the NUMERIC mirror is stale and every "+
			"analytics reader still scanning it sees the old price", mirror)
	}
}

// A legacy client sending rupees still reprices, and still moves the minor
// column with it.
func TestTheLegacyRupeeShapeStillReprices(t *testing.T) {
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 1)
	variantID := listAt(t, r, s, 129900)

	if w := call(t, r, http.MethodPatch, "/v1/commerce/variants/"+variantID, s.sellerUserID,
		map[string]any{"selling_price": 799.50}); w.Code != http.StatusOK {
		t.Fatalf("status %d — an older client just started failing\n%s", w.Code, w.Body.String())
	}
	if got := effectivePrice(t, variantID); got != 79950 {
		t.Fatalf("the pricing path reads %d paise, want 79950", got)
	}
}

// The exact price survives an edit. This is why paise are the authority on the
// way in: ₹1,299.99 through a rupee float is 1299.9899999999998.
func TestAnExactPriceSurvivesAnEdit(t *testing.T) {
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 1)
	variantID := listAt(t, r, s, 100000)

	if w := call(t, r, http.MethodPatch, "/v1/commerce/variants/"+variantID, s.sellerUserID,
		map[string]any{"selling_price_minor": 129999}); w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if got := effectivePrice(t, variantID); got != 129999 {
		t.Fatalf("stored %d paise, want exactly 129999", got)
	}
}

// ─── Refusals ──────────────────────────────────────────────────────────

// Both shapes, disagreeing. There is no reading of this that is safe to guess.
func TestDisagreeingPriceShapesAreRefused(t *testing.T) {
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 1)
	variantID := listAt(t, r, s, 129900)

	w := call(t, r, http.MethodPatch, "/v1/commerce/variants/"+variantID, s.sellerUserID,
		map[string]any{"selling_price": 1299, "selling_price_minor": 99900})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 — the server silently chose what the buyer pays\n%s",
			w.Code, w.Body.String())
	}
	if got := effectivePrice(t, variantID); got != 129900 {
		t.Fatalf("price moved to %d despite the refusal", got)
	}
}

// Agreeing is fine — a client migrating one field at a time sends both.
func TestAgreeingPriceShapesAreAccepted(t *testing.T) {
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 1)
	variantID := listAt(t, r, s, 129900)

	if w := call(t, r, http.MethodPatch, "/v1/commerce/variants/"+variantID, s.sellerUserID,
		map[string]any{"selling_price": 999, "selling_price_minor": 99900}); w.Code != http.StatusOK {
		t.Fatalf("status %d — two forms of the same price were rejected\n%s",
			w.Code, w.Body.String())
	}
	if got := effectivePrice(t, variantID); got != 99900 {
		t.Fatalf("stored %d paise, want 99900", got)
	}
}

// A price of zero gives the stock away. Checkout would happily charge nothing.
func TestAPriceCannotBeSetToZero(t *testing.T) {
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 1)
	variantID := listAt(t, r, s, 129900)

	for _, body := range []map[string]any{
		{"selling_price_minor": 0},
		{"selling_price": 0},
		{"selling_price_minor": -100},
	} {
		w := call(t, r, http.MethodPatch, "/v1/commerce/variants/"+variantID, s.sellerUserID, body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%v returned %d, want 400 — the seller's stock is now free\n%s",
				body, w.Code, w.Body.String())
		}
	}
	if got := effectivePrice(t, variantID); got != 129900 {
		t.Fatalf("price moved to %d despite the refusals", got)
	}
}

// A fraction of a paise is not money.
func TestFractionalPaiseAreRefused(t *testing.T) {
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 1)
	variantID := listAt(t, r, s, 129900)

	w := call(t, r, http.MethodPatch, "/v1/commerce/variants/"+variantID, s.sellerUserID,
		map[string]any{"selling_price_minor": 99900.5})
	if w.Code == http.StatusOK {
		t.Fatal("a fractional paise was accepted; the rounding this path exists to remove " +
			"is back")
	}
	if got := effectivePrice(t, variantID); got != 129900 {
		t.Fatalf("price moved to %d despite the refusal", got)
	}
}

// Another seller cannot reprice your catalogue — and is told so as 403, not
// as an internal error.
func TestAnotherSellerCannotRepriceYourProduct(t *testing.T) {
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 1)
	variantID := listAt(t, r, s, 129900)

	w := call(t, r, http.MethodPatch, "/v1/commerce/variants/"+variantID, s.otherUserID,
		map[string]any{"selling_price_minor": 1})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403\n%s", w.Code, w.Body.String())
	}
	if got := effectivePrice(t, variantID); got != 129900 {
		t.Fatalf("another seller repriced this product to %d", got)
	}
}

// Non-money fields still update, and a body of only non-money fields does not
// trip the pricing rules.
func TestNonMoneyFieldsStillUpdate(t *testing.T) {
	ctx := context.Background()
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 1)
	variantID := listAt(t, r, s, 129900)

	if w := call(t, r, http.MethodPatch, "/v1/commerce/variants/"+variantID, s.sellerUserID,
		map[string]any{"weight_grams": 750}); w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var weight int
	if err := edgePool.QueryRow(ctx,
		`SELECT weight_grams FROM product_variants WHERE id = $1`, variantID).Scan(&weight); err != nil {
		t.Fatal(err)
	}
	if weight != 750 {
		t.Fatalf("weight_grams = %d, want 750", weight)
	}
	// And the price is untouched.
	if got := effectivePrice(t, variantID); got != 129900 {
		t.Fatalf("a weight change moved the price to %d", got)
	}
}
