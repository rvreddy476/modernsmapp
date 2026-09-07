//go:build integration

package postgres

// Suspending a seller has to take their shop down.
//
// Until 2026-09-07 it did not. AdminSuspendSeller set sellers.status to
// 'suspended' and stopped there — store_status stayed 'active' — and it would
// not have mattered if it had, because nothing a buyer touches read either
// column. productSummaryLive asked whether the OFFER was live and never
// whether the seller still was; the checkout row lock checked the product and
// the variant and never joined sellers at all. So an admin could suspend a
// seller, watch the queue update, and the seller kept listing and kept
// taking money.
//
// Three surfaces had to move together, which is why this test walks all
// three rather than asserting on one:
//
//	the storefront   — productSummaryLive gained a seller clause
//	the search index — ProductLifecycle.Visible() carries the same rule in Go
//	checkout         — the priced-line lock refuses a closed seller
//
//	COMMERCE_TEST_DSN=postgres://…/commerce_it_test go test -tags=integration \
//	  ./internal/store/postgres/... -run SellerSuspension -v -count=1

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestSellerSuspensionClosesTheShopAndUnsuspensionReopensIt(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 10, 50_000, "18")
	admin := uuid.New()

	// Make the listing live, the way an approval would.
	mustExec(t, `UPDATE products SET status='active', approval_status='approved' WHERE id=$1`, f.productID)
	seedOfferFor(t, f.productID)

	visible := func() bool {
		t.Helper()
		got, _, err := f.store.ListProductsFiltered(ctx, ProductFilter{SellerID: &f.sellerID, Limit: 50})
		if err != nil {
			t.Fatalf("ListProductsFiltered: %v", err)
		}
		for _, p := range got {
			if p.ID == f.productID {
				return true
			}
		}
		return false
	}

	indexed := func() bool {
		t.Helper()
		l, err := f.store.GetProductLifecycle(ctx, f.productID)
		if err != nil {
			t.Fatalf("GetProductLifecycle: %v", err)
		}
		return l.Visible()
	}

	sellable := func() bool {
		t.Helper()
		tx, txErr := testPool.Begin(ctx)
		if txErr != nil {
			t.Fatalf("begin: %v", txErr)
		}
		defer tx.Rollback(ctx)
		_, _, err := lockAndPriceLines(ctx, tx, []cartLine{{VariantID: f.variantID, ProductID: f.productID, Quantity: 1}})
		if err == nil {
			return true
		}
		if errors.Is(err, ErrProductUnavailable) {
			return false
		}
		// Any other error is the test's own problem, not an answer.
		t.Fatalf("lockAndPriceLines: unexpected error %v", err)
		return false
	}

	// ── open ────────────────────────────────────────────────────────────
	if !visible() {
		t.Fatal("an active, approved listing under an active seller is not on the storefront")
	}
	if !indexed() {
		t.Fatal("the same listing is not visible to the search index")
	}

	// ── suspended ───────────────────────────────────────────────────────
	if err := f.store.SuspendSellerByAdmin(ctx, f.sellerID, admin, "fraud review", "test"); err != nil {
		t.Fatalf("SuspendSellerByAdmin: %v", err)
	}

	// Both columns, not one. status alone was the original bug.
	var status, storeStatus string
	if err := testPool.QueryRow(ctx,
		`SELECT status, store_status FROM sellers WHERE id=$1`, f.sellerID).
		Scan(&status, &storeStatus); err != nil {
		t.Fatal(err)
	}
	if status != "suspended" || storeStatus != "suspended" {
		t.Fatalf("after suspend: status=%q store_status=%q; want both 'suspended' — "+
			"store_status is the column every buyer-facing read checks", status, storeStatus)
	}

	if visible() {
		t.Error("a suspended seller's listing is still on the storefront")
	}
	if indexed() {
		t.Error("a suspended seller's listing is still visible to the search index")
	}
	if sellable() {
		t.Error("checkout still prices a line from a suspended seller — they are still taking money")
	}

	// ── reopened ────────────────────────────────────────────────────────
	if err := f.store.UnsuspendSellerByAdmin(ctx, f.sellerID, admin, "cleared"); err != nil {
		t.Fatalf("UnsuspendSellerByAdmin: %v", err)
	}
	if err := testPool.QueryRow(ctx,
		`SELECT status, store_status FROM sellers WHERE id=$1`, f.sellerID).
		Scan(&status, &storeStatus); err != nil {
		t.Fatal(err)
	}
	if status != "approved" || storeStatus != "active" {
		t.Fatalf("after unsuspend: status=%q store_status=%q; want 'approved'/'active'", status, storeStatus)
	}
	if !visible() {
		t.Error("the listing did not come back after the suspension was lifted")
	}
	if !sellable() {
		t.Error("checkout still refuses a seller whose suspension was lifted")
	}
}

// Unsuspend is guarded on the current state, so it cannot be used as a
// backdoor approval.
//
// The UPDATE carries `AND status='suspended'`. Without it, calling unsuspend
// on a draft or rejected seller would set them approved and open their
// storefront — the same class of bug as the one this change fixes, pointing
// the other way.
func TestUnsuspendRefusesASellerWhoWasNeverSuspended(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 1, 1000, "18")
	admin := uuid.New()

	mustExec(t, `UPDATE sellers SET status='rejected', store_status='inactive' WHERE id=$1`, f.sellerID)

	if err := f.store.UnsuspendSellerByAdmin(ctx, f.sellerID, admin, "should not work"); err == nil {
		t.Fatal("unsuspend succeeded on a rejected seller — it is a backdoor approval")
	}

	var status, storeStatus string
	if err := testPool.QueryRow(ctx,
		`SELECT status, store_status FROM sellers WHERE id=$1`, f.sellerID).
		Scan(&status, &storeStatus); err != nil {
		t.Fatal(err)
	}
	if status != "rejected" || storeStatus != "inactive" {
		t.Fatalf("the rejected seller moved: status=%q store_status=%q", status, storeStatus)
	}
}
