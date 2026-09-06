//go:build integration

package postgres

// A SKU is one shop's code for its own stock, not a name reserved across the
// whole catalogue.
//
// Migration 031 replaced `UNIQUE(sku)` with
// `UNIQUE NULLS NOT DISTINCT (offer_id, sku)`. These tests are the two halves
// of what that sentence means, and BOTH halves matter: a widening that only
// proves the new thing is allowed has not shown that the old thing is still
// refused, and the failure mode of getting that wrong is a shop able to point
// two listings at one code and no way to tell which one an order was for.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/store/postgres/... \
//	  -run SKU -v -count=1

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// skuSeller creates one shop and returns its id.
func skuSeller(t *testing.T, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	mustExec(t, `INSERT INTO sellers (id,user_id,store_name,slug,email,state,status)
	             VALUES ($1,$2,$3,$4,'sku@example.test','KA','approved')`,
		id, uuid.New(), name, "sku-"+id.String()[:12])
	return id
}

// listWithSKU creates a product carrying exactly this SKU, through the store's
// real create path — so the offer row, the variant's offer_id and the
// inventory row are all written the way production writes them.
func listWithSKU(t *testing.T, store *Store, sellerID uuid.UUID, sku string) (*Product, *ProductVariant, error) {
	t.Helper()
	p := &Product{
		SellerID:         sellerID,
		Title:            "Shared Catalogue Item",
		Slug:             "sku-per-offer-" + uuid.NewString(),
		ProductType:      "physical",
		Condition:        "new",
		Status:           "draft",
		Visibility:       "public",
		ApprovalStatus:   "draft",
		ReturnPolicyType: "7_days",
		ReturnPolicyDays: 7,
	}
	v := &ProductVariant{
		SKU:          sku,
		MRP:          500,
		SellingPrice: 450,
		CurrencyCode: "INR",
		Status:       "active",
	}
	err := store.CreateProductAtomic(context.Background(), NewProduct{
		Product:  p,
		Variants: []NewVariant{{Variant: v, StockQty: 3}},
	})
	return p, v, err
}

// TestTwoSellersMayHoldTheSameSKU is the thing migration 031 exists for.
//
// The SKU here is one both shops would plausibly have chosen on their own —
// an ISBN — because that is the real case. Under `UNIQUE(sku)` the second
// shop's create failed with a duplicate-key error naming a constraint, and
// the seller was told their code was taken by a shop they cannot see, cannot
// contact, and has nothing to do with them.
func TestTwoSellersMayHoldTheSameSKU(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	sku := "ISBN-9780099578512-" + uuid.NewString()[:8]
	first := skuSeller(t, "First Shop")
	second := skuSeller(t, "Second Shop")

	p1, v1, err := listWithSKU(t, store, first, sku)
	if err != nil {
		t.Fatalf("the first shop could not list %s: %v", sku, err)
	}
	p2, v2, err := listWithSKU(t, store, second, sku)
	if err != nil {
		t.Fatalf("the second shop could not list %s: %v\n"+
			"This is exactly what migration 031 removed. If this is a duplicate-key "+
			"error on product_variants_sku_key, the global UNIQUE(sku) is still in "+
			"force — check schema_migrations for 031 in THIS database.", sku, err)
	}

	// Both rows exist, both carry the same string, and they are different
	// variants under different offers. Asserted through the offer rather than
	// through the product, because the offer is what the constraint is keyed
	// on now — a test that only checked the seller ids would still pass if
	// both variants had somehow landed under one offer.
	var offer1, offer2 *uuid.UUID
	if err := testPool.QueryRow(ctx,
		`SELECT offer_id FROM product_variants WHERE id = $1`, v1.ID).Scan(&offer1); err != nil {
		t.Fatalf("read first variant's offer: %v", err)
	}
	if err := testPool.QueryRow(ctx,
		`SELECT offer_id FROM product_variants WHERE id = $1`, v2.ID).Scan(&offer2); err != nil {
		t.Fatalf("read second variant's offer: %v", err)
	}
	if offer1 == nil || offer2 == nil {
		t.Fatalf("a variant created through CreateProductAtomic has no offer_id "+
			"(first=%v second=%v) — linkVariantToOfferTx did not run, and the new "+
			"unique index is keyed on a column nothing fills", offer1, offer2)
	}
	if *offer1 == *offer2 {
		t.Fatalf("both variants landed under offer %s; two shops' listings have been "+
			"conflated", *offer1)
	}

	o1, err := store.GetOfferForProduct(ctx, p1.ID, first)
	if err != nil || o1 == nil {
		t.Fatalf("first shop's offer: %v", err)
	}
	o2, err := store.GetOfferForProduct(ctx, p2.ID, second)
	if err != nil || o2 == nil {
		t.Fatalf("second shop's offer: %v", err)
	}
	if o1.SellerID != first || o2.SellerID != second {
		t.Fatalf("offers are attributed to the wrong shops: %s / %s", o1.SellerID, o2.SellerID)
	}
}

