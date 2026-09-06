//go:build integration

package postgres

// The search document, against a real database.
//
// The thing being proved is the one the old offline backfill got wrong. It
// hand-wrote a SELECT over `products` and therefore set neither the
// category nor the price — so the index it produced could not be filtered
// or sorted, which is most of what a product search is.
//
//	COMMERCE_TEST_DSN=postgres://postgres:postgres@127.0.0.1:5432/commerce_it_test?sslmode=disable \
//	  go test -tags=integration ./internal/store/postgres/... -run SearchDoc -v -count=1

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// seedCategoryChain creates Root › Child › Grandchild and returns the ids
// root-first.
func seedCategoryChain(t *testing.T, names ...string) []uuid.UUID {
	t.Helper()
	ids := make([]uuid.UUID, 0, len(names))
	var parent *uuid.UUID
	for i, name := range names {
		id := uuid.New()
		slug := name + "-" + id.String()[:8]
		mustExec(t, `INSERT INTO product_categories (id,parent_id,name,slug,is_active)
		             VALUES ($1,$2,$3,$4,TRUE)`, id, parent, name+"-"+id.String()[:4], slug)
		ids = append(ids, id)
		p := ids[i]
		parent = &p
	}
	return ids
}

// TestSearchDocCarriesTheAncestorChain is the "a Books filter matches a
// Textbooks listing" proof.
//
// The product is filed under the LEAF. The document must nevertheless carry
// every rung from the root down, or a category filter is exact-match on a
// leaf — which is a filter no shopper can use, because a shopper clicks the
// department.
func TestSearchDocCarriesTheAncestorChain(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 7, 129900, "")
	chain := seedCategoryChain(t, "Books", "Textbooks", "Physics")
	mustExec(t, `UPDATE products SET category_id=$2 WHERE id=$1`, f.productID, chain[2])

	doc, err := f.store.ProductSearchDoc(ctx, f.productID)
	if err != nil {
		t.Fatalf("ProductSearchDoc: %v", err)
	}

	if len(doc.CategoryPath) != 3 {
		t.Fatalf("category_path has %d rungs, want 3 (root, child, leaf): %+v",
			len(doc.CategoryPath), doc.CategoryPath)
	}
	// Root FIRST, leaf LAST — the order a breadcrumb reads in, and the order
	// a consumer relies on when it takes the tail as "the category".
	if doc.CategoryPath[0].ID != chain[0] {
		t.Fatalf("category_path[0] is %s, want the ROOT %s — the chain is root-first",
			doc.CategoryPath[0].ID, chain[0])
	}
	if doc.CategoryPath[2].ID != chain[2] {
		t.Fatalf("category_path[2] is %s, want the LEAF %s — the leaf is included, so a "+
			"consumer needs exactly one field to answer 'which categories does this product "+
			"belong to'", doc.CategoryPath[2].ID, chain[2])
	}
	if doc.CategoryID == nil || *doc.CategoryID != chain[2] {
		t.Fatalf("category_id is %v, want the leaf %s", doc.CategoryID, chain[2])
	}
	for i, rung := range doc.CategoryPath {
		if rung.Name == "" || rung.Slug == "" {
			t.Fatalf("category_path[%d] is missing a name or slug: %+v — the filter matches on "+
				"id, the breadcrumb renders the name, the URL is built from the slug", i, rung)
		}
	}
}

// A product with no category must not blow up and must not invent one.
func TestSearchDocWithNoCategory(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 1, 5000, "")
	mustExec(t, `UPDATE products SET category_id=NULL WHERE id=$1`, f.productID)

	doc, err := f.store.ProductSearchDoc(ctx, f.productID)
	if err != nil {
		t.Fatalf("ProductSearchDoc: %v", err)
	}
	if doc.CategoryID != nil {
		t.Fatalf("category_id = %v, want nil", doc.CategoryID)
	}
	if doc.CategoryPath == nil {
		t.Fatal("category_path is nil; it must be an empty slice so a consumer never has to " +
			"distinguish 'absent' from 'empty'")
	}
	if len(doc.CategoryPath) != 0 {
		t.Fatalf("category_path = %+v, want empty", doc.CategoryPath)
	}
}

