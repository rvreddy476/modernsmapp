//go:build integration

package http

// The cart, as the client actually reads it.
//
// `GET /v1/commerce/cart` serialised the internal CartSummary, whose Go field
// names went out as `CartID` / `Items` / {Item, Product, Variant} while the
// Android client asks for `cart_id` and a flat `items[]` in paise. Nothing
// deserialised, so EVERY buyer's cart rendered as empty — the screen worked,
// the API worked, and the two had never been introduced.
//
// Two of the four cart routes also answered 204 where the client's API
// declares a cart, so after an add or a remove the app held no authoritative
// state at all.
//
// These assert the WIRE, key by key, because that is where the defect lived.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/http/... -v

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// cartWire is the client's contract, written out independently of the server
// types so a rename on either side fails here rather than in the app.
type cartWire struct {
	CartID        string `json:"cart_id"`
	SubtotalMinor *int64 `json:"subtotal_minor"`
	ItemCount     int    `json:"item_count"`
	Items         []struct {
		VariantID      string `json:"variant_id"`
		ProductID      string `json:"product_id"`
		Title          string `json:"title"`
		Quantity       int    `json:"quantity"`
		UnitPriceMinor *int64 `json:"unit_price_minor"`
		LineTotalMinor *int64 `json:"line_total_minor"`
		AvailableQty   int    `json:"available_qty"`
		Sellable       bool   `json:"sellable"`
		ImageURL       string `json:"image_url"`
		PriceWasMinor  *int64 `json:"price_was_minor"`
	} `json:"items"`
}

