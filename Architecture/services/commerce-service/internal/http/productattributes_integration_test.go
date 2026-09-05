//go:build integration

package http

// Product detail carrying its attributes, through the REAL registered route.
//
// Detail returned product + variants + media and nothing else, so every
// category-specific fact a seller entered was stored and then visible nowhere.
// This asserts the block is there, that it is in FORM order — fieldsets in
// displayGroupOrder, fields in their binding's order within each — and that it
// carries codes beside labels so renaming a field stays the no-deploy change
// the registry exists to make it.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/http/... -run ProductDetailAttributes -v

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

type detailAttribute struct {
	Code         string  `json:"code"`
	Label        string  `json:"label"`
	DataType     string  `json:"data_type"`
	Value        any     `json:"value"`
	UnitCode     *string `json:"unit_code"`
	DisplayGroup string  `json:"display_group"`
}

// detailFixture is a product with one attribute in each of three groups,
// deliberately bound in an order that is NOT the order they must render in —
// so a response that merely echoes insertion order fails.
type detailFixture struct {
	category uuid.UUID
	seller   uuid.UUID
	product  uuid.UUID
	codes    map[string]string
	defs     map[string]uuid.UUID
}

func newDetailFixture(t *testing.T) *detailFixture {
	t.Helper()
	ctx := context.Background()
	f := &detailFixture{
		category: uuid.New(), seller: uuid.New(), product: uuid.New(),
		codes: map[string]string{}, defs: map[string]uuid.UUID{},
	}
	tag := strings.ReplaceAll(f.product.String()[:8], "-", "")

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := edgePool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("exec: %v\nSQL: %s", err, sql)
		}
	}

	exec(`INSERT INTO product_categories (id,parent_id,name,slug,display_order,is_active)
	      VALUES ($1,NULL,'Detail Cat',$2,1,TRUE)`, f.category, "detail-cat-"+tag)
	exec(`INSERT INTO sellers (id,user_id,store_name,slug,email,state)
	      VALUES ($1,$2,'Detail Store',$3,'seller@example.test','KA')`, f.seller, uuid.New(), "detail-store-"+tag)
	exec(`INSERT INTO products (id,seller_id,category_id,title,slug,status,approval_status)
	      VALUES ($1,$2,$3,'Detail Product',$4,'active','approved')`,
		f.product, f.seller, f.category, "detail-product-"+tag)

	// Inserted logistics-first and identity-last, with sort orders that
	// disagree with insertion order inside "Product Details".
	for _, spec := range []struct {
		key, label, dataType, group string
		sortOrder                   int
	}{
		{"weight", "Item weight", "measure", "Logistics", 10},
		{"binding", "Binding", "enum", "Product Details", 40},
		{"author", "Author", "text", "Product Details", 20},
		{"pages", "Number of pages", "integer", "Product Details", 10},
		{"published", "Publication date", "date", "Product Identity", 10},
	} {
		id := uuid.New()
		code := fmt.Sprintf("%s_%s", spec.key, tag)
		f.defs[spec.key], f.codes[spec.key] = id, code
		var family, unit any
		if spec.dataType == "measure" {
			family, unit = "mass", "g"
		}
		exec(`INSERT INTO attribute_definitions
		      (id,code,label,data_type,unit_family,default_unit,display_group,applies_to)
		      VALUES ($1,$2,$3,$4,$5,$6,$7,'item')`,
			id, code, spec.label, spec.dataType, family, unit, spec.group)
		exec(`INSERT INTO category_attributes (category_id,definition_id,sort_order)
		      VALUES ($1,$2,$3)`, f.category, id, spec.sortOrder)
	}
	exec(`INSERT INTO attribute_enum_values (definition_id,code,label) VALUES ($1,'paperback','Paperback')`,
		f.defs["binding"])

	t.Cleanup(func() {
		exec(`DELETE FROM product_attributes WHERE product_id=$1`, f.product)
		exec(`DELETE FROM products WHERE id=$1`, f.product)
		exec(`DELETE FROM sellers WHERE id=$1`, f.seller)
		for _, id := range f.defs {
			exec(`DELETE FROM attribute_definitions WHERE id=$1`, id)
		}
		exec(`DELETE FROM product_categories WHERE id=$1`, f.category)
	})
	return f
}