// TestSearchDocCarriesPriceAndStock — the other half of what the old
// backfill omitted. Money in MINOR units, from the cheapest ACTIVE variant.
func TestSearchDocCarriesPriceAndStock(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 4, 129900, "") // one variant at ₹1299.00, stock 4

	// A dearer second variant, and an archived cheap one that must NOT set
	// the floor: an archived variant is a price nobody can pay, and letting
	// it win puts the listing at the top of a cheapest-first sort for an
	// offer that no longer exists.
	dear, cheapArchived := uuid.New(), uuid.New()
	mustExec(t, `INSERT INTO product_variants (id,product_id,sku,mrp,selling_price,mrp_minor,selling_price_minor,status)
	             VALUES ($1,$2,$3,1899.00,1899.00,189900,189900,'active')`,
		dear, f.productID, "SKU-"+dear.String()[:8])
	mustExec(t, `INSERT INTO product_variants (id,product_id,sku,mrp,selling_price,mrp_minor,selling_price_minor,status)
	             VALUES ($1,$2,$3,99.00,99.00,9900,9900,'archived')`,
		cheapArchived, f.productID, "SKU-"+cheapArchived.String()[:8])
	mustExec(t, `INSERT INTO inventory_items (variant_id,seller_id,total_qty,reserved_qty)
	             VALUES ($1,$2,6,2)`, dear, f.sellerID)

	doc, err := f.store.ProductSearchDoc(ctx, f.productID)
	if err != nil {
		t.Fatalf("ProductSearchDoc: %v", err)
	}
	if doc.MinPriceMinor != 129900 {
		t.Fatalf("min_price_minor = %d, want 129900 — the archived ₹99 variant must not set the "+
			"floor", doc.MinPriceMinor)
	}
	if doc.MaxPriceMinor != 189900 {
		t.Fatalf("max_price_minor = %d, want 189900", doc.MaxPriceMinor)
	}
	// 4 available on the first variant + (6 - 2 reserved) on the second.
	if doc.TotalStock != 8 {
		t.Fatalf("total_stock = %d, want 8 (available, not total — reserved units are spoken for)",
			doc.TotalStock)
	}
	if !doc.InStock {
		t.Fatal("in_stock is false with 8 available")
	}
	if doc.Currency == "" {
		t.Fatal("currency is empty; a price with no currency is not a price")
	}
}

// TestSearchDocVisibilityTracksTheLifecycle — the field the consumer acts
// on. It is computed at read time from the product's own columns, which is
// what makes the consumer's decision independent of which event woke it.
func TestSearchDocVisibilityTracksTheLifecycle(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 1, 1000, "") // seeded active + approved

	doc, err := f.store.ProductSearchDoc(ctx, f.productID)
	if err != nil {
		t.Fatalf("ProductSearchDoc: %v", err)
	}
	if !doc.Visible {
		t.Fatalf("an active+approved product reads visible=false (status=%q approval=%q)",
			doc.Status, doc.ApprovalStatus)
	}

	mustExec(t, `UPDATE products SET approval_status='rejected' WHERE id=$1`, f.productID)
	seedOfferFor(t, f.productID)

	doc, err = f.store.ProductSearchDoc(ctx, f.productID)
	if err != nil {
		t.Fatalf("ProductSearchDoc after reject: %v", err)
	}
	if doc.Visible {
		t.Fatal("a rejected product still reads visible=true — the index would keep offering a " +
			"listing a moderator refused")
	}
}

