//go:build integration

package postgres

// The database's half of the variation matrix: the guarantees that hold
// whatever the service layer believes.
//
// ─── WHY THESE ARE DATABASE TESTS AND NOT SERVICE TESTS ─────────────────
//
// The service validates a matrix before it writes one, and internal/service
// has the tests for that. But validation is a claim, and a claim has exactly
// as many holes as the number of write paths nobody remembered: the bulk
// importer, an operator's UPDATE, a repair script, the next endpoint. Every
// one of those reaches the same tables and none of them will call
// resolveVariation.
//
// So what is asserted here is what the TABLES refuse, with the service taken
// out of the picture — raw INSERTs, straight at the constraints:
//
//	the composite foreign key      an option on an axis the product does
//	                               not declare
//	the axis cap                   a third axis, and two axes in one slot
//	UNIQUE(offer_id, variation_key) one shop's second "Blue / M" — and, in
//	                               the same test, that another shop's first
//	                               one is fine
//	the legacy trigger             option_N_* staying in step with the rows
//	                               they are derived from
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/store/postgres/... \
//	  -run Variation -v -count=1

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// ─── Fixture ────────────────────────────────────────────────────────────

// variationFixture is a category that asks two enum questions, both of them
// variant axes, and a seller who can list under it.
//
// The definition codes are unique per run. `attribute_definitions.code` is
// UNIQUE across the whole catalogue by design (025: "the same question asked
// in two categories must be the same field"), so a fixture that seeded a
// fixed `size` would collide with the next run of itself and with whatever
// internal/service is doing in the package running beside it.
type variationFixture struct {
	t          *testing.T
	store      *Store
	sellerID   uuid.UUID
	categoryID uuid.UUID

	sizeDefID   uuid.UUID
	colourDefID uuid.UUID
	sizeCode    string
	colourCode  string
}

func newVariationFixture(t *testing.T) *variationFixture {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	f := &variationFixture{
		t:           t,
		store:       New(testPool),
		sellerID:    uuid.New(),
		categoryID:  uuid.New(),
		sizeDefID:   uuid.New(),
		colourDefID: uuid.New(),
		sizeCode:    "vax_size_" + suffix,
		colourCode:  "vax_colour_" + suffix,
	}

	mustExec(t, `INSERT INTO sellers (id,user_id,store_name,slug,email,state,status)
	             VALUES ($1,$2,'Variation Store',$3,$4,'KA','approved')`,
		f.sellerID, uuid.New(), "vax-store-"+f.sellerID.String()[:12],
		"vax-"+f.sellerID.String()[:8]+"@example.test")

	mustExec(t, `INSERT INTO product_categories (id,name,slug,is_active,is_listable)
	             VALUES ($1,'Variation Test',$2,TRUE,TRUE)`,
		f.categoryID, "vax-cat-"+suffix)

	for _, d := range []struct {
		id    uuid.UUID
		code  string
		label string
	}{
		{f.sizeDefID, f.sizeCode, "Size " + suffix},
		{f.colourDefID, f.colourCode, "Colour " + suffix},
	} {
		mustExec(t, `INSERT INTO attribute_definitions
		    (id, code, label, data_type, display_group, applies_to, is_variant_axis, is_active)
		    VALUES ($1,$2,$3,'enum','Product Details','item',TRUE,TRUE)`, d.id, d.code, d.label)
		mustExec(t, `INSERT INTO category_attributes (category_id, definition_id, sort_order)
		             VALUES ($1,$2,10)`, f.categoryID, d.id)
	}

	mustExec(t, `INSERT INTO attribute_enum_values (definition_id, code, label, sort_order)
	             VALUES ($1,'s','Small',10),($1,'m','Medium',20),($1,'l','Large',30)`, f.sizeDefID)
	mustExec(t, `INSERT INTO attribute_enum_values (definition_id, code, label, sort_order)
	             VALUES ($1,'blue','Blue',10),($1,'red','Red',20)`, f.colourDefID)

	return f
}

