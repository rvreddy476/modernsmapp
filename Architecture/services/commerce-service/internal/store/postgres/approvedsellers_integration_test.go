//go:build integration

package postgres

// Only approved sellers reach a buyer (founder, 1 Oct 2026).
//
// "Only approved sellers show in the retail ... non-approved or status
// pending should not come into the customer portal."
//
// The hole: every buyer surface asked whether the seller's store_status was
// 'active' — and store_status DEFAULTS to 'active' on a brand-new seller row.
// A seller in draft, submitted, under_review or changes_required, or one an
// admin rejected (which never touches store_status), was therefore "open for
// business", and any listing of theirs that reached active + approved was
// browsable, searchable, cartable and payable.
//
// Each case below is the worst one: a PERFECT listing (active, approved,
// public, priced, in stock, in a category, with a gallery image, a favourite,
// a banner and a cart line) under a seller whose store_status is 'active' and
// whose status is not 'approved'. It must be absent from every store-level
// read a buyer's request goes through. Then the real transitions: approving
// the seller makes the listing appear everywhere, suspending hides it again.
//
//	COMMERCE_TEST_DSN=.../commerce_it_test go test -tags=integration \
//	  ./internal/store/postgres/ -run ApprovedSellers -v -count=1

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// nonApprovedSellerStatuses is every value of sellers.status's CHECK
// (migration 001) except 'approved'.
var nonApprovedSellerStatuses = []string{
	"draft", "submitted", "under_review", "changes_required", "rejected", "suspended", "disabled",
}

type approvedOnlyFixture struct {
	*fixture
	sellerUser uuid.UUID
	categoryID uuid.UUID
	mediaID    uuid.UUID
	bannerID   uuid.UUID
	shopper    uuid.UUID
	title      string
	createdAt  time.Time
}