// TestOneOfferStillCannotUseOneSKUTwice is the half that would be easy to
// lose.
//
// Widening a uniqueness rule is one keystroke away from removing it. If the
// index had been created with the default NULLS DISTINCT, or keyed on
// something the create path does not fill, this test is the one that fails
// and the one above still passes.
//
// Two variants of ONE listing, not two listings — see
// TestOneSellerMayReuseASKUAcrossTheirOwnListings below for why that
// distinction is the whole shape of the new rule.
func TestOneOfferStillCannotUseOneSKUTwice(t *testing.T) {
	seller := skuSeller(t, "One Shop")
	sku := "MINE-" + uuid.NewString()[:12]

	p := &Product{
		SellerID:         seller,
		Title:            "Two Variants One Code",
		Slug:             "one-offer-dupe-" + uuid.NewString(),
		ProductType:      "physical",
		Condition:        "new",
		Status:           "draft",
		Visibility:       "public",
		ApprovalStatus:   "draft",
		ReturnPolicyType: "7_days",
		ReturnPolicyDays: 7,
	}
	variant := func() *ProductVariant {
		return &ProductVariant{SKU: sku, MRP: 500, SellingPrice: 450,
			CurrencyCode: "INR", Status: "active"}
	}

	// Refused — and refused as ErrDuplicateSKU, not as a raw pg error,
	// because the seller has to be told which field to change. asDuplicateSKU
	// matches on a constraint NAME CONTAINING "sku"; 031 renamed the index,
	// so this assertion is also what proves that matcher survived the rename.
	err := New(testPool).CreateProductAtomic(context.Background(), NewProduct{
		Product: p,
		Variants: []NewVariant{
			{Variant: variant(), StockQty: 3},
			{Variant: variant(), StockQty: 3},
		},
	})
	if err == nil {
		t.Fatal("one listing took the same SKU on two of its own variants. " +
			"UNIQUE (offer_id, sku) is not in force — either 031 did not apply to this " +
			"database, or the index is keyed on a column the create path leaves NULL.")
	}
	if !errors.Is(err, ErrDuplicateSKU) {
		t.Fatalf("the duplicate was refused, but as %v — not ErrDuplicateSKU.\n"+
			"asDuplicateSKU matches on a constraint name containing \"sku\"; if 031's "+
			"index were named something else the seller would get a 500 instead of "+
			"\"that SKU is already in use\".", err)
	}
}

