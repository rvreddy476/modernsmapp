//go:build integration

package postgres

// Who can see a seller's unreleased catalogue.
//
// `GET /v1/commerce/sellers/:sellerId/products` takes a seller id from the URL
// and requires no authentication. It called ListSellerProducts with no status
// filter, so it returned every row: drafts the seller had not released,
// products still under review, and ones moderation had rejected. Anyone
// holding a seller id — which every storefront page hands out — could read a
// competitor's pipeline and their moderation failures.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/store/postgres/... -v

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

type catalogueFixture struct {
	sellerID uuid.UUID
	titles   map[string]uuid.UUID // title -> product id
}

// newCatalogueFixture seeds one product in each state a real catalogue holds.
func newCatalogueFixture(t *testing.T) catalogueFixture {
	t.Helper()
	f := catalogueFixture{sellerID: uuid.New(), titles: map[string]uuid.UUID{}}
	mustExec(t, `INSERT INTO sellers (id,user_id,store_name,slug,email,state)
	             VALUES ($1,$2,'Catalogue Store',$3,'cat@example.test','KA')`,
		f.sellerID, uuid.New(), "cat-"+f.sellerID.String()[:8])

	rows := []struct{ title, status, approval string }{
		{"Live Product", "active", "approved"},
		{"Also Live", "active", "approved"},
		{"Secret Draft", "draft", "draft"},
		{"Under Review", "draft", "submitted"},
		{"Rejected By Moderation", "draft", "rejected"},
		{"Paused", "paused", "approved"},
		{"Archived", "archived", "archived"},
	}
	for _, r := range rows {
		id := uuid.New()
		mustExec(t, `INSERT INTO products
		               (id,seller_id,title,slug,status,approval_status,return_policy_type)
		             VALUES ($1,$2,$3,$4,$5,$6,'7_days')`,
			id, f.sellerID, r.title, "cat-"+id.String()[:8], r.status, r.approval)
		f.titles[r.title] = id
	}
	return f
}

func titlesOf(products []*Product) map[string]bool {
	seen := map[string]bool{}
	for _, p := range products {
		seen[p.Title] = true
	}
	return seen
}

// The leak: the public storefront must show only what is actually on sale.
func TestTheStorefrontHidesDraftsAndModerationRejections(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	f := newCatalogueFixture(t)

	products, total, err := store.ListSellerProducts(ctx, f.sellerID, "", true, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	shown := titlesOf(products)

	for _, hidden := range []string{
		"Secret Draft", "Under Review", "Rejected By Moderation", "Paused", "Archived",
	} {
		if shown[hidden] {
			t.Errorf("the public storefront exposed %q — an unauthenticated caller holding "+
				"this seller's id can read their unreleased pipeline and their moderation "+
				"failures", hidden)
		}
	}
	for _, visible := range []string{"Live Product", "Also Live"} {
		if !shown[visible] {
			t.Errorf("the storefront hid %q, which is on sale", visible)
		}
	}
	if total != 2 {
		t.Errorf("total = %d, want 2; a count that includes hidden rows leaks how many "+
			"unreleased products a seller has even when it does not name them", total)
	}
}

// The seller's own dashboard still sees everything — that is the point of
// splitting the two surfaces rather than filtering globally.
func TestTheSellersOwnViewStillShowsEverything(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	f := newCatalogueFixture(t)

	products, total, err := store.ListSellerProducts(ctx, f.sellerID, "", false, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	shown := titlesOf(products)
	for title := range f.titles {
		if !shown[title] {
			t.Errorf("the seller cannot see their own %q; a rejected product they cannot "+
				"see is one they cannot fix", title)
		}
	}
	if total != len(f.titles) {
		t.Errorf("total = %d, want %d", total, len(f.titles))
	}
}

// The status filter still works on the owner view, and its placeholder is
// numbered correctly now that the public predicate sits ahead of it.
func TestTheOwnerViewCanStillFilterByStatus(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	f := newCatalogueFixture(t)

	products, total, err := store.ListSellerProducts(ctx, f.sellerID, "draft", false, 50, 0)
	if err != nil {
		t.Fatalf("status filter: %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d draft products, want 3", total)
	}
	for _, p := range products {
		if p.Status != "draft" {
			t.Fatalf("status filter returned a %q product", p.Status)
		}
	}
}

// The storefront predicate must be the same one browse and search use, or a
// product can be visible on a storefront and invisible in search.
func TestTheStorefrontAgreesWithTheBrowseSurface(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	f := newCatalogueFixture(t)

	storefront, _, err := store.ListSellerProducts(ctx, f.sellerID, "", true, 50, 0)
	if err != nil {
		t.Fatal(err)
	}

	rows, err := testPool.Query(ctx, `
		SELECT title FROM products
		 WHERE seller_id = $1 AND status = 'active' AND approval_status = 'approved'`,
		f.sellerID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	browse := map[string]bool{}
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			t.Fatal(err)
		}
		browse[title] = true
	}

	shown := titlesOf(storefront)
	if len(shown) != len(browse) {
		t.Fatalf("storefront shows %d products, browse shows %d", len(shown), len(browse))
	}
	for title := range browse {
		if !shown[title] {
			t.Fatalf("%q is in browse results but not on its own seller's storefront", title)
		}
	}
}
