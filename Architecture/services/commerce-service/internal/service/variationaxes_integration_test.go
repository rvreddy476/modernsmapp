//go:build integration

package service

// The variation matrix as a seller actually meets it: through CreateProduct
// and UpdateProduct, against a real category schema.
//
// The store package has the tests for what the TABLES refuse. These are for
// what the SERVICE refuses and what it says while refusing, because the
// difference between a good refusal and a bad one here is the difference
// between a seller fixing one field and a seller giving up and typing
// whatever gets past the form.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/service/... \
//	  -run Variation -v -count=1

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ─── Fixture ────────────────────────────────────────────────────────────

// axisFixture is a category asking three questions: two enum axes and one
// plain attribute that is NOT an axis. The third is not decoration — "you
// may not vary on this" is a distinct refusal from "no such attribute", and
// a fixture with only axes cannot tell them apart.
type axisFixture struct {
	t          *testing.T
	svc        *Service
	store      *postgres.Store
	sellerID   uuid.UUID
	categoryID uuid.UUID

	sizeCode   string
	colourCode string
	plainCode  string
}

func newAxisFixture(t *testing.T) *axisFixture {
	t.Helper()
	ctx := context.Background()
	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	st := postgres.New(svcTestPool)
	f := &axisFixture{
		t: t, svc: &Service{store: st}, store: st,
		sellerID:   seedSeller(t, "axis"),
		categoryID: uuid.New(),
		sizeCode:   "sax_size_" + suffix,
		colourCode: "sax_colour_" + suffix,
		plainCode:  "sax_author_" + suffix,
	}

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := svcTestPool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("fixture %s: %v", sql[:40], err)
		}
	}

	exec(`INSERT INTO product_categories (id,name,slug,is_active,is_listable)
	      VALUES ($1,'Axis Test',$2,TRUE,TRUE)`, f.categoryID, "sax-cat-"+suffix)

	for _, d := range []struct {
		code, label, dtype string
		axis               bool
	}{
		{f.sizeCode, "Size " + suffix, "enum", true},
		{f.colourCode, "Colour " + suffix, "enum", true},
		{f.plainCode, "Author " + suffix, "text", false},
	} {
		id := uuid.New()
		exec(`INSERT INTO attribute_definitions
		      (id, code, label, data_type, display_group, applies_to, is_variant_axis, is_active)
		      VALUES ($1,$2,$3,$4,'Product Details','item',$5,TRUE)`,
			id, d.code, d.label, d.dtype, d.axis)
		exec(`INSERT INTO category_attributes (category_id, definition_id, sort_order)
		      VALUES ($1,$2,10)`, f.categoryID, id)
		if d.dtype == "enum" {
			codes := []string{"s", "m", "l", "xl"}
			if d.code == f.colourCode {
				codes = []string{"blue", "red", "green", "black", "white"}
			}
			for i, c := range codes {
				exec(`INSERT INTO attribute_enum_values (definition_id, code, label, sort_order)
				      VALUES ($1,$2,$3,$4)`, id, c, strings.ToUpper(c[:1])+c[1:], (i+1)*10)
			}
		}
	}
	return f
}

// create runs the real CreateProduct with the axes and variants given.
func (f *axisFixture) create(axes []VariationAxisInput, variants []CreateVariantInput) (*postgres.Product, error) {
	cat := f.categoryID
	tax := f.taxClass()
	return f.svc.CreateProduct(context.Background(), CreateProductInput{
		SellerID:      f.sellerID,
		ActorUserID:   uuid.New(),
		CategoryID:    &cat,
		TaxClassID:    &tax,
		Title:         "Axis Fixture Shirt",
		Variants:      variants,
		VariationAxes: axes,
	})
}

// taxClass picks any configured GST class. The create path refuses a product
// without one, and which one it is has nothing to do with this file.
func (f *axisFixture) taxClass() uuid.UUID {
	f.t.Helper()
	var id uuid.UUID
	if err := svcTestPool.QueryRow(context.Background(),
		`SELECT id FROM tax_classes ORDER BY created_at LIMIT 1`).Scan(&id); err != nil {
		f.t.Skipf("no tax class configured in this database, so the create path cannot run: %v", err)
	}
	return id
}

func sku(prefix string) string { return prefix + "-" + uuid.NewString()[:10] }

func opt(code, value string) VariantOptionInput {
	return VariantOptionInput{Code: code, Value: value}
}