// product creates one listing through the store, with the axes and options
// it is given. Through the store, not raw SQL, because the offer link the
// uniqueness index keys on is written by insertProductTx and
// linkVariantToOfferTx and a raw fixture would skip both.
func (f *variationFixture) product(axes []VariationAxis, variants ...NewVariant) *Product {
	f.t.Helper()
	cat := f.categoryID
	p := &Product{
		SellerID:         f.sellerID,
		CategoryID:       &cat,
		Title:            "Variation Fixture",
		Slug:             "vax-" + uuid.NewString(),
		ProductType:      "physical",
		Condition:        "new",
		Status:           "draft",
		Visibility:       "public",
		ApprovalStatus:   "draft",
		ReturnPolicyType: "7_days",
		ReturnPolicyDays: 7,
	}
	if err := f.store.CreateProductAtomic(context.Background(), NewProduct{
		Product: p, Variants: variants, Axes: axes,
	}); err != nil {
		f.t.Fatalf("create product: %v", err)
	}
	return p
}

func variant(sku string, opts ...VariantOption) NewVariant {
	return NewVariant{
		Variant: &ProductVariant{
			SKU: sku, MRP: 500, SellingPrice: 450, CurrencyCode: "INR", Status: "active",
		},
		StockQty: 3,
		Options:  opts,
	}
}

// constraintName pulls the constraint out of whatever the driver returned,
// so a test can say WHICH rule refused rather than merely that something did.
func constraintName(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}

// ─── The composite foreign key ──────────────────────────────────────────

// TestVariationCompositeFKRefusesAnOptionOnAnUndeclaredAxis is the
// constraint the whole design rests on.
//
// Without it, "every variant of a product agrees on the axis set" is a
// convention every write path has to remember, and the one that forgets
// produces a product with a size axis and a variant keyed on colour — which
// no grid can render and no reader can explain.
func TestVariationCompositeFKRefusesAnOptionOnAnUndeclaredAxis(t *testing.T) {
	ctx := context.Background()
	f := newVariationFixture(t)

	// A product that varies on SIZE only.
	p := f.product(
		[]VariationAxis{{DefinitionID: f.sizeDefID, Position: 1}},
		variant("VAX-FK-"+uuid.NewString()[:8], VariantOption{DefinitionID: f.sizeDefID, ValueCode: "m"}),
	)
	variants, err := f.store.ProductVariantIdentities(ctx, p.ID)
	if err != nil || len(variants) != 1 {
		t.Fatalf("read variants: %v (%d)", err, len(variants))
	}

	// Straight at the table, with no service in the way: an option on
	// COLOUR, which this product does not declare.
	_, err = testPool.Exec(ctx, `
		INSERT INTO product_variant_options (variant_id, product_id, definition_id, value_code)
		VALUES ($1,$2,$3,'blue')`, variants[0].ID, p.ID, f.colourDefID)
	if err == nil {
		t.Fatal("an option on an undeclared axis was ACCEPTED. The composite foreign key is the " +
			"only thing making 'every variant agrees on the axis set' true in the database " +
			"rather than by convention, and it is not holding.")
	}
	if got := constraintName(err); got != "product_variant_options_axis_fk" {
		t.Fatalf("refused by %q, expected product_variant_options_axis_fk: %v", got, err)
	}

	// And through the store, where it must arrive as a sentence.
	err = f.store.CreateProductAtomic(ctx, NewProduct{
		Product: &Product{
			SellerID: f.sellerID, CategoryID: &f.categoryID, Title: "Undeclared",
			Slug: "vax-" + uuid.NewString(), ProductType: "physical", Condition: "new",
			Status: "draft", Visibility: "public", ApprovalStatus: "draft",
			ReturnPolicyType: "7_days", ReturnPolicyDays: 7,
		},
		Axes: []VariationAxis{{DefinitionID: f.sizeDefID, Position: 1}},
		Variants: []NewVariant{variant("VAX-FK2-"+uuid.NewString()[:8],
			VariantOption{DefinitionID: f.colourDefID, ValueCode: "blue"})},
	})
	if !errors.Is(err, ErrUndeclaredVariationAxis) {
		t.Fatalf("store returned %v, expected ErrUndeclaredVariationAxis", err)
	}
}