// newApprovedOnlyFixture is newFixture's listing made perfect and reachable
// from every surface, under a seller left in `sellerStatus` with
// store_status 'active'.
func newApprovedOnlyFixture(t *testing.T, sellerStatus string) *approvedOnlyFixture {
	t.Helper()
	ctx := context.Background()
	f := &approvedOnlyFixture{
		fixture:    newFixture(t, 25, 50_000, "18"),
		categoryID: uuid.New(),
		mediaID:    uuid.New(),
		bannerID:   uuid.New(),
		shopper:    uuid.New(),
	}
	f.title = "Approved-only probe " + f.productID.String()

	mustExec(t, `INSERT INTO product_categories (id,name,slug,display_order,is_active)
	             VALUES ($1,$2,$3,10,TRUE)`, f.categoryID, "AO "+f.categoryID.String()[:8], "ao-"+f.categoryID.String()[:8])
	mustExec(t, `UPDATE products SET title=$2, category_id=$3, visibility='public', published_at=NOW() WHERE id=$1`,
		f.productID, f.title, f.categoryID)
	// A real discount, so the deals rail would carry it if it were visible.
	mustExec(t, `UPDATE product_variants SET mrp_minor=100000, mrp=1000 WHERE id=$1`, f.variantID)
	seedOfferFor(t, f.productID)
	mustExec(t, `INSERT INTO product_media (product_id, media_id, media_type, sort_order) VALUES ($1,$2,'image',0)`,
		f.productID, f.mediaID)
	mustExec(t, `INSERT INTO commerce_favourites (user_id, product_id) VALUES ($1,$2)`, f.shopper, f.productID)
	mustExec(t, `INSERT INTO commerce_banners (id,title,target_type,target_id,position,active)
	             VALUES ($1,'AO probe','product',$2,-2000000000,TRUE)`, f.bannerID, f.productID.String())
	f.addToCart(1, 50_000)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM commerce_banners WHERE id=$1`, f.bannerID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM commerce_favourites WHERE product_id=$1`, f.productID)
		_, _ = testPool.Exec(context.Background(), `UPDATE products SET category_id=NULL WHERE id=$1`, f.productID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM product_categories WHERE id=$1`, f.categoryID)
	})

	if err := testPool.QueryRow(ctx, `SELECT user_id FROM sellers WHERE id=$1`, f.sellerID).Scan(&f.sellerUser); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `SELECT created_at FROM products WHERE id=$1`, f.productID).Scan(&f.createdAt); err != nil {
		t.Fatal(err)
	}
	mustExec(t, `UPDATE sellers SET status=$2, store_status='active' WHERE id=$1`, f.sellerID, sellerStatus)
	return f
}

func containsProduct(ps []*Product, id uuid.UUID) bool {
	for _, p := range ps {
		if p.ID == id {
			return true
		}
	}
	return false
}

// buyerSurfaces answers, for every store read a buyer's request reaches,
// whether this listing is visible (or, for the money paths, sellable) there.
func (f *approvedOnlyFixture) buyerSurfaces(t *testing.T) map[string]bool {
	t.Helper()
	ctx := context.Background()
	s := f.store
	out := map[string]bool{}
	must := func(name string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}

	ps, _, err := s.ListProductsFiltered(ctx, ProductFilter{SellerID: &f.sellerID, Limit: 50})
	must("ListProductsFiltered(seller)", err)
	out["GET /products?seller="] = containsProduct(ps, f.productID)

	ps, _, err = s.ListProductsFiltered(ctx, ProductFilter{CategoryID: &f.categoryID, Limit: 50})
	must("ListProductsFiltered(category)", err)
	out["GET /products?category_id="] = containsProduct(ps, f.productID)

	ps, _, err = s.ListProductsFiltered(ctx, ProductFilter{Query: f.title, InStockOnly: true, MinPrice: 1, Limit: 50})
	must("ListProductsFiltered(q, in_stock, min_price)", err)
	out["GET /products?q=&in_stock&min_price"] = containsProduct(ps, f.productID)

	ps, total, err := s.ListProducts(ctx, &f.categoryID, f.title, 50, 0)
	must("ListProducts", err)
	out["GET /products?offset= (legacy)"] = containsProduct(ps, f.productID) && total == 1

	ps, _, err = s.ListSellerProducts(ctx, f.sellerID, "", true, 50, 0)
	must("ListSellerProducts(public)", err)
	out["GET /sellers/:id/products"] = containsProduct(ps, f.productID)

	ps, err = s.CategoryProducts(ctx, f.categoryID, 50)
	must("CategoryProducts", err)
	out["home: category rail"] = containsProduct(ps, f.productID)

	ps, err = s.NewArrivalProducts(ctx, 200)
	must("NewArrivalProducts", err)
	out["home: new arrivals"] = containsProduct(ps, f.productID)

	cards, err := s.ListCategoryCards(ctx)
	must("ListCategoryCards", err)
	out["GET /categories count"] = false
	for _, c := range cards {
		if c.ID == f.categoryID {
			out["GET /categories count"] = c.ProductCount == 1
		}
	}
	tree, err := s.CategoryTree(ctx, false)
	must("CategoryTree", err)
	out["GET /categories?tree count"] = false
	for _, n := range tree {
		if n.ID == f.categoryID {
			out["GET /categories?tree count"] = n.ProductCount == 1
		}
	}

	favs, _, err := s.ListFavourites(ctx, f.shopper, 100, "")
	must("ListFavourites", err)
	out["GET /favourites"] = containsProduct(favs, f.productID)

	banners, err := s.LiveBanners(ctx, 1000)
	must("LiveBanners", err)
	out["home: product banner"] = false
	for _, b := range banners {
		if b.ID == f.bannerID {
			out["home: product banner"] = true
		}
	}

	visible, owner, err := s.ProductBuyerVisibility(ctx, f.productID)
	must("ProductBuyerVisibility", err)
	out["GET /products/:id (+ media|attributes|variants|reviews|preview)"] = visible
	if owner != f.sellerUser {
		t.Fatalf("ProductBuyerVisibility owner = %s, want the seller's user %s", owner, f.sellerUser)
	}

	media, err := s.VisibleProductMediaIDs(ctx, uuid.Nil, []uuid.UUID{f.mediaID})
	must("VisibleProductMediaIDs(anonymous)", err)
	out["media-access (anonymous shopper)"] = media[f.mediaID]

	life, err := s.GetProductLifecycle(ctx, f.productID)
	must("GetProductLifecycle", err)
	out["search event: published"] = life.Visible()

	doc, err := s.ProductSearchDoc(ctx, f.productID)
	must("ProductSearchDoc", err)
	out["GET /internal/products/:id/search-doc visible"] = doc.Visible

	after := f.createdAt.Add(-time.Millisecond)
	docs, err := s.ListProductSearchDocs(ctx, true, &after, &uuid.Nil, 500)
	must("ListProductSearchDocs", err)
	out["GET /internal/products/search-docs"] = false
	for _, d := range docs {
		if d.ProductID == f.productID {
			out["GET /internal/products/search-docs"] = true
		}
	}

	_, ok, err := s.ProductSaleEligibility(ctx, f.variantID)
	must("ProductSaleEligibility", err)
	out["POST /cart/items"] = ok

	view, err := s.CartViewFor(ctx, f.cartID)
	must("CartViewFor", err)
	out["GET /cart line sellable"] = len(view.Items) == 1 && view.Items[0].Sellable

	_, err = s.PriceCartForQuote(ctx, QuotePricingInput{
		UserID: f.userID, CartID: f.cartID, SellerState: "KA", DestinationState: "KA",
	})
	switch {
	case err == nil:
		out["POST /checkout/quote"] = true
	case errors.Is(err, ErrProductUnavailable):
		out["POST /checkout/quote"] = false
	default:
		t.Fatalf("PriceCartForQuote: unexpected error %v", err)
	}

	tx, err := testPool.Begin(ctx)
	must("begin", err)
	_, _, err = lockAndPriceLines(ctx, tx, []cartLine{{VariantID: f.variantID, ProductID: f.productID, Quantity: 1}})
	_ = tx.Rollback(ctx)
	switch {
	case err == nil:
		out["POST /v2/orders/checkout (locked pricing)"] = true
	case errors.Is(err, ErrProductUnavailable):
		out["POST /v2/orders/checkout (locked pricing)"] = false
	default:
		t.Fatalf("lockAndPriceLines: unexpected error %v", err)
	}
	return out
}

func wantEverywhere(t *testing.T, got map[string]bool, want bool, why string) {
	t.Helper()
	for surface, v := range got {
		if v != want {
			t.Errorf("%s: visible/sellable=%v, want %v — %s", surface, v, want, why)
		}
	}
}

func TestApprovedSellersOnly_ANonApprovedSellersPerfectListingAppearsNowhere(t *testing.T) {
	for _, status := range nonApprovedSellerStatuses {
		t.Run(status, func(t *testing.T) {
			f := newApprovedOnlyFixture(t, status)
			wantEverywhere(t, f.buyerSurfaces(t), false,
				"seller status "+status+" with store_status 'active' must not reach a buyer")

			// The owner still sees their own photographs (listing editor).
			got, err := f.store.VisibleProductMediaIDs(context.Background(), f.sellerUser, []uuid.UUID{f.mediaID})
			if err != nil {
				t.Fatal(err)
			}
			if !got[f.mediaID] {
				t.Error("the seller lost access to their own gallery image")
			}
		})
	}
}

func TestApprovedSellersOnly_ApprovingShowsItAndSuspendingHidesItAgain(t *testing.T) {
	ctx := context.Background()
	f := newApprovedOnlyFixture(t, "submitted")
	admin := uuid.New()

	wantEverywhere(t, f.buyerSurfaces(t), false, "a submitted seller is not approved yet")

	if err := f.store.ApproveSellerByAdmin(ctx, f.sellerID, admin, "ok"); err != nil {
		t.Fatalf("ApproveSellerByAdmin: %v", err)
	}
	wantEverywhere(t, f.buyerSurfaces(t), true, "an approved seller's perfect listing is on every surface")

	if err := f.store.SuspendSellerByAdmin(ctx, f.sellerID, admin, "review", "test"); err != nil {
		t.Fatalf("SuspendSellerByAdmin: %v", err)
	}
	wantEverywhere(t, f.buyerSurfaces(t), false, "a suspended seller's listing leaves every surface")

	if err := f.store.UnsuspendSellerByAdmin(ctx, f.sellerID, admin, "cleared"); err != nil {
		t.Fatalf("UnsuspendSellerByAdmin: %v", err)
	}
	wantEverywhere(t, f.buyerSurfaces(t), true, "lifting the suspension brings it back")
}

// The listing's own visibility is the other half: an approved seller's
// listing that is not PUBLIC is not shown either.
func TestApprovedSellersOnly_APrivateListingOfAnApprovedSellerAppearsNowhere(t *testing.T) {
	f := newApprovedOnlyFixture(t, "approved")
	wantEverywhere(t, f.buyerSurfaces(t), true, "control: approved seller, public listing")
	mustExec(t, `UPDATE products SET visibility='private' WHERE id=$1`, f.productID)
	seedOfferFor(t, f.productID)
	wantEverywhere(t, f.buyerSurfaces(t), false, "visibility 'private' is not for every shopper")
}

// SellerProductIDs is what the seller-level transitions re-announce to search.
func TestApprovedSellersOnly_SellerProductIDsListsTheWholeCatalogue(t *testing.T) {
	f := newApprovedOnlyFixture(t, "approved")
	second := uuid.New()
	mustExec(t, `INSERT INTO products (id,seller_id,title,slug,status,approval_status)
	             VALUES ($1,$2,'AO draft',$3,'draft','draft')`, second, f.sellerID, "ao-d-"+second.String()[:8])
	seedOfferFor(t, second)
	ids, err := f.store.SellerProductIDs(context.Background(), f.sellerID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[uuid.UUID]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	if len(ids) != 2 || !seen[f.productID] || !seen[second] {
		t.Fatalf("SellerProductIDs = %v, want exactly the live product and the draft", ids)
	}
}