func priced(s string, opts ...VariantOptionInput) CreateVariantInput {
	return CreateVariantInput{
		SKU: s, MRPMinor: 50000, SellingPriceMinor: 45000, StockQty: 4, Options: opts,
	}
}

// problems unwraps the refusal, failing if it is some other error.
func problems(t *testing.T, err error) []VariationProblem {
	t.Helper()
	var bad *VariationInvalidError
	if !errors.As(err, &bad) {
		t.Fatalf("expected a VariationInvalidError, got %v", err)
	}
	return bad.Problems
}

func mentions(ps []VariationProblem, needle string) bool {
	for _, p := range ps {
		if strings.Contains(p.Reason, needle) || strings.Contains(p.Code, needle) ||
			strings.Contains(p.Variant, needle) {
			return true
		}
	}
	return false
}

// ─── The happy path, end to end ─────────────────────────────────────────

// TestVariationCreateWritesAxesOptionsAndTheDerivedColumns is the whole
// mechanism in one pass: the service validates, the store writes, the trigger
// derives, and the legacy columns the phone reads still say something a human
// recognises.
func TestVariationCreateWritesAxesOptionsAndTheDerivedColumns(t *testing.T) {
	ctx := context.Background()
	f := newAxisFixture(t)

	p, err := f.create(
		[]VariationAxisInput{{Code: f.sizeCode, Position: 1}, {Code: f.colourCode, Position: 2}},
		[]CreateVariantInput{
			priced(sku("SAX-A"), opt(f.sizeCode, "m"), opt(f.colourCode, "blue")),
			priced(sku("SAX-B"), opt(f.sizeCode, "l"), opt(f.colourCode, "blue")),
			priced(sku("SAX-C"), opt(f.sizeCode, "m"), opt(f.colourCode, "red")),
		})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	axes, err := f.store.ProductVariationAxes(ctx, p.ID)
	if err != nil {
		t.Fatalf("read axes: %v", err)
	}
	if len(axes) != 2 || axes[0].DefinitionCode != f.sizeCode || axes[1].DefinitionCode != f.colourCode {
		t.Fatalf("axes came back as %+v, expected size then colour", axes)
	}

	ids, err := f.store.ProductVariantIdentities(ctx, p.ID)
	if err != nil || len(ids) != 3 {
		t.Fatalf("read variants: %v (%d)", err, len(ids))
	}
	keys := map[string]bool{}
	for _, v := range ids {
		got, err := f.store.VariantLegacyView(ctx, v.ID)
		if err != nil || got == nil {
			t.Fatalf("read variant %s: %v", v.SKU, err)
		}
		if got.VariationKey == nil {
			t.Fatalf("variant %s has no variation_key", v.SKU)
		}
		keys[*got.VariationKey] = true

		// The compatibility half. Nothing in this step changes the readers
		// of these columns, so they have to go on saying something a human
		// recognises rather than an internal code.
		if got.Option1Name == nil || !strings.HasPrefix(*got.Option1Name, "Size ") {
			t.Errorf("variant %s option_1_name = %v, expected the definition's label", v.SKU, got.Option1Name)
		}
		if got.Option1Value == nil || !strings.Contains("M L", *got.Option1Value) {
			t.Errorf("variant %s option_1_value = %v, expected the option's label", v.SKU, got.Option1Value)
		}
		if got.OfferID == nil {
			t.Errorf("variant %s has no offer_id, so its combination escapes the per-offer "+
				"uniqueness index entirely", v.SKU)
		}
	}
	if len(keys) != 3 {
		t.Fatalf("three variants produced %d distinct combination keys: %v", len(keys), keys)
	}
}

// TestVariationCreateWithoutAxesIsUnchanged is the regression guard.
//
// Almost every listing does not vary, and every client written before this
// step sends no axes at all. That must go on working exactly as it did, with
// no axes written and no variation key derived.
func TestVariationCreateWithoutAxesIsUnchanged(t *testing.T) {
	ctx := context.Background()
	f := newAxisFixture(t)

	p, err := f.create(nil, []CreateVariantInput{priced(sku("SAX-PLAIN"))})
	if err != nil {
		t.Fatalf("a create with no variation axes was refused: %v", err)
	}
	axes, _ := f.store.ProductVariationAxes(ctx, p.ID)
	if len(axes) != 0 {
		t.Fatalf("a product that declares no axes got %+v", axes)
	}
	ids, _ := f.store.ProductVariantIdentities(ctx, p.ID)
	got, _ := f.store.VariantLegacyView(ctx, ids[0].ID)
	if got.VariationKey != nil {
		t.Fatalf("a variant with no options got a variation_key %q", *got.VariationKey)
	}
}

