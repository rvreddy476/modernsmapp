//go:build integration

package postgres

// The typed value layer against a real database: the CHECK constraint that
// decides what a value may look like, the upsert that must replace rather than
// accumulate, the doc projection that must not drift, and the three readers
// migration 026 moved off the EAV row.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/store/postgres/... -run AttributeValue -v
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/store/postgres/... -run SourceImage -v

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ─── Fixture ────────────────────────────────────────────────────────────

// valueFixture is one category, one seller, one product, and one definition
// per data type under test.
type valueFixture struct {
	store    *Store
	category uuid.UUID
	seller   uuid.UUID
	product  uuid.UUID
	defs     map[string]uuid.UUID // logical key → definition id
	codes    map[string]string    // logical key → definition code
}

func newValueFixture(t *testing.T) *valueFixture {
	t.Helper()
	f := &valueFixture{
		store:    New(testPool),
		category: uuid.New(),
		seller:   uuid.New(),
		product:  uuid.New(),
		defs:     map[string]uuid.UUID{},
		codes:    map[string]string{},
	}
	tag := f.product.String()[:8]

	mustExec(t, `INSERT INTO product_categories (id,parent_id,name,slug,display_order,is_active)
	             VALUES ($1,NULL,'Value Cat',$2,1,TRUE)`, f.category, "value-cat-"+tag)
	mustExec(t, `INSERT INTO sellers (id,user_id,store_name,slug,email,state)
	             VALUES ($1,$2,'Value Store',$3,'seller@example.test','KA')`,
		f.seller, uuid.New(), "value-store-"+tag)
	mustExec(t, `INSERT INTO products (id,seller_id,category_id,title,slug,status,approval_status)
	             VALUES ($1,$2,$3,'Value Product',$4,'active','approved')`,
		f.product, f.seller, f.category, "value-product-"+tag)
	seedOfferFor(t, f.product)

	// One definition per data type, each bound to the category with an
	// explicit sort order and a group, so the ordering assertions have
	// something deliberate to check.
	for _, spec := range []struct {
		key, dataType, group string
		sortOrder            int
		maxValues            *int
	}{
		{key: "author", dataType: "text", group: "Product Details", sortOrder: 20},
		{key: "pages", dataType: "integer", group: "Product Details", sortOrder: 10},
		{key: "inprint", dataType: "boolean", group: "Product Details", sortOrder: 30},
		{key: "published", dataType: "date", group: "Product Identity", sortOrder: 10},
		{key: "cover", dataType: "media", group: "Product Identity", sortOrder: 20},
		{key: "weight", dataType: "measure", group: "Logistics", sortOrder: 10},
		{key: "binding", dataType: "enum", group: "Product Details", sortOrder: 40},
		{key: "language", dataType: "multi_enum", group: "Product Details", sortOrder: 50, maxValues: valIntp(3)},
	} {
		id := uuid.New()
		code := fmt.Sprintf("%s_%s", spec.key, strings.ReplaceAll(tag, "-", ""))
		f.defs[spec.key] = id
		f.codes[spec.key] = code

		var family, defaultUnit any
		if spec.dataType == "measure" {
			family, defaultUnit = "mass", "g"
		}
		mustExec(t, `INSERT INTO attribute_definitions
		      (id,code,label,data_type,unit_family,default_unit,max_values,display_group,applies_to)
		      VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'item')`,
			id, code, strings.ToTitle(spec.key), spec.dataType, family, defaultUnit,
			spec.maxValues, spec.group)
		mustExec(t, `INSERT INTO category_attributes (category_id,definition_id,sort_order)
		             VALUES ($1,$2,$3)`, f.category, id, spec.sortOrder)
	}

	for _, v := range []string{"paperback", "hardback"} {
		mustExec(t, `INSERT INTO attribute_enum_values (definition_id,code,label) VALUES ($1,$2,$3)`,
			f.defs["binding"], v, v)
	}
	for _, v := range []string{"en", "hi", "ta"} {
		mustExec(t, `INSERT INTO attribute_enum_values (definition_id,code,label) VALUES ($1,$2,$3)`,
			f.defs["language"], v, v)
	}

	t.Cleanup(func() {
		mustExec(t, `DELETE FROM product_attributes WHERE product_id=$1`, f.product)
		mustExec(t, `DELETE FROM products WHERE id=$1`, f.product)
		mustExec(t, `DELETE FROM sellers WHERE id=$1`, f.seller)
		for _, id := range f.defs {
			mustExec(t, `DELETE FROM attribute_definitions WHERE id=$1`, id)
		}
		mustExec(t, `DELETE FROM product_categories WHERE id=$1`, f.category)
	})
	return f
}