// TestVariationOptionCannotClaimAnotherProductsAxis closes the other half.
//
// The axis foreign key alone would be satisfied by naming a DIFFERENT
// product that happens to declare the axis, which would file one variant's
// option under two products at once.
func TestVariationOptionCannotClaimAnotherProductsAxis(t *testing.T) {
	ctx := context.Background()
	f := newVariationFixture(t)

	mine := f.product(
		[]VariationAxis{{DefinitionID: f.sizeDefID, Position: 1}},
		variant("VAX-X1-"+uuid.NewString()[:8], VariantOption{DefinitionID: f.sizeDefID, ValueCode: "s"}),
	)
	theirs := f.product(
		[]VariationAxis{{DefinitionID: f.colourDefID, Position: 1}},
		variant("VAX-X2-"+uuid.NewString()[:8], VariantOption{DefinitionID: f.colourDefID, ValueCode: "red"}),
	)
	mineVariants, _ := f.store.ProductVariantIdentities(ctx, mine.ID)

	_, err := testPool.Exec(ctx, `
		INSERT INTO product_variant_options (variant_id, product_id, definition_id, value_code)
		VALUES ($1,$2,$3,'blue')`, mineVariants[0].ID, theirs.ID, f.colourDefID)
	if err == nil {
		t.Fatal("a variant of one product carried an option filed under ANOTHER product's axis")
	}
	if got := constraintName(err); got != "product_variant_options_variant_fk" {
		t.Fatalf("refused by %q, expected product_variant_options_variant_fk: %v", got, err)
	}
}

// ─── The cap ────────────────────────────────────────────────────────────

