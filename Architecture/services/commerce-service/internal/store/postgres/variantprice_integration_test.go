//go:build integration

package postgres

// A product created through the REAL write path must be charged at its real
// price.
//
// This is the proof that was missing. Every fixture in this suite inserts
// `mrp_minor` and `selling_price_minor` explicitly:
//
//	INSERT INTO product_variants (..., mrp, selling_price, mrp_minor, selling_price_minor, ...)
//
// Production did not. `Store.CreateVariant` wrote only the rupee floats, and
// migration 007 had set the minor columns to DEFAULT 0 rather than NULL — so
// `COALESCE(selling_price_minor, ROUND(selling_price*100))` found a non-NULL
// zero, the float fallback never ran, and checkout priced the variant at
// nothing.
//
// The fixtures were supplying exactly the field production dropped. That is
// the same shape of defect review 2 found in the payments reconciler, and it
// hid here for the same reason: every proof started downstream of the write.
//
// So these tests never hand-write a minor column. They go through
// CreateVariant and UpdateVariant and then read what pricing reads.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/store/postgres/... -v

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// pricedVariant creates a seller, product and variant through the real store
// write path — no minor column is ever written by hand.
func pricedVariant(t *testing.T, mrp, selling float64) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	store := New(testPool)

	sellerID, productID := uuid.New(), uuid.New()
	mustExec(t, `INSERT INTO sellers (id,user_id,store_name,slug,email,state)
	             VALUES ($1,$2,'Price Store',$3,'price@example.test','KA')`,
		sellerID, uuid.New(), "price-"+sellerID.String()[:8])
	mustExec(t, `INSERT INTO products (id,seller_id,title,slug,status,approval_status,return_policy_type)
	             VALUES ($1,$2,'Priced Product',$3,'active','approved','7_days')`,
		productID, sellerID, "priced-"+productID.String()[:8])
	seedOfferFor(t, productID)

	v := &ProductVariant{
		ProductID:    productID,
		SKU:          "SKU-" + uuid.NewString()[:8],
		MRP:          mrp,
		SellingPrice: selling,
		CurrencyCode: "INR",
		Status:       "active",
	}
	if err := store.CreateVariant(ctx, v); err != nil {
		t.Fatalf("CreateVariant: %v", err)
	}
	return v.ID
}

// pricingReads returns exactly what the checkout transaction reads.
func pricingReads(t *testing.T, variantID uuid.UUID) (selling, mrp int64) {
	t.Helper()
	if err := testPool.QueryRow(context.Background(), `
		SELECT COALESCE(NULLIF(selling_price_minor, 0), ROUND(selling_price*100)),
		       COALESCE(NULLIF(mrp_minor, 0), ROUND(mrp*100))
		  FROM product_variants WHERE id = $1`, variantID).Scan(&selling, &mrp); err != nil {
		t.Fatalf("reading the priced variant: %v", err)
	}
	return
}