func valIntp(n int) *int { return &n }

// insertRaw writes one product_attributes row with whatever combination of
// value columns the test names, and reports the database's answer. It is the
// only way to reach the CHECK constraint: the store's own writer builds legal
// rows by construction, which is the point of it.
func (f *valueFixture) insertRaw(defID any, text, num, boolean, date, media, unit any) error {
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO product_attributes
		    (product_id, definition_id, name, value, position,
		     value_text, value_num, value_bool, value_date, value_media_id, unit_code)
		VALUES ($1,$2,'probe','probe',$3,$4,$5,$6,$7,$8,$9)`,
		f.product, defID, positionCounter(), text, num, boolean, date, media, unit)
	return err
}

// positionCounter hands out a fresh position per raw insert so that the
// partial unique index — which is a DIFFERENT rule — cannot be what makes a
// CHECK-constraint test fail.
var rawPosition int

func positionCounter() int {
	rawPosition++
	return rawPosition
}

// ─── The CHECK constraint ───────────────────────────────────────────────

// Every legal and illegal combination of the five value columns plus a unit,
// against the real constraint.
//
// The two rules under test are separate constraints on purpose:
//
//	product_attributes_one_typed_value    exactly one value_* on a typed row
//	product_attributes_unit_needs_number  a unit only ever beside a number
func TestAttributeValueCheckConstraintAcceptsAndRefusesEachCombination(t *testing.T) {
	f := newValueFixture(t)
	def := f.defs["author"]
	mediaID := uuid.New()
	today := time.Now().Format("2006-01-02")

	cases := []struct {
		name                                  string
		defID                                 any
		text, num, boolean, date, media, unit any
		wantErr                               string // "" means it must be accepted
	}{
		// ── the five legal single-value shapes ──
		{name: "text alone", defID: def, text: "R. K. Narayan"},
		{name: "number alone", defID: def, num: 328},
		{name: "bool alone", defID: def, boolean: true},
		{name: "date alone", defID: def, date: today},
		{name: "media alone", defID: def, media: mediaID},

		// ── the measure pairing: a number WITH a unit ──
		//
		// This is the combination the brief calls the exception, and it is
		// legal precisely because unit_code is not one of the five counted
		// columns. If it were counted, every weight in the catalogue would be
		// rejected.
		{name: "number with a unit (measure)", defID: def, num: 250, unit: "g"},

		// ── zero values ──
		//
		// A typed row that answers nothing. The absence of an answer is the
		// absence of a row; a row like this would make a "required" check
		// think the field was filled in.
		{name: "no value at all", defID: def, wantErr: "one_typed_value"},

		// ── two or more values ──
		{name: "text and number", defID: def, text: "x", num: 1, wantErr: "one_typed_value"},
		{name: "text and bool", defID: def, text: "x", boolean: true, wantErr: "one_typed_value"},
		{name: "number and date", defID: def, num: 1, date: today, wantErr: "one_typed_value"},
		{name: "media and text", defID: def, media: mediaID, text: "x", wantErr: "one_typed_value"},
		{name: "all five", defID: def, text: "x", num: 1, boolean: true, date: today, media: mediaID,
			wantErr: "one_typed_value"},

		// ── a unit with nothing to qualify ──
		//
		// "red kg" is not anything. The unit constraint catches these; note
		// the bare-unit case violates BOTH constraints, which is why the
		// assertion checks only that the right kind of refusal happened.
		{name: "unit on text", defID: def, text: "red", unit: "kg", wantErr: "unit_needs_number"},
		{name: "unit on bool", defID: def, boolean: true, unit: "kg", wantErr: "unit_needs_number"},
		{name: "unit on date", defID: def, date: today, unit: "kg", wantErr: "unit_needs_number"},
		{name: "unit on media", defID: def, media: mediaID, unit: "kg", wantErr: "unit_needs_number"},
		// A unit and nothing else violates BOTH rules — zero value columns and
		// a unit with no number. Postgres reports whichever it evaluates
		// first, so this asserts the exactly-one rule, which is the one that
		// describes the deeper problem: the row answers nothing at all.
		{name: "unit alone", defID: def, unit: "kg", wantErr: "one_typed_value"},

		// ── the legacy side is untouched ──
		//
		// definition_id IS NULL is what every row in the table is today, and
		// what a pod on the previous image keeps writing. None of the above
		// applies to it. This is the case that makes the migration
		// deploy-order-free, so it is asserted rather than assumed.
		{name: "legacy row with no typed value", defID: nil},
		{name: "legacy row with several values", defID: nil, text: "x", num: 1, boolean: true},
		{name: "legacy row with a bare unit", defID: nil, unit: "kg"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := f.insertRaw(tc.defID, tc.text, tc.num, tc.boolean, tc.date, tc.media, tc.unit)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("must be accepted, got: %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("must be refused by %s, but the row was stored", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("refused by the wrong rule: want %s, got: %v", tc.wantErr, err)
			}
		})
	}
}

// ─── Upsert semantics ───────────────────────────────────────────────────

// Writing the same field twice REPLACES it. It used to be a delete-then-insert
// with nothing stopping two rows from surviving.
func TestAttributeValueUpsertReplacesRatherThanDuplicates(t *testing.T) {
	ctx := context.Background()
	f := newValueFixture(t)

	first, second := "First Author", "Second Author"
	for _, v := range []string{first, second, second} {
		val := v
		if err := f.store.PutProductAttributeValues(ctx, f.product, []AttributeValueSet{{
			DefinitionID: f.defs["author"],
			Values:       []ProductAttributeValue{{ValueText: &val}},
		}}); err != nil {
			t.Fatalf("put %q: %v", v, err)
		}
	}

	rows, err := f.store.ProductAttributeValues(ctx, f.product)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("three writes of one field must leave one row, got %d", len(rows))
	}
	if got := *rows[0].Value.ValueText; got != second {
		t.Fatalf("the last write must win: want %q, got %q", second, got)
	}

	// A multi_enum shrinking is the case a merge would get wrong: unticking an
	// option has to remove its row, not leave it behind.
	put := func(codes ...string) {
		t.Helper()
		vals := make([]ProductAttributeValue, 0, len(codes))
		for _, c := range codes {
			cc := c
			vals = append(vals, ProductAttributeValue{ValueText: &cc})
		}
		if err := f.store.PutProductAttributeValues(ctx, f.product, []AttributeValueSet{{
			DefinitionID: f.defs["language"], Values: vals,
		}}); err != nil {
			t.Fatalf("put languages %v: %v", codes, err)
		}
	}
	put("en", "hi", "ta")
	put("en")

	rows, _ = f.store.ProductAttributeValues(ctx, f.product)
	langs := 0
	for _, r := range rows {
		if r.Definition.Code == f.codes["language"] {
			langs++
		}
	}
	if langs != 1 {
		t.Fatalf("unticking two of three languages must leave one row, got %d", langs)
	}

	// And a definition the write did not name is untouched — that is what
	// makes a partial edit possible at all.
	found := false
	for _, r := range rows {
		if r.Definition.Code == f.codes["author"] {
			found = true
		}
	}
	if !found {
		t.Fatalf("writing languages deleted the author; the unit of replacement must be the definition")
	}
}

// ─── The doc projection ─────────────────────────────────────────────────

// attributes_doc must agree with the typed rows after every write. A doc that
// can drift is worse than no doc, because the search index believes it.
func TestAttributeValueDocMatchesTheTypedRows(t *testing.T) {
	ctx := context.Background()
	f := newValueFixture(t)

	author, binding := "R. K. Narayan", "paperback"
	pages, weight := 328.0, 250.0
	inPrint := true
	published := time.Date(2019, 4, 2, 0, 0, 0, 0, time.UTC)
	mediaID := uuid.New()
	gram := "g"
	en, hi := "en", "hi"

	if err := f.store.PutProductAttributeValues(ctx, f.product, []AttributeValueSet{
		{DefinitionID: f.defs["author"], Values: []ProductAttributeValue{{ValueText: &author}}},
		{DefinitionID: f.defs["pages"], Values: []ProductAttributeValue{{ValueNum: &pages}}},
		{DefinitionID: f.defs["inprint"], Values: []ProductAttributeValue{{ValueBool: &inPrint}}},
		{DefinitionID: f.defs["published"], Values: []ProductAttributeValue{{ValueDate: &published}}},
		{DefinitionID: f.defs["cover"], Values: []ProductAttributeValue{{ValueMediaID: &mediaID}}},
		{DefinitionID: f.defs["weight"], Values: []ProductAttributeValue{{ValueNum: &weight, UnitCode: &gram}}},
		{DefinitionID: f.defs["binding"], Values: []ProductAttributeValue{{ValueText: &binding}}},
		{DefinitionID: f.defs["language"], Values: []ProductAttributeValue{{ValueText: &en}, {ValueText: &hi}}},
	}); err != nil {
		t.Fatalf("put: %v", err)
	}

	raw, err := f.store.ProductAttributesDoc(ctx, f.product)
	if err != nil {
		t.Fatalf("doc: %v", err)
	}
	doc := map[string]any{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("doc is not json: %v (%s)", err, raw)
	}

	if got := doc[f.codes["author"]]; got != author {
		t.Fatalf("author: want %q, got %#v", author, got)
	}
	if got := doc[f.codes["pages"]]; got != 328.0 {
		t.Fatalf("pages must be a JSON NUMBER, not the string '328': got %#v", got)
	}
	if got := doc[f.codes["inprint"]]; got != true {
		t.Fatalf("in print: want true, got %#v", got)
	}
	if got := doc[f.codes["published"]]; got != "2019-04-02" {
		t.Fatalf("published: want 2019-04-02, got %#v", got)
	}
	if got := doc[f.codes["cover"]]; got != mediaID.String() {
		t.Fatalf("cover: want %s, got %#v", mediaID, got)
	}

	// A measure carries its unit INSIDE its value. A bare 250 is not
	// filterable against a catalogue where other sellers typed kilograms.
	m, ok := doc[f.codes["weight"]].(map[string]any)
	if !ok {
		t.Fatalf("a measure must project as {value, unit}, got %#v", doc[f.codes["weight"]])
	}
	if m["value"] != 250.0 || m["unit"] != "g" {
		t.Fatalf("weight: want {250, g}, got %#v", m)
	}

	// A multi_enum is ALWAYS an array, so a consumer never type-switches.
	langs, ok := doc[f.codes["language"]].([]any)
	if !ok || len(langs) != 2 || langs[0] != "en" || langs[1] != "hi" {
		t.Fatalf("language: want [en hi] as an array, got %#v", doc[f.codes["language"]])
	}

	// Now shrink and clear, and prove the doc follows DOWN as well as up. A
	// projection that only ever grows is the failure that leaves a product
	// matching a facet it no longer answers.
	if err := f.store.PutProductAttributeValues(ctx, f.product, []AttributeValueSet{
		{DefinitionID: f.defs["language"], Values: []ProductAttributeValue{{ValueText: &en}}},
		{DefinitionID: f.defs["author"]}, // no values: clear it
	}); err != nil {
		t.Fatalf("shrink: %v", err)
	}
	raw, _ = f.store.ProductAttributesDoc(ctx, f.product)
	doc = map[string]any{}
	_ = json.Unmarshal(raw, &doc)

	if _, still := doc[f.codes["author"]]; still {
		t.Fatalf("a cleared field must leave the doc, but author is still there: %s", raw)
	}
	if langs, _ := doc[f.codes["language"]].([]any); len(langs) != 1 {
		t.Fatalf("language must shrink to one entry, got %#v", doc[f.codes["language"]])
	}

	// The doc and the rows must agree, field for field — the invariant the
	// whole same-transaction rebuild exists to hold.
	rows, _ := f.store.ProductAttributeValues(ctx, f.product)
	codesInRows := map[string]bool{}
	for _, r := range rows {
		codesInRows[r.Definition.Code] = true
	}
	if len(codesInRows) != len(doc) {
		t.Fatalf("doc has %d fields, the rows have %d: they have drifted\ndoc: %s", len(doc), len(codesInRows), raw)
	}
	for code := range doc {
		if !codesInRows[code] {
			t.Fatalf("doc carries %q, which no row answers", code)
		}
	}
}

// The doc must not be reachable except through a write. This is a design
// assertion rather than a behavioural one: it guards the reason the rebuild is
// unexported, which a later change could quietly undo.
func TestAttributeValueDocIsRebuiltOnlyInsideTheWrite(t *testing.T) {
	ctx := context.Background()
	f := newValueFixture(t)

	// A row inserted BEHIND the store's back leaves the doc stale, and the
	// next legitimate write repairs it. That is the contract: the doc tracks
	// what went through PutProductAttributeValues.
	author := "Behind The Back"
	mustExec(t, `INSERT INTO product_attributes
	    (product_id, definition_id, name, value, position, value_text)
	    VALUES ($1,$2,'x','x',0,$3)`, f.product, f.defs["author"], author)

	raw, _ := f.store.ProductAttributesDoc(ctx, f.product)
	if string(raw) != "{}" {
		t.Fatalf("a raw INSERT must not update the doc; got %s", raw)
	}

	pages := 12.0
	if err := f.store.PutProductAttributeValues(ctx, f.product, []AttributeValueSet{
		{DefinitionID: f.defs["pages"], Values: []ProductAttributeValue{{ValueNum: &pages}}},
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	raw, _ = f.store.ProductAttributesDoc(ctx, f.product)
	doc := map[string]any{}
	_ = json.Unmarshal(raw, &doc)
	if doc[f.codes["author"]] != author {
		t.Fatalf("the next write must rebuild the whole doc, not just its own field: %s", raw)
	}
}

// ─── The three readers ──────────────────────────────────────────────────

// Migration 026 moved product detail, the cart's product batch and the shared
// storefront summary off the EAV row and onto products.source_image_url.
//
// The proof is that all three return the URL the EAV row holds — the same
// answer, from the new place. The fixture writes ONLY the EAV row and lets the
// migration's backfill (re-run here as the same statement) fill the column, so
// this exercises the actual retirement path rather than a column the test
// populated itself.
func TestSourceImageURLReadersAgreeWithTheRetiredEAVRow(t *testing.T) {
	ctx := context.Background()
	f := newValueFixture(t)

	// The & and the %3D are the reason the brief insists on JSON decoding
	// rather than shell text extraction: they survive a round trip through
	// JSON as \u0026, and a text diff reports a difference that is not one.
	want := "https://cdn.example.test/img/reader.jpg?w=800&h=800&sig=abc%3D%3D&fm=webp"
	decoy := "https://cdn.example.test/img/decoy.jpg?x=1&y=2"

	// Two rows. The readers all said ORDER BY sort_order LIMIT 1, so the
	// higher one must lose — a backfill that took the wrong row would swap
	// every multiply-imported product's image.
	mustExec(t, `INSERT INTO product_attributes (product_id,name,value,sort_order)
	             VALUES ($1,'source_image_url',$2,0)`, f.product, want)
	mustExec(t, `INSERT INTO product_attributes (product_id,name,value,sort_order)
	             VALUES ($1,'source_image_url',$2,5)`, f.product, decoy)

	// The migration's backfill, verbatim.
	mustExec(t, `
		UPDATE products p SET source_image_url = a.value
		  FROM (SELECT DISTINCT ON (pa.product_id) pa.product_id, pa.value
		          FROM product_attributes pa
		         WHERE pa.name='source_image_url' AND pa.definition_id IS NULL AND pa.value <> ''
		         ORDER BY pa.product_id, pa.sort_order, pa.id) a
		 WHERE a.product_id = p.id AND p.source_image_url IS DISTINCT FROM a.value`)

	// Reader 1 — product detail (store.go).
	detail, err := f.store.GetProductByID(ctx, f.product)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	assertImageURL(t, "product detail", detail.SourceImageURL, want)

	// Reader 2 — the cart's product batch (batch.go).
	batch, err := f.store.GetProductsByIDs(ctx, []uuid.UUID{f.product})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	assertImageURL(t, "product batch", batch[f.product].SourceImageURL, want)

	// Reader 3 — the shared summary projection (storefront.go), which is home,
	// browse, favourites and the seller's catalogue at once.
	summaries, _, err := f.store.ListSellerProducts(ctx, f.seller, "", true, 50, 0)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	var found *Product
	for _, p := range summaries {
		if p.ID == f.product {
			found = p
		}
	}
	if found == nil {
		t.Fatalf("the summary projection did not return the fixture product")
	}
	assertImageURL(t, "summary projection", found.SourceImageURL, want)

	// And the retired row is still there. Deleting it is a later, gated step;
	// until the fleet has drained, a pod on the old image still reads it.
	var legacy int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM product_attributes WHERE product_id=$1 AND name='source_image_url'`,
		f.product).Scan(&legacy); err != nil {
		t.Fatalf("counting legacy rows: %v", err)
	}
	if legacy != 2 {
		t.Fatalf("the EAV rows must survive the move (rollback path + old pods), found %d", legacy)
	}
}