func decodeCart(t *testing.T, body []byte) cartWire {
	t.Helper()
	var env struct {
		Data cartWire `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("the cart body does not parse as the client's contract: %v\n%s", err, body)
	}
	return env.Data
}

// The defect, stated as a test.
func TestTheCartIsNotEmptyOnTheWire(t *testing.T) {
	r := journeyEngine(t, 4000)
	f := seedJourney(t, 5, 129900)

	w := call(t, r, http.MethodGet, "/v1/commerce/cart", f.userID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	cart := decodeCart(t, w.Body.Bytes())

	if len(cart.Items) != 1 {
		t.Fatalf("the cart has %d items on the wire but one in the database — every buyer's "+
			"cart renders as empty\n%s", len(cart.Items), w.Body.String())
	}
	if cart.CartID == "" {
		t.Fatalf("no cart_id\n%s", w.Body.String())
	}
	line := cart.Items[0]
	if line.Title == "" || line.VariantID == "" {
		t.Fatalf("the line carries no title or variant id: %+v", line)
	}
	if line.UnitPriceMinor == nil || *line.UnitPriceMinor != 129900 {
		t.Fatalf("unit_price_minor = %v, want 129900 paise — the old shape sent "+
			"`selling_price: 1299` as a rupee float", line.UnitPriceMinor)
	}
	if line.LineTotalMinor == nil || *line.LineTotalMinor != 129900 {
		t.Fatalf("line_total_minor = %v, want 129900", line.LineTotalMinor)
	}
	if cart.SubtotalMinor == nil || *cart.SubtotalMinor != 129900 {
		t.Fatalf("subtotal_minor = %v, want 129900", cart.SubtotalMinor)
	}
	if cart.ItemCount != 1 {
		t.Fatalf("item_count = %d, want 1", cart.ItemCount)
	}
	if !line.Sellable {
		t.Fatal("a live, approved product is reported as not sellable")
	}
	if line.AvailableQty != 5 {
		t.Fatalf("available_qty = %d, want 5 — the quantity stepper has no ceiling to "+
			"respect", line.AvailableQty)
	}
}

// No rupee floats anywhere in the payload. The old nesting leaked
// `price_snapshot` and `selling_price`, which would have made the cart the one
// surface where money crossed the wire as a float.
func TestTheCartCarriesNoRupeeFloats(t *testing.T) {
	r := journeyEngine(t, 4000)
	f := seedJourney(t, 5, 129900)

	body := call(t, r, http.MethodGet, "/v1/commerce/cart", f.userID, nil).Body.String()
	for _, banned := range []string{
		`"price_snapshot"`, `"selling_price"`, `"mrp"`, `"subtotal"`,
		`"Items"`, `"CartID"`, `"Item"`, `"Variant"`,
	} {
		if strings.Contains(body, banned) {
			t.Errorf("the cart payload still carries %s — either a rupee float or an "+
				"internal Go field name\n%s", banned, body)
		}
	}
}

// Every cart mutation answers with the resulting cart. Two of the four used to
// answer 204, so the app held no authoritative state after an add or remove.
func TestEveryCartMutationAnswersWithTheCart(t *testing.T) {
	r := journeyEngine(t, 4000)
	f := seedJourney(t, 5, 129900)

	// Remove the seeded line, then add it back through the API.
	w := call(t, r, http.MethodDelete, "/v1/commerce/cart/items/"+f.variantID.String(), f.userID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE returned %d, want 200 with the cart\n%s", w.Code, w.Body.String())
	}
	if got := decodeCart(t, w.Body.Bytes()); len(got.Items) != 0 {
		t.Fatalf("the cart still has %d items after a remove", len(got.Items))
	}

	w = call(t, r, http.MethodPost, "/v1/commerce/cart/items", f.userID,
		map[string]any{"variant_id": f.variantID.String(), "quantity": 2})
	if w.Code != http.StatusOK {
		t.Fatalf("POST returned %d, want 200 with the cart — a 204 leaves the app with "+
			"nothing to render\n%s", w.Code, w.Body.String())
	}
	added := decodeCart(t, w.Body.Bytes())
	if len(added.Items) != 1 || added.Items[0].Quantity != 2 {
		t.Fatalf("the add response does not show the added line: %+v", added.Items)
	}
	if added.ItemCount != 2 {
		t.Fatalf("item_count = %d, want 2 — the cart badge reads this", added.ItemCount)
	}

	w = call(t, r, http.MethodPatch,
		"/v1/commerce/cart/items/by-variant/"+f.variantID.String(), f.userID,
		map[string]any{"quantity": 3})
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH returned %d\n%s", w.Code, w.Body.String())
	}
	if got := decodeCart(t, w.Body.Bytes()); got.ItemCount != 3 {
		t.Fatalf("item_count = %d after a quantity change, want 3", got.ItemCount)
	}
}

// A moved catalogue price is surfaced, so the buyer is not ambushed by
// checkout's PRICE_CHANGED refusal.
func TestAMovedPriceIsShownOnTheCartLine(t *testing.T) {
	ctx := context.Background()
	r := journeyEngine(t, 4000)
	f := seedJourney(t, 5, 129900)

	if _, err := edgePool.Exec(ctx,
		`UPDATE product_variants SET selling_price_minor = 149900, selling_price = 1499
		  WHERE id = $1`, f.variantID); err != nil {
		t.Fatal(err)
	}

	cart := decodeCart(t, call(t, r, http.MethodGet, "/v1/commerce/cart", f.userID, nil).Body.Bytes())
	if len(cart.Items) != 1 {
		t.Fatalf("cart has %d items", len(cart.Items))
	}
	line := cart.Items[0]
	if line.UnitPriceMinor == nil || *line.UnitPriceMinor != 149900 {
		t.Fatalf("unit_price_minor = %v, want the CURRENT 149900 — showing the stale "+
			"snapshot sets an expectation checkout then refuses", line.UnitPriceMinor)
	}
	if line.PriceWasMinor == nil || *line.PriceWasMinor != 129900 {
		t.Fatalf("price_was_minor = %v, want 129900 — the buyer gets no warning before "+
			"checkout refuses with PRICE_CHANGED", line.PriceWasMinor)
	}
}

// A product that left the catalogue is marked, rather than silently priced.
func TestAnUnsellableLineIsMarked(t *testing.T) {
	ctx := context.Background()
	r := journeyEngine(t, 4000)
	f := seedJourney(t, 5, 129900)

	if _, err := edgePool.Exec(ctx,
		`UPDATE products SET status = 'paused'
		  WHERE id = (SELECT product_id FROM product_variants WHERE id = $1)`,
		f.variantID); err != nil {
		t.Fatal(err)
	}
	seedOfferFor(t, productIDOfVariant(t, f.variantID))

	cart := decodeCart(t, call(t, r, http.MethodGet, "/v1/commerce/cart", f.userID, nil).Body.Bytes())
	if len(cart.Items) != 1 {
		t.Fatalf("cart has %d items", len(cart.Items))
	}
	if cart.Items[0].Sellable {
		t.Fatal("a paused product is still reported as sellable; checkout will refuse it " +
			"with no warning on the cart screen")
	}
}
