//go:build integration

package service

// Migration 045 — a books-only shop's listings, through the real create,
// patch and submit paths against a live PostgreSQL. The rules themselves are
// unit-tested in sellscope_test.go; this proves each path calls them with the
// seller's real scope.
//
//	COMMERCE_TEST_DSN=.../commerce_it_test go test -tags=integration ./internal/service/ -run BooksOnlyShop -v

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// booksShop turns the gate fixture's shop books-only, flags its category as
// a book category, and puts the fixture on GST 0%.
func booksShop(t *testing.T) (*gateFixture, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	f := newGateFixture(t)
	if _, err := svcTestPool.Exec(ctx, `UPDATE sellers SET sell_scope = 'books_only' WHERE id = $1`, f.sellerID); err != nil {
		t.Fatal(err)
	}
	if _, err := svcTestPool.Exec(ctx, `UPDATE product_categories SET is_book_category = TRUE WHERE id = $1`, f.categoryID); err != nil {
		t.Fatal(err)
	}
	if err := svcTestPool.QueryRow(ctx, `SELECT id FROM tax_classes WHERE name = 'GST 0%'`).Scan(&f.taxClassID); err != nil {
		t.Skipf("no GST 0%% class: %v", err)
	}
	var eighteen uuid.UUID
	if err := svcTestPool.QueryRow(ctx, `SELECT id FROM tax_classes WHERE name = 'GST 18%'`).Scan(&eighteen); err != nil {
		t.Skipf("no GST 18%% class: %v", err)
	}
	return f, eighteen
}

func scopeRefusal(t *testing.T, err error) string {
	t.Helper()
	var b *SellerScopeBooksOnlyError
	if !errors.As(err, &b) {
		t.Fatalf("err = %v, want a SellerScopeBooksOnlyError", err)
	}
	return b.Field
}

func TestBooksOnlyShopCreatesAndSubmitsABookOnGSTZero(t *testing.T) {
	f, _ := booksShop(t)
	p := f.complete()
	if err := f.submit(p); err != nil {
		t.Fatalf("a book on GST 0%% from a books-only shop was refused: %v", err)
	}
}

func TestBooksOnlyShopCannotCreateOutsideBooks(t *testing.T) {
	f, eighteen := booksShop(t)
	ctx := context.Background()
	electronics := uuid.MustParse("00000000-0000-4000-8000-00000000c001")
	cat := f.categoryID
	for _, tc := range []struct {
		category, tax *uuid.UUID
		field         string
	}{
		{&electronics, &f.taxClassID, "category_id"},
		{&cat, &eighteen, "tax_class_id"},
	} {
		_, err := f.svc.CreateProduct(ctx, CreateProductInput{
			SellerID: f.sellerID, SellScope: postgres.SellScopeBooksOnly, ActorUserID: f.userID,
			CategoryID: tc.category, TaxClassID: tc.tax, Title: "Not a book",
			Variants: []CreateVariantInput{{SKU: "NB-" + uuid.NewString()[:8], MRPMinor: 1000, SellingPriceMinor: 900}},
		})
		if got := scopeRefusal(t, err); got != tc.field {
			t.Errorf("refused on %q, want %q", got, tc.field)
		}
	}
}

func TestBooksOnlyShopCannotPatchAListingOutOfBooks(t *testing.T) {
	f, eighteen := booksShop(t)
	ctx := context.Background()
	p := f.complete()
	electronics := uuid.MustParse("00000000-0000-4000-8000-00000000c001")

	_, err := f.svc.UpdateProduct(ctx, UpdateProductInput{ActorUserID: f.userID, ProductID: p.ID,
		Fields: postgres.ProductPatch{CategoryID: &electronics}})
	if got := scopeRefusal(t, err); got != "category_id" {
		t.Fatalf("move to electronics refused on %q", got)
	}
	_, err = f.svc.UpdateProduct(ctx, UpdateProductInput{ActorUserID: f.userID, ProductID: p.ID,
		Fields: postgres.ProductPatch{TaxClassID: &eighteen}})
	if got := scopeRefusal(t, err); got != "tax_class_id" {
		t.Fatalf("move to 18%% refused on %q", got)
	}
}

// A bulk-imported or uncategorised draft saves, and may not go to review.
func TestBooksOnlyShopCannotSubmitAnUncategorisedDraft(t *testing.T) {
	f, _ := booksShop(t)
	p := f.complete()
	if _, err := svcTestPool.Exec(context.Background(), `UPDATE products SET category_id = NULL WHERE id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if got := scopeRefusal(t, f.submit(p)); got != "category_id" {
		t.Fatalf("uncategorised submit refused on %q", got)
	}
}

// The same listing from an all-categories shop is untouched.
func TestAnAllCategoriesShopIsNotHeldToBooks(t *testing.T) {
	f, eighteen := booksShop(t)
	if _, err := svcTestPool.Exec(context.Background(), `UPDATE sellers SET sell_scope = 'all_categories' WHERE id = $1`, f.sellerID); err != nil {
		t.Fatal(err)
	}
	f.taxClassID = eighteen
	if err := f.submit(f.complete()); err != nil {
		t.Fatalf("an all-categories shop was held to the books rules: %v", err)
	}
}
