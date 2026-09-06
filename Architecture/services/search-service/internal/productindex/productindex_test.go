package productindex

// The conversion the consumer and the reindex share.
//
// It is the one place a document's shape is decided, so these tests are the
// contract for what "a product is searchable" actually means: it can be
// matched by title, filtered by its department as well as its leaf
// category, sorted by a price that is an integer count of paise, and
// faceted on attribute values that stay attached to their codes.

import (
	"testing"
	"time"

	"github.com/atpost/search-service/internal/commerceclient"
)

func booksTextbooksPhysics() commerceclient.SearchDoc {
	return commerceclient.SearchDoc{
		ProductID:      "11111111-1111-1111-1111-111111111111",
		SellerID:       "22222222-2222-2222-2222-222222222222",
		SellerName:     "Blossom Books",
		Visible:        true,
		Status:         "active",
		ApprovalStatus: "approved",
		Title:          "Swami and Friends",
		Description:    "Malgudi, 1935.",
		BrandName:      "Indian Thought",
		CategoryID:     "cccccccc-cccc-cccc-cccc-cccccccccc03",
		CategoryName:   "Physics",
		CategoryPath: []commerceclient.Category{
			{ID: "cccccccc-cccc-cccc-cccc-cccccccccc01", Name: "Books", Slug: "books"},
			{ID: "cccccccc-cccc-cccc-cccc-cccccccccc02", Name: "Textbooks", Slug: "textbooks"},
			{ID: "cccccccc-cccc-cccc-cccc-cccccccccc03", Name: "Physics", Slug: "physics"},
		},
		MinPriceMinor: 129900,
		MaxPriceMinor: 189900,
		Currency:      "INR",
		TotalStock:    8,
		InStock:       true,
		CreatedAt:     time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	}
}

// The central claim of the category half of this step.
func TestTheWholeAncestorChainIsIndexed(t *testing.T) {
	doc := Doc(booksTextbooksPhysics())

	wantIDs := []string{
		"cccccccc-cccc-cccc-cccc-cccccccccc01",
		"cccccccc-cccc-cccc-cccc-cccccccccc02",
		"cccccccc-cccc-cccc-cccc-cccccccccc03",
	}
	if len(doc.CategoryIDs) != 3 {
		t.Fatalf("category_ids = %v, want all three rungs — a filter on the DEPARTMENT has to "+
			"match a listing filed under a leaf three levels down", doc.CategoryIDs)
	}
	for i, want := range wantIDs {
		if doc.CategoryIDs[i] != want {
			t.Fatalf("category_ids[%d] = %q, want %q (root-first)", i, doc.CategoryIDs[i], want)
		}
	}
	if doc.CategoryNames[0] != "Books" {
		t.Fatalf("category_names[0] = %q, want %q — the department a shopper clicks",
			doc.CategoryNames[0], "Books")
	}
	if doc.CategorySlugs[1] != "textbooks" {
		t.Fatalf("category_slugs[1] = %q, want %q", doc.CategorySlugs[1], "textbooks")
	}
	if doc.CategoryPath != "Books > Textbooks > Physics" {
		t.Fatalf("category_path = %q, want the breadcrumb root-first", doc.CategoryPath)
	}
	// The leaf still answers under the legacy single-value field the
	// existing /v1/search/products response carries.
	if doc.Category != "Physics" || doc.CategoryName != "Physics" {
		t.Fatalf("category / category_name = %q / %q, want the leaf", doc.Category, doc.CategoryName)
	}
}

// A category that resolved to no chain must still answer for itself.
func TestALeafWithNoChainStillFilters(t *testing.T) {
	src := booksTextbooksPhysics()
	src.CategoryPath = nil
	doc := Doc(src)

	if len(doc.CategoryIDs) != 1 || doc.CategoryIDs[0] != src.CategoryID {
		t.Fatalf("category_ids = %v, want just the leaf — better a filter that matches only the "+
			"exact category than one that matches nothing", doc.CategoryIDs)
	}
	if len(doc.CategoryNames) != 1 || doc.CategoryNames[0] != "Physics" {
		t.Fatalf("category_names = %v, want just the leaf", doc.CategoryNames)
	}
}

