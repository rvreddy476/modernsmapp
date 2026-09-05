//go:build integration

package http

// seedOfferFor — the one line every raw-SQL product fixture in this package
// now carries.
//
// Migration 027 gave every product a `product_offers` row and the store's
// write paths keep it there: insertProductTx creates it, and the five
// lifecycle transitions sync it. A fixture that reaches past the store and
// INSERTs into `products` directly bypasses all of that, and leaves behind a
// product with no offer.
//
// That matters because the instrument gating the step which moves the
// catalogue readers onto `product_offers` —
// postgres.CheckProductOfferConsistency — walks the WHOLE table. It cannot
// tell a fixture's orphan from a real one written by a pod on an old image,
// and an instrument that reports seventy-five false positives on every test
// run is an instrument nobody will believe when it reports a true one.
//
// So the fixtures do what the migration did: product first, then its offer.
//
// It is an UPSERT, not an insert, because several fixtures also reach past
// the store to UPDATE a product's lifecycle columns directly — parking one at
// 'paused' or 'hidden' to prove a gate refuses it. Calling this again after
// such an UPDATE is the fixture's half of the dual-write the store does for
// itself.

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func seedOfferFor(t *testing.T, productIDs ...uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if _, err := edgePool.Exec(ctx, `
		INSERT INTO product_offers (product_id, seller_id, status, visibility,
		                            approval_status, rejection_reason, published_at, condition)
		SELECT p.id, p.seller_id, p.status, p.visibility,
		       p.approval_status, p.rejection_reason, p.published_at, p.condition
		  FROM products p WHERE p.id = ANY($1)
		 ON CONFLICT (product_id, seller_id) DO UPDATE SET
		       status           = EXCLUDED.status,
		       visibility       = EXCLUDED.visibility,
		       approval_status  = EXCLUDED.approval_status,
		       rejection_reason = EXCLUDED.rejection_reason,
		       published_at     = EXCLUDED.published_at,
		       condition        = EXCLUDED.condition,
		       updated_at       = NOW()`, productIDs); err != nil {
		t.Fatalf("seed offer: %v", err)
	}
	if _, err := edgePool.Exec(ctx, `
		UPDATE product_variants v SET offer_id = o.id
		  FROM product_offers o
		 WHERE v.product_id = ANY($1) AND o.product_id = v.product_id AND v.offer_id IS NULL`,
		productIDs); err != nil {
		t.Fatalf("link variants to offer: %v", err)
	}
}

// productIDOfVariant is for the fixtures that identify a product by the
// variant sitting in a cart rather than by id.
func productIDOfVariant(t *testing.T, variantID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := edgePool.QueryRow(context.Background(),
		`SELECT product_id FROM product_variants WHERE id = $1`, variantID).Scan(&id); err != nil {
		t.Fatalf("product for variant %s: %v", variantID, err)
	}
	return id
}
