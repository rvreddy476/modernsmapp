//go:build integration

package postgres

// The dual-write, and the instrument that says whether it held.
//
// Migration 027 split the seller's OFFER out of the catalogue row and
// backfilled it 1:1. Nothing reads the new table; everything writes it. The
// value of that arrangement is entirely in whether the second copy is
// actually correct, and the only thing that can answer that over an estate
// which has been through a rolling deploy is a checker run against the whole
// table.
//
// So there are two kinds of test here, and the second one matters more:
//
//	1. Each write path, exercised, with both copies read back. These say
//	   the dual-write works when it is called.
//	2. The checker, over EVERY product in the database. This says nothing
//	   has been writing the legacy columns behind the dual-write's back —
//	   which is the failure a per-path test cannot see, because the path it
//	   cannot see is the one nobody remembered to test.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/store/postgres/... \
//	  -run Offer -v -count=1

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// seedOfferFor — the one line every raw-SQL product fixture in this package
// now carries.
//
// Migration 027 gave every product a `product_offers` row and the store's
// write paths keep it there: insertProductTx creates it, and the five
// lifecycle transitions sync it. A fixture that INSERTs into `products`
// directly bypasses all of that and leaves a product with no offer.
//
// That matters because CheckProductOfferConsistency — the instrument gating
// the step which moves the catalogue readers onto `product_offers` — walks
// the WHOLE table. It cannot tell a fixture's orphan from a real one written
// by a pod on an old image, and an instrument that reports dozens of false
// positives on every test run is one nobody will believe when it reports a
// true one.
//
// So the fixtures do what the migration did: product first, then its offer.
//
// It is an UPSERT, not an insert, because several fixtures also reach past
// the store to UPDATE a product's lifecycle columns directly — parking one at
// 'paused' or 'hidden' to prove a gate refuses it. Calling this again after
// such an UPDATE is the fixture's half of the dual-write the store does for
// itself.
func seedOfferFor(t *testing.T, productIDs ...uuid.UUID) {
	t.Helper()
	mustExec(t, `
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
		       updated_at       = NOW()`, productIDs)
	mustExec(t, `
		UPDATE product_variants v SET offer_id = o.id
		  FROM product_offers o
		 WHERE v.product_id = ANY($1) AND o.product_id = v.product_id AND v.offer_id IS NULL`,
		productIDs)
}

// offerFixture is one seller with one product created THROUGH THE STORE —
// not through raw SQL, because the whole question is whether the store's
// write paths maintain the offer.
type offerFixture struct {
	t         *testing.T
	store     *Store
	sellerID  uuid.UUID
	productID uuid.UUID
	variantID uuid.UUID
}

func newOfferFixture(t *testing.T) *offerFixture {
	t.Helper()
	ctx := context.Background()
	f := &offerFixture{t: t, store: New(testPool), sellerID: uuid.New()}

	mustExec(t, `INSERT INTO sellers (id,user_id,store_name,slug,email,state,status)
	             VALUES ($1,$2,'Offer Store',$3,'offer@example.test','KA','approved')`,
		f.sellerID, uuid.New(), "offer-store-"+f.sellerID.String()[:12])

	p := &Product{
		SellerID:         f.sellerID,
		Title:            "Offer Fixture Product",
		Slug:             "offer-fixture-" + uuid.NewString(),
		ProductType:      "physical",
		Condition:        "new",
		Status:           "draft",
		Visibility:       "public",
		ApprovalStatus:   "draft",
		ReturnPolicyType: "7_days",
		ReturnPolicyDays: 7,
	}
	v := &ProductVariant{
		SKU:          "OFR-" + uuid.NewString()[:12],
		MRP:          500,
		SellingPrice: 450,
		CurrencyCode: "INR",
		Status:       "active",
	}
	if err := f.store.CreateProductAtomic(ctx, NewProduct{
		Product:  p,
		Variants: []NewVariant{{Variant: v, StockQty: 7}},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	f.productID, f.variantID = p.ID, v.ID
	return f
}

// offer reads back the seller's offer, failing if there is none.
func (f *offerFixture) offer() *ProductOffer {
	f.t.Helper()
	o, err := f.store.GetOfferForProduct(context.Background(), f.productID, f.sellerID)
	if err != nil {
		f.t.Fatalf("read offer: %v", err)
	}
	if o == nil {
		f.t.Fatalf("product %s has no offer for its own seller", f.productID)
	}
	return o
}

// product reads back the legacy row the offer is supposed to be shadowing.
func (f *offerFixture) product() *Product {
	f.t.Helper()
	p, err := f.store.GetProductByID(context.Background(), f.productID)
	if err != nil || p == nil {
		f.t.Fatalf("read product: %v", err)
	}
	return p
}

// assertAgrees is the dual-write's contract, in one place: the five fields
// the checker asserts over the whole estate, asserted here for one product.
func (f *offerFixture) assertAgrees(stage string) {
	f.t.Helper()
	p, o := f.product(), f.offer()
	if o.SellerID != p.SellerID {
		f.t.Errorf("%s: offer seller %s, product seller %s", stage, o.SellerID, p.SellerID)
	}
	if o.Status != p.Status {
		f.t.Errorf("%s: offer status %q, product status %q", stage, o.Status, p.Status)
	}
	if o.Visibility != p.Visibility {
		f.t.Errorf("%s: offer visibility %q, product visibility %q", stage, o.Visibility, p.Visibility)
	}
	if o.ApprovalStatus != p.ApprovalStatus {
		f.t.Errorf("%s: offer approval_status %q, product approval_status %q",
			stage, o.ApprovalStatus, p.ApprovalStatus)
	}
	if !sameInstant(o.PublishedAt, p.PublishedAt) {
		f.t.Errorf("%s: offer published_at %v, product published_at %v",
			stage, fmtTime(o.PublishedAt), fmtTime(p.PublishedAt))
	}
}

func sameInstant(a, b *time.Time) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return a.Equal(*b)
	}
}

