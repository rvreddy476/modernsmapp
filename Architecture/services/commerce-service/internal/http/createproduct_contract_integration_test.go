//go:build integration

package http

// Creating a product that can actually be bought.
//
// Two things stood between a seller filling in a create form and a buyer
// completing a purchase, and neither was visible from either end:
//
//  1. `tax_class_id` was OPTIONAL, and there was no endpoint listing the
//     classes — so a form had no way to offer the choice and every product
//     created through the API had none. A product with no GST class is not
//     untaxed, it is UNSELLABLE: checkout resolves the rate under a row lock
//     and refuses with PRODUCT_TAX_UNCONFIGURED. The listing went live,
//     appeared in search, sat in a cart, and failed at the last step with an
//     error the seller never saw.
//
//  2. Money entered as a rupee FLOAT. This is the one place a human types the
//     price every subsequent sale is charged at, and `1299.99` arrives as
//     1299.9899999999998.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/http/... -v

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func createBody(t *testing.T, title string, variant map[string]any) map[string]any {
	t.Helper()
	if _, ok := variant["sku"]; !ok {
		variant["sku"] = "SKU-" + uuid.New().String()[:8]
	}
	return map[string]any{
		"title":        title,
		"tax_class_id": gstClass(t),
		"variants":     []map[string]any{variant},
	}
}

func createdProductID(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	if env.Data.ID == "" {
		t.Fatalf("the create response names no product\n%s", body)
	}
	return env.Data.ID
}

// ─── The rate table ────────────────────────────────────────────────────