// TestVariationAxisCapIsTwo checks both halves of the declarative cap.
//
// It is a CHECK on the position plus a UNIQUE on (product, position), not a
// counting trigger, so there is no window in which two concurrent inserts
// each see one existing row and both proceed.
func TestVariationAxisCapIsTwo(t *testing.T) {
	ctx := context.Background()
	f := newVariationFixture(t)

	p := f.product(
		[]VariationAxis{
			{DefinitionID: f.sizeDefID, Position: 1},
			{DefinitionID: f.colourDefID, Position: 2},
		},
		variant("VAX-CAP-"+uuid.NewString()[:8],
			VariantOption{DefinitionID: f.sizeDefID, ValueCode: "l"},
			VariantOption{DefinitionID: f.colourDefID, ValueCode: "blue"}),
	)

	// A third definition, bound to the same category, so the only thing that
	// can refuse it is the cap.
	thirdID := uuid.New()
	thirdCode := "vax_third_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	mustExec(t, `INSERT INTO attribute_definitions
	    (id, code, label, data_type, display_group, applies_to, is_variant_axis, is_active)
	    VALUES ($1,$2,$3,'text','Product Details','item',TRUE,TRUE)`, thirdID, thirdCode, "Third "+thirdCode)
	mustExec(t, `INSERT INTO category_attributes (category_id, definition_id) VALUES ($1,$2)`,
		f.categoryID, thirdID)

	_, err := testPool.Exec(ctx,
		`INSERT INTO product_variation_axes (product_id, definition_id, position) VALUES ($1,$2,3)`,
		p.ID, thirdID)
	if err == nil {
		t.Fatal("a THIRD variation axis was accepted. Two axes of five options is 25 cells a " +
			"seller prices by hand; three is 125, and the observable result is that they price " +
			"six and leave the rest at whatever the form defaulted to.")
	}
	if got := constraintName(err); got != "product_variation_axes_position_range" {
		t.Fatalf("third axis refused by %q, expected product_variation_axes_position_range: %v", got, err)
	}

	_, err = testPool.Exec(ctx,
		`INSERT INTO product_variation_axes (product_id, definition_id, position) VALUES ($1,$2,2)`,
		p.ID, thirdID)
	if err == nil {
		t.Fatal("two axes were accepted in the same slot")
	}
	if got := constraintName(err); got != "product_variation_axes_position_key" {
		t.Fatalf("duplicate slot refused by %q, expected product_variation_axes_position_key: %v", got, err)
	}

	// The slots may still be SWAPPED in one statement. The unique is
	// DEFERRABLE INITIALLY IMMEDIATE precisely so that a reorder — which
	// passes through a state where both rows claim one slot — is judged at
	// the end of the statement rather than row by row.
	if _, err := testPool.Exec(ctx,
		`UPDATE product_variation_axes SET position = 3 - position WHERE product_id = $1`, p.ID); err != nil {
		t.Fatalf("swapping the two axes round should be one statement, not a constraint violation: %v", err)
	}
	axes, err := f.store.ProductVariationAxes(ctx, p.ID)
	if err != nil {
		t.Fatalf("read axes: %v", err)
	}
	if len(axes) != 2 || axes[0].DefinitionID != f.colourDefID {
		t.Fatalf("after the swap the first axis should be colour, got %+v", axes)
	}
}

// ─── UNIQUE(offer_id, variation_key) ────────────────────────────────────

// TestVariationKeyIsUniquePerOfferAndSharedAcrossOffers is the shared
// catalogue, stated as a test.
//
// Both halves are the point and neither is sufficient alone. A UNIQUE over
// the PRODUCT would stop the second seller listing "Blue / M" at all, which
// is the entire thing a shared catalogue exists to allow. No uniqueness at
// all lets one seller list it twice, and then "this shop's price for Blue /
// M" has two answers.
func TestVariationKeyIsUniquePerOfferAndSharedAcrossOffers(t *testing.T) {
	ctx := context.Background()
	f := newVariationFixture(t)

	axes := []VariationAxis{
		{DefinitionID: f.sizeDefID, Position: 1},
		{DefinitionID: f.colourDefID, Position: 2},
	}
	blueM := []VariantOption{
		{DefinitionID: f.sizeDefID, ValueCode: "m"},
		{DefinitionID: f.colourDefID, ValueCode: "blue"},
	}

	p := f.product(axes, variant("VAX-U1-"+uuid.NewString()[:8], blueM...))

	// ── The same shop, the same combination, twice ──
	err := f.store.CreateVariant(ctx, &ProductVariant{
		ProductID: p.ID, SKU: "VAX-U2-" + uuid.NewString()[:8],
		MRP: 600, SellingPrice: 550, CurrencyCode: "INR", Status: "active",
	})
	if err != nil {
		t.Fatalf("second variant: %v", err)
	}
	all, _ := f.store.ProductVariantIdentities(ctx, p.ID)
	var second uuid.UUID
	for _, v := range all {
		if strings.HasPrefix(v.SKU, "VAX-U2-") {
			second = v.ID
		}
	}
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	err = insertVariantOptionsTx(ctx, tx, second, p.ID, blueM)
	_ = tx.Rollback(ctx)
	if !errors.Is(err, ErrDuplicateVariantCombination) {
		t.Fatalf("one shop listed the same combination twice; got %v, expected "+
			"ErrDuplicateVariantCombination. \"this shop's price for Blue / M\" now has two answers.", err)
	}

	// ── A DIFFERENT shop, the same catalogue item, the same combination ──
	//
	// This must be allowed, and it is the reason the index is keyed on
	// offer_id rather than on product_id.
	//
	// Built and ROLLED BACK inside one transaction. A second seller's offer
	// on someone else's product is precisely the shape 027's consistency
	// checker still reports as divergence — it joins on product_id alone and
	// compares seller_id, which was right while one product meant one offer
	// and is the assumption a later step in this plan retires. Leaving one
	// behind would make that instrument report a false positive on every
	// subsequent run, and an instrument nobody believes is worse than none.
	otherSeller := uuid.New()
	otherOffer := uuid.New()
	rivalVariant := uuid.New()

	tx, err = testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO sellers (id,user_id,store_name,slug,email,state,status)
		  VALUES ($1,$2,'Second Shop',$3,$4,'KA','approved')`,
			[]any{otherSeller, uuid.New(), "vax-shop2-" + otherSeller.String()[:12],
				"vax2-" + otherSeller.String()[:8] + "@example.test"}},
		{`INSERT INTO product_offers (id, product_id, seller_id, status, visibility,
		                              approval_status, condition)
		  VALUES ($1,$2,$3,'draft','public','draft','new')`,
			[]any{otherOffer, p.ID, otherSeller}},
		{`INSERT INTO product_variants (id, product_id, sku, mrp, selling_price, offer_id)
		  VALUES ($1,$2,$3,700,650,$4)`,
			[]any{rivalVariant, p.ID, "VAX-RIVAL-" + uuid.NewString()[:8], otherOffer}},
	} {
		if _, err := tx.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed the rival shop: %v", err)
		}
	}

	if err := insertVariantOptionsTx(ctx, tx, rivalVariant, p.ID, blueM); err != nil {
		t.Fatalf("a SECOND SHOP could not offer the same combination of the same catalogue item: %v.\n"+
			"That is the one thing the shared catalogue exists to allow; the uniqueness must be "+
			"per offer, not per product.", err)
	}

	// Read back inside the same transaction — the row does not survive it.
	var key *string
	if err := tx.QueryRow(ctx,
		`SELECT variation_key FROM product_variants WHERE id = $1`, rivalVariant).Scan(&key); err != nil {
		t.Fatalf("read the rival's key: %v", err)
	}
	if key == nil || *key != f.sizeCode+"=m|"+f.colourCode+"=blue" {
		t.Fatalf("the rival's variation_key = %v, expected the same key the first shop's variant "+
			"carries — the two are the same combination, which is the point", deref(key))
	}
}

