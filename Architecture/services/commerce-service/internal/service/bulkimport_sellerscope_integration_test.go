//go:build integration

package service

// The seller-crossing bulk import.
//
// ─── THE ATTACK ─────────────────────────────────────────────────────────
//
// Seller A lists a variant with SKU "X". Seller B uploads a CSV whose first
// column also reads "X" — a code B chose, or copied, or generated from the
// same manufacturer part number A did, because SKUs are seller-local strings
// and two shops selling the same textbook will collide on ISBN-derived codes
// as a matter of course, not as an exception.
//
// The importer then has to decide: is this row an UPDATE of an existing
// listing, or a NEW one? If it answers that question by SKU alone, B's row
// updates A's product — B's title, B's price, B's stock, written onto a
// listing in A's shop, with A never told and B never aware. That is a
// catalogue takeover through a file upload, and the only reason it is not
// live today is an incidental global `UNIQUE(sku)` that a later step in this
// plan widens to `(offer_id, sku)`.
//
// This test is written against the state AFTER that widening, so that it is
// the code and not the constraint that has to hold the line:
//
//	1. Seller A's product is untouched — title, price and stock.
//	2. Seller B's row is a PER-ROW failure with a reason naming the cause,
//	   not a whole-import abort and not a silent success.
//	3. Nothing of seller B's row is left behind. A row that failed must not
//	   leave a titled, empty, variant-less product sitting in B's catalogue
//	   and counting towards their dashboard.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/service/... \
//	  -run SellerCrossing -v -count=1

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var svcTestPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dsn := os.Getenv("COMMERCE_TEST_DSN")
	if dsn == "" {
		fmt.Println("COMMERCE_TEST_DSN not set; skipping the service integration proofs")
		os.Exit(0)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		fmt.Printf("connect: %v\n", err)
		os.Exit(1)
	}
	svcTestPool = pool
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

// seedSeller inserts one approved seller and returns its id.
func seedSeller(t *testing.T, label string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := svcTestPool.Exec(context.Background(),
		`INSERT INTO sellers (id,user_id,store_name,slug,email,state,status)
		 VALUES ($1,$2,$3,$4,$5,'KA','approved')`,
		id, uuid.New(), "Store "+label, "store-"+id.String()[:12], label+"-"+id.String()[:8]+"@example.test",
	); err != nil {
		t.Fatalf("seed seller %s: %v", label, err)
	}
	return id
}

// importRow is the shape a validated CSV line arrives in.
func importRow(sku, title string, mrp, price float64, stock int) *BulkImportRow {
	return &BulkImportRow{
		RowNumber:    1,
		SKU:          sku,
		Title:        title,
		MRP:          mrp,
		SellingPrice: price,
		StockQty:     stock,
	}
}

// productSnapshot is the three facts the attack would change.
type productSnapshot struct {
	title string
	price float64
	stock int
}

func snapshotBySKU(t *testing.T, sellerID uuid.UUID, sku string) productSnapshot {
	t.Helper()
	var s productSnapshot
	err := svcTestPool.QueryRow(context.Background(), `
		SELECT p.title, v.selling_price, COALESCE(i.total_qty, -1)
		  FROM product_variants v
		  JOIN products p ON p.id = v.product_id
		  LEFT JOIN inventory_items i ON i.variant_id = v.id
		 WHERE v.sku = $1 AND p.seller_id = $2`, sku, sellerID,
	).Scan(&s.title, &s.price, &s.stock)
	if err != nil {
		t.Fatalf("snapshot %s for seller %s: %v", sku, sellerID, err)
	}
	return s
}

func countProducts(t *testing.T, sellerID uuid.UUID) int {
	t.Helper()
	var n int
	if err := svcTestPool.QueryRow(context.Background(),
		`SELECT count(*) FROM products WHERE seller_id = $1`, sellerID).Scan(&n); err != nil {
		t.Fatalf("count products: %v", err)
	}
	return n
}