// There was no way for a client to discover a tax class at all.
func TestTheGstRateTableIsReadable(t *testing.T) {
	r := journeyEngine(t, 4000)

	w := call(t, r, http.MethodGet, "/v1/commerce/tax-classes", uuid.Nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	// Through `data`: every commerce response is enveloped, and a test that
	// read the body directly would agree with a bug rather than the client.
	var env struct {
		Data struct {
			Items []struct {
				ID          string  `json:"id"`
				Name        string  `json:"name"`
				RatePercent float64 `json:"rate_percent"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v\n%s", err, w.Body.String())
	}
	body := env.Data
	if len(body.Items) == 0 {
		t.Fatal("no tax classes; a create form has nothing to offer and every product " +
			"made through the API is one checkout will refuse")
	}
	for _, tc := range body.Items {
		if tc.ID == "" || tc.Name == "" {
			t.Fatalf("a rate row is missing its id or name: %+v", tc)
		}
		if tc.RatePercent < 0 {
			t.Fatalf("negative rate: %+v", tc)
		}
	}
	// Cheapest first. Ordering by name puts "GST 0%" after "GST 28%", which
	// reads as a broken list to anyone scanning it.
	for i := 1; i < len(body.Items); i++ {
		if body.Items[i].RatePercent < body.Items[i-1].RatePercent {
			t.Fatalf("rates are out of order: %v then %v",
				body.Items[i-1].RatePercent, body.Items[i].RatePercent)
		}
	}
}

// ─── The tax class is required ─────────────────────────────────────────

func TestAProductWithNoTaxClassIsRefused(t *testing.T) {
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 1)

	body := createBody(t, "No GST Set", map[string]any{
		"mrp_minor": 129900, "selling_price_minor": 129900, "stock_qty": 5,
	})
	delete(body, "tax_class_id")

	w := call(t, r, http.MethodPost, "/v1/commerce/products", s.sellerUserID, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 — the listing would go live and fail at checkout "+
			"with an error the seller never sees\n%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "TAX_CLASS_REQUIRED") {
		t.Fatalf("the refusal does not name the reason\n%s", w.Body.String())
	}
	// And it says where to get one, because a seller cannot guess a uuid.
	if !strings.Contains(w.Body.String(), "tax-classes") {
		t.Fatalf("the refusal does not say where to find the rates\n%s", w.Body.String())
	}
}

// ─── Money enters as paise ─────────────────────────────────────────────

// The price a seller types is stored exactly, to the paise.
func TestAPriceInPaiseIsStoredExactly(t *testing.T) {
	ctx := context.Background()
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 1)

	// ₹1,299.99 — the case a float cannot hold. As a rupee double this is
	// 1299.9899999999998, and the stored paise then depend on which way the
	// rounding falls.
	const wantMinor = 129999

	w := call(t, r, http.MethodPost, "/v1/commerce/products", s.sellerUserID,
		createBody(t, "Exactly One Two Nine Nine Nine Nine", map[string]any{
			"mrp_minor": wantMinor, "selling_price_minor": wantMinor, "stock_qty": 5,
		}))
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	productID := createdProductID(t, w.Body.Bytes())

	var mrpMinor, sellMinor int64
	if err := edgePool.QueryRow(ctx, `
		SELECT mrp_minor, selling_price_minor FROM product_variants WHERE product_id = $1`,
		productID).Scan(&mrpMinor, &sellMinor); err != nil {
		t.Fatal(err)
	}
	if sellMinor != wantMinor || mrpMinor != wantMinor {
		t.Fatalf("stored mrp=%d selling=%d, want %d paise for both — the price the seller "+
			"typed is not the price the buyer is charged", mrpMinor, sellMinor, wantMinor)
	}
}

// A newly created product is immediately priced, not free.
//
// This is the ₹0 defect in its create-path form: before the minor columns were
// written here, `COALESCE(selling_price_minor, ROUND(selling_price*100))` found
// a non-NULL zero and the float fallback never ran.
func TestANewProductIsNotFree(t *testing.T) {
	ctx := context.Background()
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 1)

	w := call(t, r, http.MethodPost, "/v1/commerce/products", s.sellerUserID,
		createBody(t, "Definitely Not Free", map[string]any{
			"mrp_minor": 250000, "selling_price_minor": 199900, "stock_qty": 3,
		}))
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}

	// What the pricing path actually reads.
	var effective int64
	if err := edgePool.QueryRow(ctx, `
		SELECT COALESCE(NULLIF(v.selling_price_minor, 0), ROUND(v.selling_price * 100))
		  FROM product_variants v
		 WHERE v.product_id = $1`, createdProductID(t, w.Body.Bytes())).Scan(&effective); err != nil {
		t.Fatal(err)
	}
	if effective != 199900 {
		t.Fatalf("the pricing path reads %d paise for a product listed at ₹1,999 — a buyer "+
			"takes it for %v", effective, effective)
	}
}

// The legacy rupee shape still works, so a client written before the minor
// migration does not start failing.
func TestTheLegacyRupeeShapeStillCreates(t *testing.T) {
	ctx := context.Background()
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 1)

	w := call(t, r, http.MethodPost, "/v1/commerce/products", s.sellerUserID,
		createBody(t, "Old Client", map[string]any{
			"mrp": 1299.0, "selling_price": 1299.0, "stock_qty": 2,
		}))
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d — an older client just started failing\n%s", w.Code, w.Body.String())
	}

	var sellMinor int64
	if err := edgePool.QueryRow(ctx,
		`SELECT selling_price_minor FROM product_variants WHERE product_id = $1`,
		createdProductID(t, w.Body.Bytes())).Scan(&sellMinor); err != nil {
		t.Fatal(err)
	}
	if sellMinor != 129900 {
		t.Fatalf("selling_price_minor = %d, want 129900", sellMinor)
	}
}

// When both shapes are sent, paise win. A client migrating one field at a time
// must not have its exact figure silently replaced by the float mirror.
func TestPaiseWinOverRupeesWhenBothAreSent(t *testing.T) {
	ctx := context.Background()
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 1)

	w := call(t, r, http.MethodPost, "/v1/commerce/products", s.sellerUserID,
		createBody(t, "Both Shapes", map[string]any{
			"mrp": 1.0, "selling_price": 1.0,
			"mrp_minor": 129900, "selling_price_minor": 129900,
			"stock_qty": 1,
		}))
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}

	var sellMinor int64
	if err := edgePool.QueryRow(ctx,
		`SELECT selling_price_minor FROM product_variants WHERE product_id = $1`,
		createdProductID(t, w.Body.Bytes())).Scan(&sellMinor); err != nil {
		t.Fatal(err)
	}
	if sellMinor != 129900 {
		t.Fatalf("selling_price_minor = %d; the rupee field overrode the paise one", sellMinor)
	}
}

// The created product carries its stock, so a seller does not have to open the
// stock screen immediately after listing.
func TestANewProductCarriesItsOpeningStock(t *testing.T) {
	ctx := context.Background()
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 1)

	w := call(t, r, http.MethodPost, "/v1/commerce/products", s.sellerUserID,
		createBody(t, "With Stock", map[string]any{
			"mrp_minor": 50000, "selling_price_minor": 50000, "stock_qty": 7,
		}))
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}

	var total int
	if err := edgePool.QueryRow(ctx, `
		SELECT i.total_qty FROM inventory_items i
		  JOIN product_variants v ON v.id = i.variant_id
		 WHERE v.product_id = $1`, createdProductID(t, w.Body.Bytes())).Scan(&total); err != nil {
		t.Fatalf("no inventory row for a new product: %v", err)
	}
	if total != 7 {
		t.Fatalf("opening stock = %d, want 7", total)
	}
}