// ─── The legacy columns ─────────────────────────────────────────────────

// TestVariationTriggerKeepsTheLegacyOptionColumnsInStep is the compatibility
// half.
//
// `option_N_name` / `option_N_value` are read by the bulk importer, by the
// analytics queries and by product screens in a phone build already in
// people's hands. Nothing in this change touches those readers, so the
// columns have to go on saying the truth — derived from the option rows,
// never authoritative, and recomputed by whichever writer touches them.
func TestVariationTriggerKeepsTheLegacyOptionColumnsInStep(t *testing.T) {
	ctx := context.Background()
	f := newVariationFixture(t)

	p := f.product(
		[]VariationAxis{
			{DefinitionID: f.sizeDefID, Position: 1},
			{DefinitionID: f.colourDefID, Position: 2},
		},
		variant("VAX-LEG-"+uuid.NewString()[:8],
			VariantOption{DefinitionID: f.sizeDefID, ValueCode: "l"},
			VariantOption{DefinitionID: f.colourDefID, ValueCode: "blue"}),
	)
	ids, _ := f.store.ProductVariantIdentities(ctx, p.ID)
	vid := ids[0].ID

	got, err := f.store.VariantLegacyView(ctx, vid)
	if err != nil || got == nil {
		t.Fatalf("read variant: %v", err)
	}
	// The LABELS, because that is what the phone renders straight to a
	// human. The machine-readable form is variation_key and the option rows.
	if got.Option1Value == nil || *got.Option1Value != "Large" {
		t.Fatalf("option_1_value = %v, expected the enum LABEL \"Large\"", deref(got.Option1Value))
	}
	if got.Option2Value == nil || *got.Option2Value != "Blue" {
		t.Fatalf("option_2_value = %v, expected \"Blue\"", deref(got.Option2Value))
	}
	wantKey := f.sizeCode + "=l|" + f.colourCode + "=blue"
	if got.VariationKey == nil || *got.VariationKey != wantKey {
		t.Fatalf("variation_key = %v, expected %q", deref(got.VariationKey), wantKey)
	}
	if got.Option3Name != nil || got.Option3Value != nil {
		t.Fatalf("slot 3 should be cleared on a managed variant, got %v/%v",
			deref(got.Option3Name), deref(got.Option3Value))
	}

	// ── Change an option row: the derived columns must follow ──
	mustExec(t, `UPDATE product_variant_options SET value_code='red'
	              WHERE variant_id=$1 AND definition_id=$2`, vid, f.colourDefID)
	got, _ = f.store.VariantLegacyView(ctx, vid)
	if got.Option2Value == nil || *got.Option2Value != "Red" {
		t.Fatalf("after changing the option row, option_2_value = %v, expected \"Red\". "+
			"The legacy columns have gone stale, which is exactly what the trigger exists to "+
			"prevent — every reader of them is now looking at the previous combination.",
			deref(got.Option2Value))
	}
	if got.VariationKey == nil || *got.VariationKey != f.sizeCode+"=l|"+f.colourCode+"=red" {
		t.Fatalf("variation_key did not follow the option row: %v", deref(got.VariationKey))
	}

	// ── Remove the options: the derived columns go with them ──
	mustExec(t, `DELETE FROM product_variant_options WHERE variant_id=$1`, vid)
	got, _ = f.store.VariantLegacyView(ctx, vid)
	if got.Option1Name != nil || got.Option1Value != nil || got.VariationKey != nil {
		t.Fatalf("the derived columns survived the deletion of the rows they derive from: %+v", got)
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// ─── The migration's split ──────────────────────────────────────────────

// TestVariationBackfillResolvesWhatItCanAndParksTheRest exercises migration
// 028's resolution pass over data built to sit on both sides of the line.
//
// The parked half is the half that matters. On a shared catalogue an axis is
// permanent and public: deciding that free text "Colour" means some
// definition that happens to be spelled like it mints a wrong axis onto
// every seller who ever lists that item, and unlike a wrong value a seller
// cannot correct it, because the axis is a fact about the ITEM. So the
// assertion is not "how much did it migrate" but "did it refuse exactly the
// things it cannot know".
func TestVariationBackfillResolvesWhatItCanAndParksTheRest(t *testing.T) {
	ctx := context.Background()
	f := newVariationFixture(t)

	// The definition's LABEL is "Size <suffix>"; the legacy text says it in
	// a different case, which is what real data looks like.
	label := "Size " + strings.TrimPrefix(f.sizeCode, "vax_size_")

	// ── Resolvable: two variants, one axis, values that are enum labels ──
	good := f.rawProduct("resolvable")
	f.rawVariant(good, "VAXB-OK1-"+uuid.NewString()[:8], strings.ToUpper(label), "large")
	f.rawVariant(good, "VAXB-OK2-"+uuid.NewString()[:8], strings.ToLower(label), "Medium")

	// ── Parked: a name no definition matches ──
	unknown := f.rawProduct("unknown-name")
	f.rawVariant(unknown, "VAXB-U1-"+uuid.NewString()[:8], "Fabric Weave "+uuid.NewString()[:6], "twill")

	// ── Parked: the name resolves, the VALUE is not an option ──
	badValue := f.rawProduct("unknown-value")
	f.rawVariant(badValue, "VAXB-V1-"+uuid.NewString()[:8], label, "Enormous")

	// ── Parked: one variant resolves, its sibling carries nothing ──
	partial := f.rawProduct("half-resolvable")
	f.rawVariant(partial, "VAXB-P1-"+uuid.NewString()[:8], label, "Small")
	f.rawVariant(partial, "VAXB-P2-"+uuid.NewString()[:8], "", "")

	rep, err := f.store.BackfillVariationAxes(ctx)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	t.Logf("backfill: products=%d variants=%d exceptions=%d",
		rep.ProductsMigrated, rep.VariantsMigrated, rep.ExceptionsRecorded)

	// The resolvable product got its axis and both variants got options.
	axes, err := f.store.ProductVariationAxes(ctx, good)
	if err != nil {
		t.Fatalf("read axes: %v", err)
	}
	if len(axes) != 1 || axes[0].DefinitionID != f.sizeDefID || axes[0].Position != 1 {
		t.Fatalf("the resolvable product should have exactly one axis at position 1, got %+v", axes)
	}
	ids, _ := f.store.ProductVariantIdentities(ctx, good)
	for _, v := range ids {
		opts, err := f.store.VariantOptionCodes(ctx, v.ID)
		if err != nil {
			t.Fatalf("read options: %v", err)
		}
		if len(opts) != 1 || opts[f.sizeCode] == "" {
			t.Fatalf("variant %s resolved to %v, expected one option on %s", v.SKU, opts, f.sizeCode)
		}
	}

	// And the three that could not be resolved got NO axes and a reason.
	for _, parked := range []struct {
		id   uuid.UUID
		what string
	}{{unknown, "an option name no definition matches"},
		{badValue, "a value that is not one of the definition's options"},
		{partial, "a sibling variant carrying no options at all"}} {

		axes, err := f.store.ProductVariationAxes(ctx, parked.id)
		if err != nil {
			t.Fatalf("read axes: %v", err)
		}
		if len(axes) != 0 {
			t.Fatalf("a product with %s was MIGRATED anyway (%+v). A wrong axis on a shared "+
				"catalogue is permanent and no seller can correct it; parking it was the "+
				"whole instruction.", parked.what, axes)
		}
		var n int
		if err := testPool.QueryRow(ctx,
			`SELECT count(*) FROM variant_migration_exceptions WHERE product_id=$1`, parked.id,
		).Scan(&n); err != nil {
			t.Fatalf("count exceptions: %v", err)
		}
		if n == 0 {
			t.Fatalf("a product with %s was neither migrated NOR parked. The residue has to be a "+
				"worklist, not a silence.", parked.what)
		}
	}

	// Re-running is idempotent: the product that migrated is skipped, not
	// fought over.
	before, _ := f.store.ProductVariationAxes(ctx, good)
	if _, err := f.store.BackfillVariationAxes(ctx); err != nil {
		t.Fatalf("second backfill run: %v", err)
	}
	after, _ := f.store.ProductVariationAxes(ctx, good)
	if len(before) != len(after) {
		t.Fatalf("re-running the backfill changed the axes: %d -> %d", len(before), len(after))
	}
}

// rawProduct inserts a product the way the estate that predates 028 looks:
// straight into `products`, with its offer, and no axes.
func (f *variationFixture) rawProduct(label string) uuid.UUID {
	f.t.Helper()
	id := uuid.New()
	mustExec(f.t, `INSERT INTO products
	    (id, seller_id, category_id, title, slug, product_type, condition, status, visibility,
	     approval_status, return_policy_type, return_policy_days)
	    VALUES ($1,$2,$3,$4,$5,'physical','new','draft','public','draft','7_days',7)`,
		id, f.sellerID, f.categoryID, "Backfill "+label, "vax-bf-"+uuid.NewString())
	seedOfferFor(f.t, id)
	return id
}

// rawVariant inserts a variant carrying LEGACY free text in slot 1, which is
// the shape everything before 028 wrote.
func (f *variationFixture) rawVariant(productID uuid.UUID, sku, name, value string) uuid.UUID {
	f.t.Helper()
	id := uuid.New()
	var n, v any
	if name != "" {
		n, v = name, value
	}
	mustExec(f.t, `INSERT INTO product_variants
	    (id, product_id, sku, option_1_name, option_1_value, mrp, selling_price, offer_id)
	    SELECT $1,$2,$3,$4,$5,500,450,o.id FROM product_offers o WHERE o.product_id = $2`,
		id, productID, sku, n, v)
	return id
}

// ─── The instrument from the previous step, still clean ─────────────────

// TestVariationWritesLeaveOfferConsistencyClean re-runs the 027 checker after
// this step's writes.
//
// The variation write path touches `product_variants` — through the trigger,
// which rewrites six columns and bumps `updated_at` — and creates products
// through the same atomic path that dual-writes the offer. If any of that
// had slipped past the dual-write, the instrument gating the step that moves
// the readers onto `product_offers` would start reporting divergence, and
// that step would be blocked by this one.
func TestVariationWritesLeaveOfferConsistencyClean(t *testing.T) {
	ctx := context.Background()
	f := newVariationFixture(t)

	f.product(
		[]VariationAxis{{DefinitionID: f.sizeDefID, Position: 1}},
		variant("VAX-CON1-"+uuid.NewString()[:8], VariantOption{DefinitionID: f.sizeDefID, ValueCode: "s"}),
		variant("VAX-CON2-"+uuid.NewString()[:8], VariantOption{DefinitionID: f.sizeDefID, ValueCode: "m"}),
	)

	rep, err := f.store.CheckProductOfferConsistency(ctx)
	if err != nil {
		t.Fatalf("checker: %v", err)
	}
	t.Logf("products=%d offers=%d missing=%d divergent=%d extra=%d variants_without_offer=%d",
		rep.Products, rep.Offers, rep.MissingOfferCount, rep.DivergentCount,
		rep.ExtraOfferCount, rep.VariantsWithoutOffer)
	for _, d := range rep.Divergent {
		t.Logf("  divergent: product %s %s: product=%q offer=%q", d.ProductID, d.Field, d.Product, d.Offer)
	}
	if rep.DivergentCount != 0 {
		t.Errorf("%d product(s) disagree with their offer after the variation writes", rep.DivergentCount)
	}
	if rep.MissingOfferCount != 0 {
		t.Errorf("%d product(s) have no offer after the variation writes", rep.MissingOfferCount)
	}
}