// TestSearchDocUsesTheStoredAttributeProjection — attributes_doc verbatim,
// not a re-derivation. The doc is rebuilt inside the same transaction as
// any value write, so it cannot disagree with the typed rows; a second
// reader that re-joined product_attributes would be a third opinion.
func TestSearchDocUsesTheStoredAttributeProjection(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 1, 1000, "")
	mustExec(t, `UPDATE products SET attributes_doc = $2::jsonb WHERE id=$1`, f.productID,
		`{"author":"R. K. Narayan","page_count":328,"binding":["paperback","illustrated"],`+
			`"net_weight":{"value":250,"unit":"g"}}`)

	doc, err := f.store.ProductSearchDoc(ctx, f.productID)
	if err != nil {
		t.Fatalf("ProductSearchDoc: %v", err)
	}
	if doc.Attributes["author"] != "R. K. Narayan" {
		t.Fatalf("attributes.author = %v, want the stored projection verbatim", doc.Attributes["author"])
	}
	if got, ok := doc.Attributes["binding"].([]any); !ok || len(got) != 2 {
		t.Fatalf("attributes.binding = %#v, want the two-member array as stored — a multi_enum "+
			"is always an array so a consumer never has to type-switch", doc.Attributes["binding"])
	}
	measure, ok := doc.Attributes["net_weight"].(map[string]any)
	if !ok || measure["unit"] != "g" {
		t.Fatalf("attributes.net_weight = %#v, want {value,unit} — a number with no unit is not "+
			"filterable", doc.Attributes["net_weight"])
	}
}

// TestListProductSearchDocsWalksTheCatalogueExactlyOnce — the keyset walk.
//
// Offset paging over a live catalogue skips or repeats rows every time a
// product is inserted underneath it, and the whole point of a reindex is
// that afterwards you can say what is in the index. This walks the entire
// live catalogue in small pages and asserts nothing is seen twice and
// everything visible is seen once.
func TestListProductSearchDocsWalksTheCatalogueExactlyOnce(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 1, 1000, "")
	store := f.store

	// The ids that are live BEFORE the walk starts.
	//
	// Comparing the walk's tally against a COUNT taken beforehand assumes the
	// catalogue stands still, and it does not: `go test ./...` runs this
	// package beside internal/http, whose fixtures create and approve
	// products against the same database, so the count moved under the walk
	// and this test failed on every concurrent run. What it is actually for
	// is narrower and worth keeping — that keyset paging never SKIPS a row —
	// so it checks exactly that: every row that was live before the walk, and
	// is still live after it, must have been visited.
	liveIDs := func() map[uuid.UUID]struct{} {
		t.Helper()
		rows, err := testPool.Query(ctx,
			`SELECT p.id `+productsLiveFrom+` WHERE `+productSummaryLive)
		if err != nil {
			t.Fatalf("live ids: %v", err)
		}
		defer rows.Close()
		out := map[uuid.UUID]struct{}{}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				t.Fatalf("scan live id: %v", err)
			}
			out[id] = struct{}{}
		}
		return out
	}

	before := liveIDs()
	if len(before) == 0 {
		t.Fatal("the fixture seeded an active+approved product; the live set cannot be empty")
	}

	seen := map[uuid.UUID]int{}
	var afterAt *time.Time
	var afterID *uuid.UUID
	const page = 50
	for pages := 0; ; pages++ {
		if pages > 2000 {
			t.Fatal("the walk did not terminate — the cursor is not advancing")
		}
		docs, err := store.ListProductSearchDocs(ctx, true, afterAt, afterID, page)
		if err != nil {
			t.Fatalf("ListProductSearchDocs: %v", err)
		}
		if len(docs) == 0 {
			break
		}
		for _, d := range docs {
			seen[d.ProductID]++
			if !d.Visible {
				t.Fatalf("product %s came back from the visible-only walk with visible=false "+
					"(status=%q approval=%q) — a reindex would put an unapproved listing into search",
					d.ProductID, d.Status, d.ApprovalStatus)
			}
		}
		last := docs[len(docs)-1]
		at, id := last.CreatedAt, last.ProductID
		afterAt, afterID = &at, &id
		if len(docs) < page {
			break
		}
	}

	for id, n := range seen {
		if n != 1 {
			t.Fatalf("product %s was returned %d times; the keyset walk must visit each row once", id, n)
		}
	}
	// A row live on both sides of the walk that the walk never returned is a
	// skip, which is the defect this test exists for. A row that was live
	// before and is not live now was retired by a concurrent test, and its
	// absence says nothing about the cursor.
	after := liveIDs()
	for id := range before {
		if _, stillLive := after[id]; !stillLive {
			continue
		}
		if _, visited := seen[id]; !visited {
			t.Fatalf("product %s was live before and after the walk but was never returned — "+
				"keyset paging skipped a row, so a reindex reporting 'indexed N' would be lying", id)
		}
	}
	if _, ok := seen[f.productID]; !ok {
		t.Fatalf("the fixture's own product %s was never returned by the walk", f.productID)
	}
}