// The legacy free-text writer keeps the column in step, including clearing it.
func TestSourceImageURLDualWriteKeepsTheColumnInStep(t *testing.T) {
	ctx := context.Background()
	f := newValueFixture(t)
	url := "https://cdn.example.test/img/written.jpg?a=1&b=2"

	if err := f.store.SetProductAttributes(ctx, f.product, []ProductAttribute{
		{Name: "source_image_url", Value: url},
		{Name: "colour", Value: "red"},
	}); err != nil {
		t.Fatalf("set: %v", err)
	}
	p, _ := f.store.GetProductByID(ctx, f.product)
	assertImageURL(t, "after a legacy write", p.SourceImageURL, url)

	// Replacing the block WITHOUT the image row must clear the column, because
	// that is exactly what the old readers did once the row was gone. A
	// dual-write that only ever sets is a dual-write that goes stale.
	if err := f.store.SetProductAttributes(ctx, f.product, []ProductAttribute{
		{Name: "colour", Value: "blue"},
	}); err != nil {
		t.Fatalf("set without the image: %v", err)
	}
	p, _ = f.store.GetProductByID(ctx, f.product)
	if p.SourceImageURL != nil {
		t.Fatalf("removing the EAV row must clear the column, got %q", *p.SourceImageURL)
	}

	// And the legacy writer must not touch typed rows. Before 026 it deleted
	// every row for the product, which would now take a seller's whole
	// specification block with it.
	pages := 42.0
	if err := f.store.PutProductAttributeValues(ctx, f.product, []AttributeValueSet{
		{DefinitionID: f.defs["pages"], Values: []ProductAttributeValue{{ValueNum: &pages}}},
	}); err != nil {
		t.Fatalf("put typed: %v", err)
	}
	if err := f.store.SetProductAttributes(ctx, f.product, []ProductAttribute{
		{Name: "colour", Value: "green"},
	}); err != nil {
		t.Fatalf("set again: %v", err)
	}
	rows, _ := f.store.ProductAttributeValues(ctx, f.product)
	if len(rows) != 1 {
		t.Fatalf("the legacy free-text write deleted the typed values: %d left", len(rows))
	}
}