func fmtTime(t *time.Time) string {
	if t == nil {
		return "NULL"
	}
	return t.Format(time.RFC3339Nano)
}

// TestOfferDualWrite_CreateWritesTheOfferAndLinksTheVariant covers the two
// create paths at their single shared statement.
func TestOfferDualWrite_CreateWritesTheOfferAndLinksTheVariant(t *testing.T) {
	f := newOfferFixture(t)
	f.assertAgrees("after create")

	o := f.offer()
	if o.Condition != "new" {
		t.Errorf("offer condition %q; the create's value must be carried, not defaulted", o.Condition)
	}
	if o.HandlingTimeDays != nil {
		t.Errorf("offer handling_time_days %v; nothing supplies it yet and a number nobody chose "+
			"is worse than a NULL", *o.HandlingTimeDays)
	}

	var offerID *uuid.UUID
	if err := testPool.QueryRow(context.Background(),
		`SELECT offer_id FROM product_variants WHERE id=$1`, f.variantID).Scan(&offerID); err != nil {
		t.Fatalf("read variant offer_id: %v", err)
	}
	if offerID == nil {
		t.Fatal("the variant was created with no offer_id")
	}
	if *offerID != o.ID {
		t.Errorf("variant points at offer %s, its seller's offer is %s", *offerID, o.ID)
	}
}