// ─── The refusals ───────────────────────────────────────────────────────

// TestVariationRefusesFreeTextValues is the refusal that this whole step
// exists for, exercised through the create route.
func TestVariationRefusesFreeTextValues(t *testing.T) {
	f := newAxisFixture(t)

	_, err := f.create(
		[]VariationAxisInput{{Code: f.colourCode, Position: 1}},
		[]CreateVariantInput{
			priced(sku("SAX-FT1"), opt(f.colourCode, "Navy Blue")),
			priced(sku("SAX-FT2"), opt(f.colourCode, "Blue")),
		})
	ps := problems(t, err)
	if len(ps) != 2 {
		t.Fatalf("two bad values produced %d problems; a form with two mistakes must not need "+
			"two round trips: %+v", len(ps), ps)
	}
	if !mentions(ps, "Free text is refused") {
		t.Errorf("the refusal does not say free text is what is being refused: %+v", ps)
	}
	if !mentions(ps, "blue, red, green, black, white") {
		t.Errorf("the refusal does not list the codes the client should have sent: %+v", ps)
	}
	// Nothing was written. A create that refused halfway would leave a
	// titled, variant-less product in the seller's catalogue.
	var n int
	if err := svcTestPool.QueryRow(context.Background(),
		`SELECT count(*) FROM product_variants WHERE sku LIKE 'SAX-FT%'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d variant(s) survived a refused create", n)
	}
}

// TestVariationRefusesAnAttributeThatIsNotAnAxis distinguishes the two
// "no" answers that look the same from the client's side.
func TestVariationRefusesAnAttributeThatIsNotAnAxis(t *testing.T) {
	f := newAxisFixture(t)

	_, err := f.create(
		[]VariationAxisInput{{Code: f.plainCode, Position: 1}},
		[]CreateVariantInput{priced(sku("SAX-NA"), opt(f.plainCode, "narayan"))})
	ps := problems(t, err)
	if !mentions(ps, "is not marked as a variation axis") {
		t.Fatalf("an attribute the category asks for but does not permit as an axis was refused "+
			"with the wrong reason: %+v", ps)
	}

	_, err = f.create(
		[]VariationAxisInput{{Code: "sax_nothing_at_all", Position: 1}},
		[]CreateVariantInput{priced(sku("SAX-NB"), opt("sax_nothing_at_all", "x"))})
	ps = problems(t, err)
	if !mentions(ps, "is not an attribute this category asks for") {
		t.Fatalf("an unknown code was refused with the wrong reason: %+v", ps)
	}
}

// TestEveryVariantMustAnswerEveryAxis is what makes the matrix a matrix.
//
// A variant that answers only one of two axes is not a distinct thing to
// sell: two of them could be the same shirt at two prices, and nothing
// downstream could say which.
func TestEveryVariantMustAnswerEveryAxis(t *testing.T) {
	f := newAxisFixture(t)

	_, err := f.create(
		[]VariationAxisInput{{Code: f.sizeCode, Position: 1}, {Code: f.colourCode, Position: 2}},
		[]CreateVariantInput{
			priced(sku("SAX-M1"), opt(f.sizeCode, "m"), opt(f.colourCode, "blue")),
			priced(sku("SAX-M2"), opt(f.sizeCode, "l")), // no colour
		})
	ps := problems(t, err)
	if !mentions(ps, "does not answer it") {
		t.Fatalf("a variant missing one axis was not named: %+v", ps)
	}
	if !mentions(ps, "SAX-M2") {
		t.Fatalf("the refusal does not say WHICH variant is short: %+v", ps)
	}

	// And an option on something that is not an axis of this product.
	_, err = f.create(
		[]VariationAxisInput{{Code: f.sizeCode, Position: 1}},
		[]CreateVariantInput{
			priced(sku("SAX-M3"), opt(f.sizeCode, "m"), opt(f.colourCode, "blue")),
		})
	ps = problems(t, err)
	if !mentions(ps, "is not one of this product's variation axes") {
		t.Fatalf("an option on an undeclared axis was not named: %+v", ps)
	}
}

// TestVariationRefusesADuplicateCombinationByName checks the refusal names
// both variants, rather than surfacing the unique index.
func TestVariationRefusesADuplicateCombinationByName(t *testing.T) {
	f := newAxisFixture(t)

	first, second := sku("SAX-D1"), sku("SAX-D2")
	_, err := f.create(
		[]VariationAxisInput{{Code: f.sizeCode, Position: 1}},
		[]CreateVariantInput{
			priced(first, opt(f.sizeCode, "m")),
			priced(second, opt(f.sizeCode, "m")),
		})
	ps := problems(t, err)
	if !mentions(ps, first) || !mentions(ps, second) {
		t.Fatalf("the duplicate refusal names neither variant; a seller cannot act on it: %+v", ps)
	}
	if !mentions(ps, "same combination") {
		t.Fatalf("the duplicate refusal does not say what is duplicated: %+v", ps)
	}
}

// TestVariationCapsTheCombinationCount is the second, softer cap.
//
// The axis cap is two and lives in the database. This one is on the number of
// cells, and it exists because asking somebody to price sixty rows by hand
// does not produce sixty prices — it produces six, and fifty-four rows at
// whatever the form defaulted to.
func TestVariationCapsTheCombinationCount(t *testing.T) {
	f := newAxisFixture(t)

	// 5 colours × 4 sizes + 1 = 21, one past the cap. Every combination is
	// distinct, so the ONLY thing that can refuse it is the cap.
	variants := []CreateVariantInput{}
	sizes := []string{"s", "m", "l", "xl"}
	colours := []string{"blue", "red", "green", "black", "white"}
	for _, c := range colours {
		for _, s := range sizes {
			variants = append(variants,
				priced(sku(fmt.Sprintf("SAX-C%s%s", c[:1], s)), opt(f.sizeCode, s), opt(f.colourCode, c)))
		}
	}
	if len(variants) != 20 {
		t.Fatalf("fixture built %d variants, expected 20", len(variants))
	}

	axes := []VariationAxisInput{{Code: f.sizeCode, Position: 1}, {Code: f.colourCode, Position: 2}}

	// Exactly at the cap: accepted.
	if _, err := f.create(axes, variants); err != nil {
		t.Fatalf("%d combinations — exactly the cap — was refused: %v", len(variants), err)
	}

	// One past it: refused, and the message says the number and the limit.
	variants = append(variants, priced(sku("SAX-OVER"), opt(f.sizeCode, "s"), opt(f.colourCode, "white")))
	_, err := f.create(axes, variants)
	ps := problems(t, err)
	if !mentions(ps, "capped at 20") {
		t.Fatalf("21 combinations were not refused by the cap: %+v (err=%v)", ps, err)
	}
}

// TestMoreThanTwoAxesIsRefusedBeforeTheDatabaseHasTo checks the service says
// something useful about the cap the database also enforces.
func TestMoreThanTwoAxesIsRefusedBeforeTheDatabaseHasTo(t *testing.T) {
	f := newAxisFixture(t)

	_, err := f.create(
		[]VariationAxisInput{
			{Code: f.sizeCode, Position: 1},
			{Code: f.colourCode, Position: 2},
			{Code: f.plainCode, Position: 3},
		},
		[]CreateVariantInput{priced(sku("SAX-3AX"),
			opt(f.sizeCode, "m"), opt(f.colourCode, "blue"), opt(f.plainCode, "x"))})
	ps := problems(t, err)
	if !mentions(ps, "at most two attributes") {
		t.Fatalf("a third axis was not refused with the cap's reason: %+v (err=%v)", ps, err)
	}
}

// ─── The patch ──────────────────────────────────────────────────────────

// TestVariationPatchReplacesTheWholeMatrix covers the update path, including
// the two things about it that are easy to get wrong: it must refuse a patch
// that leaves a variant out, and clearing the axes must take the derived
// columns with them.
func TestVariationPatchReplacesTheWholeMatrix(t *testing.T) {
	ctx := context.Background()
	f := newAxisFixture(t)

	p, err := f.create(
		[]VariationAxisInput{{Code: f.sizeCode, Position: 1}},
		[]CreateVariantInput{
			priced(sku("SAX-P1"), opt(f.sizeCode, "m")),
			priced(sku("SAX-P2"), opt(f.sizeCode, "l")),
		})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	ids, _ := f.store.ProductVariantIdentities(ctx, p.ID)
	if len(ids) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(ids))
	}

	// The seller who owns this product, as UpdateProduct's ownership gate
	// resolves it.
	var owner uuid.UUID
	if err := svcTestPool.QueryRow(ctx,
		`SELECT user_id FROM sellers WHERE id = $1`, f.sellerID).Scan(&owner); err != nil {
		t.Fatalf("read owner: %v", err)
	}

	patch := func(v *VariationPatchInput) error {
		_, err := f.svc.UpdateProduct(ctx, UpdateProductInput{
			ActorUserID: owner, ProductID: p.ID, Variation: v,
		})
		return err
	}

	// ── A patch that leaves a variant out is refused ──
	err = patch(&VariationPatchInput{
		Axes: []VariationAxisInput{{Code: f.sizeCode, Position: 1}},
		Variants: []VariantOptionsPatch{
			{VariantID: ids[0].ID, Options: []VariantOptionInput{opt(f.sizeCode, "s")}},
		},
	})
	ps := problems(t, err)
	if !mentions(ps, "does not say what its options are") {
		t.Fatalf("a patch that named only one of two variants was accepted or misreported: %+v", ps)
	}
	// And it changed nothing.
	before, _ := f.store.VariantOptionCodes(ctx, ids[0].ID)
	if before[f.sizeCode] != "m" {
		t.Fatalf("a refused patch changed the matrix anyway: %v", before)
	}

	// ── A complete patch: a second axis, every variant restated ──
	if err := patch(&VariationPatchInput{
		Axes: []VariationAxisInput{
			{Code: f.sizeCode, Position: 1},
			{Code: f.colourCode, Position: 2},
		},
		Variants: []VariantOptionsPatch{
			{VariantID: ids[0].ID, Options: []VariantOptionInput{
				opt(f.sizeCode, "m"), opt(f.colourCode, "blue")}},
			{VariantID: ids[1].ID, Options: []VariantOptionInput{
				opt(f.sizeCode, "m"), opt(f.colourCode, "red")}},
		},
	}); err != nil {
		t.Fatalf("a complete matrix patch was refused: %v", err)
	}
	axes, _ := f.store.ProductVariationAxes(ctx, p.ID)
	if len(axes) != 2 {
		t.Fatalf("after adding an axis the product has %d: %+v", len(axes), axes)
	}
	got, _ := f.store.VariantLegacyView(ctx, ids[1].ID)
	if got.Option2Value == nil || *got.Option2Value != "Red" {
		t.Fatalf("the derived columns did not follow the patch: %+v", got)
	}

	// ── Clearing the matrix ──
	if err := patch(&VariationPatchInput{
		Axes: nil,
		Variants: []VariantOptionsPatch{
			{VariantID: ids[0].ID}, {VariantID: ids[1].ID},
		},
	}); err != nil {
		t.Fatalf("clearing the matrix was refused: %v", err)
	}
	axes, _ = f.store.ProductVariationAxes(ctx, p.ID)
	if len(axes) != 0 {
		t.Fatalf("the axes survived being cleared: %+v", axes)
	}
	got, _ = f.store.VariantLegacyView(ctx, ids[0].ID)
	if got.VariationKey != nil || got.Option1Name != nil {
		t.Fatalf("the derived columns survived the axes they derive from: %+v", got)
	}
}

// TestVariationPatchIgnoredWhenAbsent is the other half of the regression
// guard: a patch that says nothing about the matrix must leave it alone.
func TestVariationPatchIgnoredWhenAbsent(t *testing.T) {
	ctx := context.Background()
	f := newAxisFixture(t)

	p, err := f.create(
		[]VariationAxisInput{{Code: f.sizeCode, Position: 1}},
		[]CreateVariantInput{priced(sku("SAX-Q1"), opt(f.sizeCode, "xl"))})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var owner uuid.UUID
	if err := svcTestPool.QueryRow(ctx,
		`SELECT user_id FROM sellers WHERE id = $1`, f.sellerID).Scan(&owner); err != nil {
		t.Fatalf("read owner: %v", err)
	}

	title := "Retitled, Matrix Untouched"
	if _, err := f.svc.UpdateProduct(ctx, UpdateProductInput{
		ActorUserID: owner, ProductID: p.ID,
		Fields: postgres.ProductPatch{Title: &title},
	}); err != nil {
		t.Fatalf("title-only patch: %v", err)
	}
	axes, _ := f.store.ProductVariationAxes(ctx, p.ID)
	if len(axes) != 1 {
		t.Fatalf("a patch that never mentioned the matrix changed it: %+v", axes)
	}
	ids, _ := f.store.ProductVariantIdentities(ctx, p.ID)
	opts, _ := f.store.VariantOptionCodes(ctx, ids[0].ID)
	if opts[f.sizeCode] != "xl" {
		t.Fatalf("a title-only patch changed a variant's options: %v", opts)
	}
}