func assertImageURL(t *testing.T, surface string, got *string, want string) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: no image URL at all", surface)
	}
	if *got != want {
		t.Fatalf("%s:\n want %q\n  got %q", surface, want, *got)
	}
}

// ─── Ordering ───────────────────────────────────────────────────────────

// The store orders within a group by the category binding's sort order. The
// GROUP order is the service layer's job (displayGroupOrder), so this asserts
// only the half the SQL owns.
func TestAttributeValueReadIsOrderedByBindingWithinAGroup(t *testing.T) {
	ctx := context.Background()
	f := newValueFixture(t)

	author, binding := "A", "paperback"
	pages := 100.0
	inPrint := true
	if err := f.store.PutProductAttributeValues(ctx, f.product, []AttributeValueSet{
		{DefinitionID: f.defs["binding"], Values: []ProductAttributeValue{{ValueText: &binding}}},
		{DefinitionID: f.defs["inprint"], Values: []ProductAttributeValue{{ValueBool: &inPrint}}},
		{DefinitionID: f.defs["author"], Values: []ProductAttributeValue{{ValueText: &author}}},
		{DefinitionID: f.defs["pages"], Values: []ProductAttributeValue{{ValueNum: &pages}}},
	}); err != nil {
		t.Fatalf("put: %v", err)
	}

	rows, err := f.store.ProductAttributeValues(ctx, f.product)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// All four are in "Product Details", bound at 10, 20, 30, 40.
	want := []string{f.codes["pages"], f.codes["author"], f.codes["inprint"], f.codes["binding"]}
	if len(rows) != len(want) {
		t.Fatalf("want %d rows, got %d", len(want), len(rows))
	}
	for i, code := range want {
		if rows[i].Definition.Code != code {
			got := make([]string, len(rows))
			for j, r := range rows {
				got[j] = r.Definition.Code
			}
			t.Fatalf("binding sort order ignored:\n want %v\n  got %v", want, got)
		}
		if rows[i].DisplayGroup != "Product Details" {
			t.Fatalf("%s: want group Product Details, got %q", code, rows[i].DisplayGroup)
		}
	}
}