// TestOfferDualWrite_EveryLifecycleTransitionMovesBothCopies walks the five
// statements in this package that change a product's lifecycle columns.
//
// Five, and the list is not a guess: they are every `UPDATE products` in the
// package that names status, visibility, approval_status, rejection_reason or
// published_at. If a sixth is added and this test is not extended, the
// whole-estate checker below is what catches it.
func TestOfferDualWrite_EveryLifecycleTransitionMovesBothCopies(t *testing.T) {
	ctx := context.Background()
	f := newOfferFixture(t)
	actor := uuid.New()

	// 1. submit  (SubmitProductForReview — draft → submitted)
	if err := f.store.SubmitProductForReview(ctx, f.productID, f.sellerID); err != nil {
		t.Fatalf("submit: %v", err)
	}
	f.assertAgrees("after submit")
	if got := f.offer().ApprovalStatus; got != "submitted" {
		t.Errorf("after submit the offer says %q", got)
	}

	// 2. approve (ApproveProductByAdmin — sets status, approval_status AND
	//    published_at, the transition that would show a withdrawn listing as
	//    live if the copies parted)
	if err := f.store.ApproveProductByAdmin(ctx, f.productID, actor, "looks fine"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	f.assertAgrees("after approve")
	o := f.offer()
	if o.ApprovalStatus != "approved" || o.Status != "active" || o.PublishedAt == nil {
		t.Errorf("after approve the offer is %+v", o)
	}

	// 3. request changes (RequestProductChangesByAdmin)
	if err := f.store.RequestProductChangesByAdmin(ctx, f.productID, actor, "fix the title"); err != nil {
		t.Fatalf("request changes: %v", err)
	}
	f.assertAgrees("after request-changes")

	// 4. reject (RejectProductByAdmin)
	if err := f.store.RejectProductByAdmin(ctx, f.productID, actor, "counterfeit"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	f.assertAgrees("after reject")

	// 5. the patch's revalidation bounce (PatchProduct with Revalidate),
	//    which writes approval_status, status and published_at as literals
	//    inside the patch statement rather than through a named transition.
	if err := f.store.ApproveProductByAdmin(ctx, f.productID, actor, "resolved"); err != nil {
		t.Fatalf("re-approve: %v", err)
	}
	newTitle := "Edited While Approved"
	if err := f.store.PatchProduct(ctx, f.productID,
		ProductPatch{Title: &newTitle, Revalidate: true}, nil); err != nil {
		t.Fatalf("patch: %v", err)
	}
	f.assertAgrees("after revalidation bounce")
	o = f.offer()
	if o.ApprovalStatus != "submitted" || o.Status != "draft" || o.PublishedAt != nil {
		t.Errorf("the revalidation bounce did not reach the offer: %+v", o)
	}

	// And a patch that changes an OFFER column without touching the
	// lifecycle at all: `condition` is on the patch allowlist and lives on
	// both sides, so a sync predicated on the revalidation flag would miss it.
	cond := "refurbished"
	if err := f.store.PatchProduct(ctx, f.productID, ProductPatch{Condition: &cond}, nil); err != nil {
		t.Fatalf("patch condition: %v", err)
	}
	if got := f.offer().Condition; got != "refurbished" {
		t.Errorf("the offer's condition is still %q after the product's became refurbished", got)
	}
}

// TestOfferConsistencyCheckerOverTheWholeEstate is the instrument the
// reader-flip will be gated on, run over whatever is actually in this
// database.
//
// It is deliberately NOT scoped to rows this test created. A checker that
// only looks at its own fixtures cannot tell you the thing you need to know
// before moving the readers, which is whether anything ANYWHERE has been
// writing the legacy columns without the offer — an old pod, an operator's
// UPDATE, a code path nobody remembered.
func TestOfferConsistencyCheckerOverTheWholeEstate(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	// A product created and moved through a transition inside this test, so
	// the run is never vacuous: a checker reporting "0 divergent" over an
	// empty products table is not evidence of anything.
	f := newOfferFixture(t)
	if err := store.SubmitProductForReview(ctx, f.productID, f.sellerID); err != nil {
		t.Fatalf("submit: %v", err)
	}

	// ── Two reads, and why ───────────────────────────────────
	//
	// `go test ./...` runs this package alongside internal/http, whose
	// fixtures insert a product and its offer as two statements. A check
	// landing between them sees a product with no offer that will have one
	// a millisecond later — a false positive, and a flaky gate is a gate
	// people learn to re-run rather than read.
	//
	// Real drift does not heal. So the report is taken twice, and only the
	// second is asserted on: anything still offending after the settle is
	// something no in-flight fixture was going to fix.
	rep, err := store.CheckProductOfferConsistency(ctx)
	if err != nil {
		t.Fatalf("checker: %v", err)
	}
	if !rep.OK() {
		time.Sleep(1500 * time.Millisecond)
		if rep, err = store.CheckProductOfferConsistency(ctx); err != nil {
			t.Fatalf("checker (second read): %v", err)
		}
	}
	t.Logf("products=%d offers=%d missing_offer=%d divergent=%d extra_offers=%d variants_without_offer=%d",
		rep.Products, rep.Offers, rep.MissingOfferCount, rep.DivergentCount,
		rep.ExtraOfferCount, rep.VariantsWithoutOffer)

	for _, id := range rep.MissingOffer {
		t.Logf("  missing offer: product %s", id)
	}
	for _, d := range rep.Divergent {
		t.Logf("  divergent: product %s offer %s %s: product=%q offer=%q",
			d.ProductID, d.OfferID, d.Field, d.Product, d.Offer)
	}

	if rep.DivergentCount != 0 {
		t.Errorf("%d product(s) disagree with their offer. The two copies have drifted, and the "+
			"step that moves the readers onto product_offers must not run until this is zero.",
			rep.DivergentCount)
	}
	if rep.MissingOfferCount != 0 {
		t.Errorf("%d product(s) have no offer for their own seller. Either the 027 backfill did not "+
			"cover them or something is inserting into products without going through "+
			"insertProductTx.", rep.MissingOfferCount)
	}
	if rep.ExtraOfferCount != 0 {
		t.Errorf("%d offer(s) point at a product that does not exist", rep.ExtraOfferCount)
	}
	if !rep.OK() {
		t.Errorf("report.OK() is false; the reader-flip gate would refuse")
	}
}