// THE defect. A seller enters ₹1,299 and the buyer must be charged ₹1,299.
func TestCreatedVariantIsPricedAtWhatTheSellerEntered(t *testing.T) {
	id := pricedVariant(t, 1500.00, 1299.00)

	selling, mrp := pricingReads(t, id)
	if selling != 129900 {
		t.Fatalf("checkout would charge %d paise for a ₹1,299.00 variant. "+
			"A product created through the API is free.", selling)
	}
	if mrp != 150000 {
		t.Fatalf("mrp reads %d paise, want 150000", mrp)
	}

	// And the stored column itself — not just the COALESCE — must hold it,
	// because the fallback is for legacy rows, not for new writes.
	var stored int64
	if err := testPool.QueryRow(context.Background(),
		`SELECT selling_price_minor FROM product_variants WHERE id=$1`, id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 129900 {
		t.Fatalf("selling_price_minor = %d, want 129900 — the write path must populate the "+
			"column pricing reads, not rely on a fallback", stored)
	}
}

// Repricing must move the column pricing reads. Updating only the float left
// checkout charging the OLD price.
func TestRepricingAVariantMovesThePriceCheckoutReads(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	id := pricedVariant(t, 1500.00, 1299.00)

	if err := store.UpdateVariant(ctx, id, map[string]any{"selling_price": 999.50}); err != nil {
		t.Fatalf("UpdateVariant: %v", err)
	}

	selling, _ := pricingReads(t, id)
	if selling != 99950 {
		t.Fatalf("after repricing to ₹999.50 checkout would charge %d paise. "+
			"The float moved and the minor column did not, so the buyer pays the old price.", selling)
	}
}

// Rounding, at the one place it happens.
//
// 12.99 arrives from JSON as 12.989999999999998, and a truncating cast gives
// 1298 — a paisa lost on a price nobody would think to check.
func TestRupeePricesRoundRatherThanTruncate(t *testing.T) {
	cases := []struct {
		rupees float64
		want   int64
	}{
		{12.99, 1299},
		{0.01, 1},
		{1299.00, 129900},
		{99.95, 9995},
		{0.005, 1}, // half a paisa rounds up, deterministically
		{1234567.89, 123456789},
	}
	for _, c := range cases {
		id := pricedVariant(t, c.rupees, c.rupees)
		if selling, _ := pricingReads(t, id); selling != c.want {
			t.Fatalf("₹%.2f stored as %d paise, want %d", c.rupees, selling, c.want)
		}
	}
}

// An optional cost price must stay absent rather than becoming a stated zero.
func TestAnAbsentCostPriceStaysAbsent(t *testing.T) {
	id := pricedVariant(t, 1500.00, 1299.00) // created with CostPrice nil

	var costMinor *int64
	if err := testPool.QueryRow(context.Background(),
		`SELECT cost_price_minor FROM product_variants WHERE id=$1`, id).Scan(&costMinor); err != nil {
		t.Fatal(err)
	}
	if costMinor != nil && *costMinor != 0 {
		t.Fatalf("cost_price_minor = %d for a variant with no cost price", *costMinor)
	}
}

// A patch carrying a non-numeric price is refused, not coerced.
//
// PATCH /variants/:id binds into map[string]any, so a client can send
// anything. Coercing a string or a bool to zero would reprice the variant to
// free — the exact defect this file exists to prevent, arriving by a
// different door.
func TestANonNumericPriceIsRefused(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	id := pricedVariant(t, 1500.00, 1299.00)

	for _, bad := range []any{"1299", true, []any{1299}, map[string]any{"v": 1}} {
		if err := store.UpdateVariant(ctx, id, map[string]any{"selling_price": bad}); err == nil {
			t.Fatalf("a %T price was accepted; a coerced zero prices the variant at free", bad)
		}
	}

	// And the original price survived every refusal.
	if selling, _ := pricingReads(t, id); selling != 129900 {
		t.Fatalf("a refused patch changed the price to %d paise", selling)
	}
}

// The legacy fallback must still work for rows written before this fix — the
// estate that has a float price and a zero minor column.
func TestLegacyRowsWithAZeroMinorColumnFallBackToTheFloat(t *testing.T) {
	id := pricedVariant(t, 1500.00, 1299.00)

	// Reproduce a pre-fix row exactly: float set, minor columns at the
	// DEFAULT 0 that migration 007 installed.
	mustExec(t, `UPDATE product_variants SET selling_price_minor = 0, mrp_minor = 0 WHERE id = $1`, id)

	selling, mrp := pricingReads(t, id)
	if selling != 129900 || mrp != 150000 {
		t.Fatalf("a legacy row priced at selling=%d mrp=%d; the NULLIF fallback is not "+
			"rescuing rows written before the minor columns were populated", selling, mrp)
	}
}

// The CATALOGUE must publish the same money the client reads.
//
// The Android `ProductSummaryDto` expects `min_price_minor`, `mrp_minor` and
// `in_stock`, all integer paise / boolean — correctly, because integer paise
// is the rule everywhere else in this service. The server published
// `min_selling_price`, `min_mrp` and `total_stock` as rupee floats. The names
// never matched, `Paise` defaulted to ZERO, and every product in the app's
// grid rendered as ₹0.
//
// The float pair also slipped past the CI money gate, which matches on field
// NAMES: "MinSellingPrice" does not look like money to the pattern.
func TestCatalogueListPublishesPaiseTheClientCanRead(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	// A product created through the real write path, priced ₹1,299.
	id := pricedVariant(t, 1500.00, 1299.00)
	var productID uuid.UUID
	if err := testPool.QueryRow(ctx,
		`SELECT product_id FROM product_variants WHERE id=$1`, id).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	mustExec(t, `INSERT INTO inventory_items (variant_id,seller_id,total_qty,reserved_qty)
	             SELECT $1, p.seller_id, 5, 0 FROM products p WHERE p.id=$2`, id, productID)

	products, _, err := store.ListProductsFiltered(ctx, ProductFilter{Limit: 50})
	if err != nil {
		t.Fatalf("ListProductsFiltered: %v", err)
	}

	var found *Product
	for _, p := range products {
		if p.ID == productID {
			found = p
			break
		}
	}
	if found == nil {
		t.Fatal("the created product is not in the catalogue listing")
	}

	if found.MinPriceMinor == nil {
		t.Fatal("min_price_minor is absent; the Android client reads exactly this field and " +
			"renders ₹0 without it")
	}
	if *found.MinPriceMinor != 129900 {
		t.Fatalf("min_price_minor = %d, want 129900 (₹1,299.00)", *found.MinPriceMinor)
	}
	if found.MRPMinor == nil || *found.MRPMinor != 150000 {
		t.Fatalf("mrp_minor = %v, want 150000", found.MRPMinor)
	}
	if found.InStock == nil || !*found.InStock {
		t.Fatalf("in_stock = %v, want true for a variant with 5 units free", found.InStock)
	}
}

// A legacy product — float price, minor column still at DEFAULT 0 — must
// advertise its real price in the catalogue, not free.
func TestCatalogueDoesNotAdvertiseLegacyRowsAsFree(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	id := pricedVariant(t, 1500.00, 1299.00)
	var productID uuid.UUID
	_ = testPool.QueryRow(ctx, `SELECT product_id FROM product_variants WHERE id=$1`, id).Scan(&productID)
	mustExec(t, `UPDATE product_variants SET selling_price_minor = 0, mrp_minor = 0 WHERE id = $1`, id)

	products, _, err := store.ListProductsFiltered(ctx, ProductFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range products {
		if p.ID != productID {
			continue
		}
		if p.MinPriceMinor == nil || *p.MinPriceMinor != 129900 {
			t.Fatalf("a legacy row advertises min_price_minor=%v; the catalogue would show it "+
				"as free", p.MinPriceMinor)
		}
		return
	}
	t.Fatal("product not found in the listing")
}