// TestOneSellerMayReuseASKUAcrossTheirOwnListings pins down the part of 031
// that is wider than "two sellers may share a SKU", so that nobody has to
// discover it from a support ticket.
//
// The key is (offer_id, sku), and an offer is ONE SHOP'S LISTING OF ONE
// CATALOGUE ITEM — so the rule is per listing, not per shop. A seller with
// two separate listings may now put the same code on both. Under
// `UNIQUE(sku)` they could not, and there is no third constraint that
// refuses it.
//
// That is a real loosening and it is deliberate, because the alternative —
// keying on (seller_id, sku) — reintroduces exactly the thing 027 split
// apart: it would make a shop's code global across the shop's whole
// catalogue, and the moment two sellers' offers on ONE shared catalogue item
// are involved it is the wrong grain again. If a seller-wide code turns out
// to be worth enforcing it belongs in the seller's own validation with a
// message they can act on, not in an index that reports a collision with a
// listing the error does not name.
//
// This test exists to FAIL if someone silently narrows the index back, which
// would start refusing listings sellers can create today.
func TestOneSellerMayReuseASKUAcrossTheirOwnListings(t *testing.T) {
	store := New(testPool)
	seller := skuSeller(t, "Reuse Shop")
	sku := "REUSE-" + uuid.NewString()[:12]

	if _, _, err := listWithSKU(t, store, seller, sku); err != nil {
		t.Fatalf("first listing: %v", err)
	}
	if _, _, err := listWithSKU(t, store, seller, sku); err != nil {
		t.Fatalf("the same shop's SECOND listing was refused the same code: %v\n"+
			"031 keys uniqueness on (offer_id, sku), and two listings are two offers, "+
			"so this must be allowed. If it is not, the index has been narrowed to "+
			"something seller-wide and sellers who could list yesterday cannot today.", err)
	}
}

// TestAVariantWithNoOfferStillCannotDuplicateASKU covers the NULLS NOT
// DISTINCT clause specifically.
//
// `product_variants.offer_id` is still nullable — 027 left it that way so a
// pod on an older image could keep inserting — and under the DEFAULT
// (NULLS DISTINCT) every offer-less row compares unequal to every other, so
// they would have had NO sku constraint at all. A migration that was supposed
// to narrow a rule would have quietly removed one for exactly the rows nobody
// is watching.
//
// The two rows here are inserted with raw SQL because there is no code path
// left that produces an offer-less variant; the point is what the DATABASE
// refuses, not what the store does.
func TestAVariantWithNoOfferStillCannotDuplicateASKU(t *testing.T) {
	ctx := context.Background()

	seller := skuSeller(t, "Orphan Shop")
	productID := uuid.New()
	mustExec(t, `
		INSERT INTO products (id,seller_id,title,slug,product_type,condition,status,
		                      visibility,approval_status,return_policy_type,return_policy_days)
		VALUES ($1,$2,'Orphan Variant Product',$3,'physical','new','draft','public','draft','7_days',7)`,
		productID, seller, "orphan-"+productID.String()[:12])
	// The product gets its offer — this fixture must not add to the
	// checker's missing-offer count for every other test in the package.
	seedOfferFor(t, productID)

	// This is the one fixture in the package that deliberately creates
	// offer-less variants, and `variants_without_offer` is the number a later
	// step has to drive to zero before `offer_id` can be made NOT NULL. A test
	// that leaves its own orphans behind is a test that makes that step look
	// impossible. The product cascades to its offer and its variants.
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(),
			`DELETE FROM products WHERE id = $1`, productID); err != nil {
			t.Logf("could not clean up the orphan-variant fixture: %v", err)
		}
	})

	sku := "ORPH-" + uuid.NewString()[:12]
	ins := func() error {
		_, err := testPool.Exec(ctx, `
			INSERT INTO product_variants (id,product_id,sku,mrp,selling_price,
			                              mrp_minor,selling_price_minor,currency_code,status,offer_id)
			VALUES ($1,$2,$3,500,450,50000,45000,'INR','active',NULL)`,
			uuid.New(), productID, sku)
		return err
	}
	if err := ins(); err != nil {
		t.Fatalf("first offer-less variant: %v", err)
	}
	if err := ins(); err == nil {
		t.Fatal("two offer-less variants took the same SKU. The unique index was created " +
			"NULLS DISTINCT, so every variant with a NULL offer_id escapes it entirely — " +
			"031 removed a constraint instead of narrowing one.")
	}
}