func TestProductDetailAttributesAreReturnedInGroupOrder(t *testing.T) {
	ctx := context.Background()
	f := newDetailFixture(t)
	store := postgres.New(edgePool)

	author, binding, gram := "R. K. Narayan", "paperback", "g"
	pages, weight := 328.0, 250.0
	published := mustDate(t, "2019-04-02")

	if err := store.PutProductAttributeValues(ctx, f.product, []postgres.AttributeValueSet{
		{DefinitionID: f.defs["weight"], Values: []postgres.ProductAttributeValue{{ValueNum: &weight, UnitCode: &gram}}},
		{DefinitionID: f.defs["binding"], Values: []postgres.ProductAttributeValue{{ValueText: &binding}}},
		{DefinitionID: f.defs["author"], Values: []postgres.ProductAttributeValue{{ValueText: &author}}},
		{DefinitionID: f.defs["pages"], Values: []postgres.ProductAttributeValue{{ValueNum: &pages}}},
		{DefinitionID: f.defs["published"], Values: []postgres.ProductAttributeValue{{ValueDate: &published}}},
	}); err != nil {
		t.Fatalf("seeding values: %v", err)
	}

	r := liveEngine(t)
	w, envelope := doJSON(t, r, http.MethodGet, "/v1/commerce/products/"+f.product.String(), nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("detail: want 200, got %d — %s", w.Code, w.Body.String())
	}

	var data struct {
		Attributes []detailAttribute `json:"attributes"`
	}
	if err := json.Unmarshal(envelope["data"], &data); err != nil {
		t.Fatalf("decoding data: %v — %s", err, envelope["data"])
	}

	// Identity, then details in binding order, then logistics. Insertion order
	// was logistics first, so echoing it would fail here.
	want := []string{
		f.codes["published"], // Product Identity
		f.codes["pages"],     // Product Details, bound 10
		f.codes["author"],    // Product Details, bound 20
		f.codes["binding"],   // Product Details, bound 40
		f.codes["weight"],    // Logistics
	}
	if len(data.Attributes) != len(want) {
		t.Fatalf("want %d attributes, got %d: %+v", len(want), len(data.Attributes), data.Attributes)
	}
	got := make([]string, len(data.Attributes))
	for i, a := range data.Attributes {
		got[i] = a.Code
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attributes are not in form order:\n want %v\n  got %v", want, got)
		}
	}

	byCode := map[string]detailAttribute{}
	for _, a := range data.Attributes {
		byCode[a.Code] = a
	}

	// Labels travel WITH codes. A response carrying only the label would make
	// renaming a field break every client that keyed anything on it.
	if a := byCode[f.codes["author"]]; a.Label != "Author" || a.DataType != "text" || a.Value != author {
		t.Fatalf("author: want label/type/value Author/text/%q, got %+v", author, a)
	}
	// A number is a number, not the string "328" — the whole point of typing.
	if a := byCode[f.codes["pages"]]; a.Value != 328.0 {
		t.Fatalf("pages must be a JSON number, got %#v", a.Value)
	}
	if a := byCode[f.codes["published"]]; a.Value != "2019-04-02" {
		t.Fatalf("published: want 2019-04-02, got %#v", a.Value)
	}
	// The unit rides beside the value rather than inside it, so a client can
	// format "250 g" itself.
	a := byCode[f.codes["weight"]]
	if a.Value != 250.0 || a.UnitCode == nil || *a.UnitCode != "g" {
		t.Fatalf("weight: want 250 with unit g, got value=%#v unit=%#v", a.Value, a.UnitCode)
	}
	if a.DisplayGroup != "Logistics" {
		t.Fatalf("weight: want group Logistics, got %q", a.DisplayGroup)
	}
}

// A product with no typed values gets an empty list, not a failure and not a
// missing key. The block is new; every product in the catalogue is in this
// state until somebody fills a form in.
func TestProductDetailAttributesAreEmptyNotAbsentWhenNoneAreSet(t *testing.T) {
	f := newDetailFixture(t)
	r := liveEngine(t)

	w, envelope := doJSON(t, r, http.MethodGet, "/v1/commerce/products/"+f.product.String(), nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — %s", w.Code, w.Body.String())
	}
	var data struct {
		Attributes *[]detailAttribute `json:"attributes"`
	}
	if err := json.Unmarshal(envelope["data"], &data); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if data.Attributes == nil {
		t.Fatalf("the key must be present and empty, not absent: %s", envelope["data"])
	}
	if len(*data.Attributes) != 0 {
		t.Fatalf("want no attributes, got %d", len(*data.Attributes))
	}
}

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("bad fixture date %q: %v", s, err)
	}
	return d
}