// TestSellerCrossingImportCannotTouchAnotherSellersProduct is the attack,
// executed.
func TestSellerCrossingImportCannotTouchAnotherSellersProduct(t *testing.T) {
	ctx := context.Background()
	svc := &Service{store: postgres.New(svcTestPool)}

	sellerA := seedSeller(t, "alpha")
	sellerB := seedSeller(t, "bravo")

	// A shared SKU string. Unique per run so repeated runs never collide
	// with each other's leftovers, but the SAME string for both sellers,
	// which is the whole point.
	sku := "XSKU-" + uuid.NewString()[:12]

	// ── Seller A lists it ────────────────────────────────────
	if err := svc.upsertImportRow(ctx, sellerA,
		importRow(sku, "Alpha's Hand-Written Listing", 1000, 900, 42)); err != nil {
		t.Fatalf("seller A's own import must succeed: %v", err)
	}
	before := snapshotBySKU(t, sellerA, sku)
	if before.title != "Alpha's Hand-Written Listing" || before.price != 900 || before.stock != 42 {
		t.Fatalf("seed did not land as written: %+v", before)
	}

	bProductsBefore := countProducts(t, sellerB)

	// ── Seller B uploads the same SKU ────────────────────────
	err := svc.upsertImportRow(ctx, sellerB,
		importRow(sku, "BRAVO TAKEOVER", 1, 1, 0))

	// 1. A's listing is exactly as A left it.
	after := snapshotBySKU(t, sellerA, sku)
	if after != before {
		t.Errorf("SELLER A'S PRODUCT WAS MODIFIED BY SELLER B'S IMPORT\n before: %+v\n after:  %+v",
			before, after)
	}

	// 2. B's row failed, with a reason that names the cause.
	if err == nil {
		t.Fatalf("seller B's row was accepted; it must be reported as a per-row failure")
	}
	if !errors.Is(err, ErrImportSKUOwnedByAnotherSeller) {
		t.Errorf("seller B's row failed with an unclassified error, so the importer cannot\n"+
			"tell the seller WHY the row was rejected: %v", err)
	}

	// 3. Nothing of B's row survives.
	if got := countProducts(t, sellerB); got != bProductsBefore {
		t.Errorf("seller B's failed row left %d product row(s) behind; a row that failed must "+
			"write nothing", got-bProductsBefore)
	}
	var orphan int
	if err := svcTestPool.QueryRow(ctx, `
		SELECT count(*) FROM products p
		 WHERE p.seller_id = $1
		   AND NOT EXISTS (SELECT 1 FROM product_variants v WHERE v.product_id = p.id)`,
		sellerB).Scan(&orphan); err != nil {
		t.Fatalf("orphan count: %v", err)
	}
	if orphan != 0 {
		t.Errorf("seller B has %d variant-less product row(s) — the failed import created a "+
			"listing with nothing in it", orphan)
	}
}

// TestSellerCrossingNegativeControl_UnscopedMatchFindsTheOtherSellersVariant
// executes the defect, so that the assertion above is known to be about
// something.
//
// A test that asserts "seller A's product was not modified" passes just as
// green on code that could never have modified it. This runs the matcher the
// importer must NOT use — SKU alone, no seller predicate, which is what
// `FindVariantBySKUForSeller` degenerates to if the join's seller_id
// condition is dropped — and asserts that it does hand seller B seller A's
// variant. That is the row the old create-versus-update branch would have
// taken as "existing", and updateExistingVariant would then have written
// B's title, price and stock onto it.
func TestSellerCrossingNegativeControl_UnscopedMatchFindsTheOtherSellersVariant(t *testing.T) {
	ctx := context.Background()
	svc := &Service{store: postgres.New(svcTestPool)}

	sellerA := seedSeller(t, "delta")
	sellerB := seedSeller(t, "echo")
	sku := "XSKU-" + uuid.NewString()[:12]

	if err := svc.upsertImportRow(ctx, sellerA, importRow(sku, "Delta's Listing", 500, 450, 3)); err != nil {
		t.Fatalf("seed seller A: %v", err)
	}

	// The unscoped matcher, spelled out.
	var variantID, ownerSeller uuid.UUID
	err := svcTestPool.QueryRow(ctx, `
		SELECT v.id, p.seller_id
		  FROM product_variants v
		  JOIN products p ON p.id = v.product_id
		 WHERE v.sku = $1
		 LIMIT 1`, sku).Scan(&variantID, &ownerSeller)
	if err != nil {
		t.Fatalf("unscoped match: %v", err)
	}
	if ownerSeller != sellerA {
		t.Fatalf("negative control did not reproduce: the unscoped match found seller %s, not A", ownerSeller)
	}

	// And the scoped resolver, asked the same question by seller B, refuses
	// to hand that variant over.
	match, err := svc.store.ResolveSKUForSeller(ctx, sellerB, sku)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if match.Mine() {
		t.Fatalf("the scoped resolver handed seller B a variant; scoping is not in effect")
	}
	if !match.TakenByAnother() || match.OwnerSellerID != sellerA {
		t.Fatalf("the resolver must name seller A as the owner so the row can be refused with a "+
			"reason; got owner=%s", match.OwnerSellerID)
	}
}

// TestSellerCrossingImportStillUpdatesTheSellersOwnSKU is the negative
// control for the test above: the scoping must refuse ANOTHER seller's SKU
// without also refusing the seller's own re-upload, which is the whole
// purpose of the importer.
func TestSellerCrossingImportStillUpdatesTheSellersOwnSKU(t *testing.T) {
	ctx := context.Background()
	svc := &Service{store: postgres.New(svcTestPool)}

	seller := seedSeller(t, "charlie")
	sku := "XSKU-" + uuid.NewString()[:12]

	if err := svc.upsertImportRow(ctx, seller, importRow(sku, "First Upload", 1000, 900, 5)); err != nil {
		t.Fatalf("first upload: %v", err)
	}
	countAfterFirst := countProducts(t, seller)

	if err := svc.upsertImportRow(ctx, seller, importRow(sku, "Second Upload", 1200, 1100, 9)); err != nil {
		t.Fatalf("re-upload of the seller's OWN sku must update, not fail: %v", err)
	}
	got := snapshotBySKU(t, seller, sku)
	if got.title != "Second Upload" || got.price != 1100 || got.stock != 9 {
		t.Errorf("the seller's own re-upload did not update the listing: %+v", got)
	}
	if n := countProducts(t, seller); n != countAfterFirst {
		t.Errorf("re-uploading the same SKU created %d extra product(s); it must update in place",
			n-countAfterFirst)
	}
}