// Money: paise are the truth, the rupee float is a rendering of them.
func TestPriceIsIndexedInMinorUnits(t *testing.T) {
	doc := Doc(booksTextbooksPhysics())

	if doc.MinPriceMinor != 129900 || doc.MaxPriceMinor != 189900 {
		t.Fatalf("price range = %d..%d minor, want 129900..189900",
			doc.MinPriceMinor, doc.MaxPriceMinor)
	}
	if doc.Price != 1299.00 {
		t.Fatalf("legacy price mirror = %v, want 1299.00 — it is derived from the paise, never "+
			"carried separately", doc.Price)
	}
	if doc.Currency != "INR" {
		t.Fatalf("currency = %q", doc.Currency)
	}
}

// Attribute flattening: the three shapes commerce's attributes_doc can hold.
func TestAttributesFlattenIntoCodeKeyedPairs(t *testing.T) {
	src := booksTextbooksPhysics()
	src.Attributes = map[string]any{
		"author":     "R. K. Narayan",
		"page_count": float64(328),
		"binding":    []any{"paperback", "illustrated"},
		"net_weight": map[string]any{"value": float64(250), "unit": "g"},
	}
	doc := Doc(src)

	byCode := map[string][]string{}
	units := map[string]string{}
	nums := map[string]*float64{}
	for _, p := range doc.Attributes {
		byCode[p.Code] = append(byCode[p.Code], p.Value)
		if p.Unit != "" {
			units[p.Code] = p.Unit
		}
		if p.ValueNum != nil {
			nums[p.Code] = p.ValueNum
		}
	}

	if got := byCode["author"]; len(got) != 1 || got[0] != "R. K. Narayan" {
		t.Fatalf("author pairs = %v, want one", got)
	}
	// A multi_enum becomes one pair PER member, so a filter for "illustrated"
	// matches without the consumer type-switching on the field.
	if got := byCode["binding"]; len(got) != 2 {
		t.Fatalf("binding pairs = %v, want one per member", got)
	}
	if got := byCode["page_count"]; len(got) != 1 || got[0] != "328" {
		t.Fatalf("page_count value = %v, want the string \"328\" — a bucket key has to be "+
			"something a client can echo back as a filter", got)
	}
	if nums["page_count"] == nil || *nums["page_count"] != 328 {
		t.Fatalf("page_count value_num = %v, want 328 — a range filter reads the numeric field",
			nums["page_count"])
	}
	if units["net_weight"] != "g" {
		t.Fatalf("net_weight unit = %q, want \"g\" — 250 g and 250 kg are not the same bucket",
			units["net_weight"])
	}
	if got := byCode["net_weight"]; len(got) != 1 || got[0] != "250" {
		t.Fatalf("net_weight value = %v, want \"250\"", got)
	}
}

// Byte-identical documents from identical input. A reindex must not change
// an index it is only supposed to repair.
func TestAttributeOrderIsStable(t *testing.T) {
	src := booksTextbooksPhysics()
	src.Attributes = map[string]any{
		"zebra": "z", "author": "a", "binding": []any{"b2", "b1"}, "middle": "m",
	}
	first := Doc(src).Attributes
	for i := 0; i < 20; i++ {
		again := Doc(src).Attributes
		if len(again) != len(first) {
			t.Fatalf("run %d produced %d pairs, first run produced %d", i, len(again), len(first))
		}
		for j := range first {
			if again[j] != first[j] {
				t.Fatalf("run %d differs at pair %d: %+v vs %+v.\n"+
					"Map iteration order must not reach the document, or a reindex rewrites "+
					"every product it was only meant to verify.", i, j, again[j], first[j])
			}
		}
	}
	if first[0].Code != "author" {
		t.Fatalf("pairs are not sorted by code; first is %q", first[0].Code)
	}
}

// An attribute value shaped like nothing a facet can bucket is dropped
// rather than stringified into a key nobody can filter on.
func TestUnfacetableAttributeShapesAreDropped(t *testing.T) {
	src := booksTextbooksPhysics()
	src.Attributes = map[string]any{
		"ok":      "yes",
		"nested":  map[string]any{"no_value_key": 1},
		"nothing": nil,
	}
	doc := Doc(src)
	if len(doc.Attributes) != 1 || doc.Attributes[0].Code != "ok" {
		t.Fatalf("attributes = %+v, want only the one facetable pair", doc.Attributes)
	}
}