// TestFacetDefinitionsAreTheFilterableOnes — `is_filterable` is the entire
// gate, and it is an operator's checkbox. That is what makes adding a facet
// a no-deploy change.
func TestFacetDefinitionsAreTheFilterableOnes(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 1, 1000, "")
	lockSchemaState(t)

	on, off := uuid.New(), uuid.New()
	mustExec(t, `INSERT INTO attribute_definitions
	              (id,code,label,data_type,display_group,applies_to,is_filterable,is_active)
	             VALUES ($1,$2,'Binding','enum','Product Details','item',TRUE,TRUE)`,
		on, "binding_"+on.String()[:8])
	mustExec(t, `INSERT INTO attribute_definitions
	              (id,code,label,data_type,display_group,applies_to,is_filterable,is_active)
	             VALUES ($1,$2,'Internal note','text','Product Details','item',FALSE,TRUE)`,
		off, "note_"+off.String()[:8])
	mustExec(t, `INSERT INTO attribute_enum_values (id,definition_id,code,label,sort_order,is_active)
	             VALUES (gen_random_uuid(),$1,'hardcover','Hardcover',1,TRUE),
	                    (gen_random_uuid(),$1,'paperback','Paperback',2,TRUE),
	                    (gen_random_uuid(),$1,'retired','Retired',3,FALSE)`, on)
	t.Cleanup(func() {
		mustExec(t, `DELETE FROM attribute_enum_values WHERE definition_id = ANY($1::uuid[])`,
			[]uuid.UUID{on, off})
		mustExec(t, `DELETE FROM attribute_definitions WHERE id = ANY($1::uuid[])`,
			[]uuid.UUID{on, off})
	})
	_ = f

	defs, err := f.store.FacetDefinitions(ctx)
	if err != nil {
		t.Fatalf("FacetDefinitions: %v", err)
	}
	byID := map[string]*FacetDefinition{}
	for _, d := range defs {
		byID[d.Code] = d
	}

	filterable, ok := byID["binding_"+on.String()[:8]]
	if !ok {
		t.Fatal("a definition with is_filterable=TRUE is missing from FacetDefinitions")
	}
	if _, ok := byID["note_"+off.String()[:8]]; ok {
		t.Fatal("a definition with is_filterable=FALSE came back as a facet — the checkbox is " +
			"the whole gate")
	}

	// Codes AND labels, and only the ACTIVE options. A retired option
	// offered as a filter is a filter that returns nothing.
	if len(filterable.Options) != 2 {
		t.Fatalf("options = %+v, want the two active ones (the retired option must not be offered)",
			filterable.Options)
	}
	for _, o := range filterable.Options {
		if o.Code == "" || o.Label == "" {
			t.Fatalf("option %+v is missing a code or a label — the code is the stable filter key, "+
				"the label is presentation and may be re-worded without a deploy", o)
		}
		if o.Code == o.Label {
			t.Fatalf("option %+v has code == label; the fixture authored different values, so "+
				"one of the two columns is not being read", o)
		}
	}
	if filterable.Label != "Binding" {
		t.Fatalf("definition label = %q, want %q", filterable.Label, "Binding")
	}
}
