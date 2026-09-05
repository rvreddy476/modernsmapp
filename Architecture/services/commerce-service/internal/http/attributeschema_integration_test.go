//go:build integration

package http

// The attribute-schema endpoints through the REAL registered routes, against
// the taxonomy migration 025 seeds.
//
// The store-level proof (internal/store/postgres/attributes_integration_test.go)
// asserts that the inheritance walk resolves correctly. This one asserts the
// other half: that the resolved answer reaches the wire in the shape the
// contract promises, that a client which already has it gets a 304 instead of
// the body, that an unknown category is a 404 rather than an empty form, and
// that a narrowing edit is refused until the operator quotes the damage back.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/http/... -run Schema -v

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func categoryIDBySlug(t *testing.T, slug string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := edgePool.QueryRow(context.Background(),
		`SELECT id FROM product_categories WHERE slug=$1`, slug).Scan(&id); err != nil {
		t.Fatalf("migration 025 must have seeded %q: %v", slug, err)
	}
	return id
}

func doJSON(t *testing.T, r *gin.Engine, method, path string, body any, headers map[string]string) (*httptest.ResponseRecorder, map[string]json.RawMessage) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	envelope := map[string]json.RawMessage{}
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &envelope)
	}
	return w, envelope
}

type schemaField struct {
	Code           string   `json:"code"`
	Label          string   `json:"label"`
	DataType       string   `json:"data_type"`
	Required       bool     `json:"required"`
	Scope          string   `json:"scope"`
	Regex          *string  `json:"regex"`
	MinNum         *float64 `json:"min_num"`
	MaxNum         *float64 `json:"max_num"`
	IsFilterable   bool     `json:"is_filterable"`
	IsVariantAxis  bool     `json:"is_variant_axis"`
	LookupEndpoint *string  `json:"lookup_endpoint"`
	Values         []struct {
		Code  string `json:"code"`
		Label string `json:"label"`
	} `json:"values"`
	UnitFamily  *string `json:"unit_family"`
	DefaultUnit *string `json:"default_unit"`
	Units       []struct {
		Code         string  `json:"code"`
		FactorToBase float64 `json:"factor_to_base"`
	} `json:"units"`
}

type schemaDoc struct {
	CategoryID    string   `json:"category_id"`
	CategoryPath  []string `json:"category_path"`
	SchemaVersion int      `json:"schema_version"`
	VariationAxes []string `json:"variation_axes"`
	Groups        []struct {
		Name       string        `json:"name"`
		SortOrder  int           `json:"sort_order"`
		Attributes []schemaField `json:"attributes"`
	} `json:"groups"`
}

func fetchSchema(t *testing.T, r *gin.Engine, categoryID uuid.UUID, query string, headers map[string]string) (*httptest.ResponseRecorder, schemaDoc) {
	t.Helper()
	path := fmt.Sprintf("/v1/commerce/categories/%s/attribute-schema%s", categoryID, query)
	w, envelope := doJSON(t, r, http.MethodGet, path, nil, headers)
	var doc schemaDoc
	if raw, ok := envelope["data"]; ok {
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("decode schema: %v\n%s", err, w.Body.String())
		}
	}
	return w, doc
}

// Textbooks binds NOTHING of its own. Every field it asks for is inherited
// from Books, which is the case the binding table exists for — a merchandiser
// should be able to describe "a book" once and have every child ask it.
func TestTextbooksInheritsTheBooksSchema(t *testing.T) {
	r := liveEngine(t)
	textbooks := categoryIDBySlug(t, "books-textbooks")

	w, doc := fetchSchema(t, r, textbooks, "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if len(doc.CategoryPath) != 2 || doc.CategoryPath[0] != "Books & Stationery" || doc.CategoryPath[1] != "Textbooks" {
		t.Fatalf("category_path = %v, want the root-first breadcrumb [Books & Stationery Textbooks]", doc.CategoryPath)
	}
	if len(doc.VariationAxes) != 1 || doc.VariationAxes[0] != "binding" {
		t.Fatalf("variation_axes = %v, want [binding]", doc.VariationAxes)
	}

	fields := map[string]schemaField{}
	var groupOrder []string
	for _, g := range doc.Groups {
		groupOrder = append(groupOrder, g.Name)
		for _, f := range g.Attributes {
			fields[f.Code] = f
		}
	}
	for _, code := range []string{"gtin", "author", "binding", "pages", "item_weight", "language", "publication_date"} {
		if _, ok := fields[code]; !ok {
			t.Errorf("Textbooks must inherit %q from Books; it binds nothing of its own", code)
		}
	}

	// Form order, not alphabetical: identity first.
	if len(groupOrder) < 2 || groupOrder[0] != "Product Identity" {
		t.Fatalf("groups = %v, want Product Identity first", groupOrder)
	}
	for i := 1; i < len(doc.Groups); i++ {
		if doc.Groups[i-1].SortOrder > doc.Groups[i].SortOrder {
			t.Fatalf("groups are not in sort_order order: %v", groupOrder)
		}
	}

	gtin := fields["gtin"]
	if !gtin.Required {
		t.Error("gtin is bound required on Books; a book with no ISBN cannot be matched to any other listing of it")
	}
	if gtin.Regex == nil || *gtin.Regex == "" {
		t.Error("gtin must carry its pattern so a client can validate before submitting")
	}
	if gtin.LookupEndpoint != nil {
		t.Error("lookup_endpoint must be null today — and present, so a future searchable enum is not a contract change")
	}

	binding := fields["binding"]
	if len(binding.Values) != 3 {
		t.Errorf("binding must carry its three options inline, got %d", len(binding.Values))
	}
	if !binding.IsVariantAxis {
		t.Error("binding is the variant axis for books")
	}

	weight := fields["item_weight"]
	if weight.UnitFamily == nil || *weight.UnitFamily != "mass" {
		t.Errorf("item_weight must name its unit family, got %v", weight.UnitFamily)
	}
	if weight.DefaultUnit == nil || *weight.DefaultUnit != "g" {
		t.Errorf("item_weight must name its default unit, got %v", weight.DefaultUnit)
	}
	if len(weight.Units) != 5 {
		t.Errorf("item_weight must carry the mass family's five units and their factors, got %d", len(weight.Units))
	}

	pages := fields["pages"]
	if pages.MinNum == nil || *pages.MinNum != 1 || pages.MaxNum == nil || *pages.MaxNum != 10000 {
		t.Errorf("pages must carry its bounds; got min=%v max=%v", pages.MinNum, pages.MaxNum)
	}
}

func TestAttributeSchemaETagAnswers304(t *testing.T) {
	r := liveEngine(t)
	textbooks := categoryIDBySlug(t, "books-textbooks")

	first, _ := fetchSchema(t, r, textbooks, "", nil)
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("the schema response must carry an ETag; it is fetched before every create screen and " +
			"changes only on publish")
	}

	second, _ := fetchSchema(t, r, textbooks, "", map[string]string{"If-None-Match": etag})
	if second.Code != http.StatusNotModified {
		t.Fatalf("re-fetching with If-None-Match answered %d, want 304: %s", second.Code, second.Body.String())
	}
	if second.Body.Len() != 0 {
		t.Fatalf("a 304 must carry no body, got %d bytes", second.Body.Len())
	}
	if second.Header().Get("ETag") != etag {
		t.Fatal("a 304 must repeat the ETag so the cache can keep validating against it")
	}

	stale, _ := fetchSchema(t, r, textbooks, "", map[string]string{"If-None-Match": `W/"as-0-0"`})
	if stale.Code != http.StatusOK {
		t.Fatalf("a stale validator must get the body, got %d", stale.Code)
	}
}

func TestAttributeSchemaRefusesAnUnknownCategory(t *testing.T) {
	r := liveEngine(t)
	w, envelope := doJSON(t, r, http.MethodGet,
		"/v1/commerce/categories/"+uuid.NewString()+"/attribute-schema", nil, nil)

	if w.Code != http.StatusNotFound {
		t.Fatalf("an unknown category answered %d, want 404. An empty form and a mistyped id render "+
			"as the same screen, so the client shows a create page with no fields on it: %s",
			w.Code, w.Body.String())
	}
	var apiErr struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(envelope["error"], &apiErr)
	if apiErr.Code != "CATEGORY_NOT_FOUND" {
		t.Fatalf("error code = %q, want CATEGORY_NOT_FOUND", apiErr.Code)
	}
}

func TestAttributeSchemaRefusesAnUnknownScope(t *testing.T) {
	r := liveEngine(t)
	textbooks := categoryIDBySlug(t, "books-textbooks")
	w, _ := fetchSchema(t, r, textbooks, "?scope=items", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("scope=items answered %d, want 400. Silently returning everything ships a form "+
			"asking for offer fields on a shared item record", w.Code)
	}
}

// The flat category list is byte-identical without the flag. Two shipped
// clients read it and neither asked for a different shape.
func TestCategoryTreeIsBehindAFlagAndTheFlatAnswerIsUnchanged(t *testing.T) {
	r := liveEngine(t)

	flat, _ := doJSON(t, r, http.MethodGet, "/v1/commerce/categories", nil, nil)
	if flat.Code != http.StatusOK {
		t.Fatalf("flat categories: %d", flat.Code)
	}
	if bytes.Contains(flat.Body.Bytes(), []byte(`"children"`)) {
		t.Fatal("the flat response must not grow a `children` key; that changes every byte of a payload " +
			"the storefront and the phone both decode")
	}
	if bytes.Contains(flat.Body.Bytes(), []byte(`"is_listable"`)) {
		t.Fatal("the flat response must not grow `is_listable` either")
	}

	tree, envelope := doJSON(t, r, http.MethodGet, "/v1/commerce/categories?tree=true", nil, nil)
	if tree.Code != http.StatusOK {
		t.Fatalf("tree categories: %d %s", tree.Code, tree.Body.String())
	}
	var roots []struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Slug       string `json:"slug"`
		IsListable bool   `json:"is_listable"`
		Depth      int    `json:"depth"`
		Children   []struct {
			Slug       string `json:"slug"`
			Name       string `json:"name"`
			Depth      int    `json:"depth"`
			IsListable bool   `json:"is_listable"`
		} `json:"children"`
	}
	if err := json.Unmarshal(envelope["data"], &roots); err != nil {
		t.Fatalf("decode tree: %v\n%s", err, tree.Body.String())
	}

	var books *struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Slug       string `json:"slug"`
		IsListable bool   `json:"is_listable"`
		Depth      int    `json:"depth"`
		Children   []struct {
			Slug       string `json:"slug"`
			Name       string `json:"name"`
			Depth      int    `json:"depth"`
			IsListable bool   `json:"is_listable"`
		} `json:"children"`
	}
	for i := range roots {
		if roots[i].Slug == "books-and-stationery" {
			books = &roots[i]
		}
		if roots[i].Slug == "books-textbooks" {
			t.Fatal("Textbooks must be nested under Books, not returned at the top level")
		}
	}
	if books == nil {
		t.Fatal("Books must be a root of the tree")
	}
	if books.IsListable {
		t.Error("Books is a browse heading; 025 marks it non-listable so a seller picks a leaf beneath it")
	}
	found := false
	for _, child := range books.Children {
		if child.Slug == "books-textbooks" {
			found = true
			if child.Depth != 1 {
				t.Errorf("Textbooks depth = %d, want 1", child.Depth)
			}
			if !child.IsListable {
				t.Error("Textbooks is where listings go and must be listable")
			}
		}
	}
	if !found {
		t.Fatal("Textbooks must appear under Books in the tree")
	}
}

// Making a field required is one checkbox and it is not reversible in effect:
// every live listing without a value is instantly non-compliant. The server
// knows the number and refuses until the operator has quoted it back.
func TestMakingAFieldRequiredNeedsTheImpactAcknowledged(t *testing.T) {
	r := liveEngine(t)
	books := categoryIDBySlug(t, "books-and-stationery")

	type binding struct {
		DefinitionID  uuid.UUID `json:"definition_id"`
		IsRequired    bool      `json:"is_required"`
		IsExcluded    bool      `json:"is_excluded"`
		IsVariantAxis *bool     `json:"is_variant_axis"`
		DisplayGroup  *string   `json:"display_group"`
		SortOrder     int       `json:"sort_order"`
	}
	readBindings := func() []binding {
		w, envelope := doJSON(t, r, http.MethodGet,
			fmt.Sprintf("/v1/commerce/internal/categories/%s/attributes", books), nil, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("read bindings: %d %s", w.Code, w.Body.String())
		}
		var payload struct {
			Items []binding `json:"items"`
		}
		if err := json.Unmarshal(envelope["data"], &payload); err != nil {
			t.Fatalf("decode bindings: %v", err)
		}
		return payload.Items
	}

	original := readBindings()
	if len(original) == 0 {
		t.Fatal("migration 025 binds seven definitions to Books")
	}
	t.Cleanup(func() {
		// Put the seeded state back. The restore only RELAXES `pages`, which
		// narrows nothing, so ack_impact=0 is the honest value: no field
		// becomes required that was not already.
		_, _ = doJSON(t, r, http.MethodPut,
			fmt.Sprintf("/v1/commerce/internal/categories/%s/attributes?ack_impact=0", books),
			map[string]any{"items": original}, nil)
	})

	var pagesID uuid.UUID
	if err := edgePool.QueryRow(context.Background(),
		`SELECT id FROM attribute_definitions WHERE code='pages'`).Scan(&pagesID); err != nil {
		t.Fatalf("migration 025 must have seeded `pages`: %v", err)
	}

	// What does the server say it would cost?
	w, envelope := doJSON(t, r, http.MethodGet,
		fmt.Sprintf("/v1/commerce/internal/attribute-definitions/%s/impact", pagesID), nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("impact: %d %s", w.Code, w.Body.String())
	}
	var impact struct {
		LiveProducts int `json:"live_products"`
		Missing      int `json:"missing"`
		OutOfRange   int `json:"out_of_range"`
		Affected     int `json:"affected"`
	}
	if err := json.Unmarshal(envelope["data"], &impact); err != nil {
		t.Fatalf("decode impact: %v", err)
	}
	if impact.Affected != impact.Missing+impact.OutOfRange {
		t.Fatalf("affected (%d) must be missing (%d) + out_of_range (%d)",
			impact.Affected, impact.Missing, impact.OutOfRange)
	}

	next := make([]binding, len(original))
	copy(next, original)
	for i := range next {
		if next[i].DefinitionID == pagesID {
			next[i].IsRequired = true
		}
	}

	// Without the acknowledgement: refused, and the refusal states the number.
	blocked, blockedEnvelope := doJSON(t, r, http.MethodPut,
		fmt.Sprintf("/v1/commerce/internal/categories/%s/attributes", books),
		map[string]any{"items": next}, nil)
	if blocked.Code != http.StatusConflict {
		t.Fatalf("making `pages` required with no ack_impact answered %d, want 409: %s",
			blocked.Code, blocked.Body.String())
	}
	var apiErr struct {
		Code    string `json:"code"`
		Details struct {
			AckImpact int `json:"ack_impact"`
		} `json:"details"`
	}
	if err := json.Unmarshal(blockedEnvelope["error"], &apiErr); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if apiErr.Code != "IMPACT_ACK_REQUIRED" {
		t.Fatalf("error code = %q, want IMPACT_ACK_REQUIRED", apiErr.Code)
	}
	if apiErr.Details.AckImpact != impact.Affected {
		t.Fatalf("the refusal must state the number to send: got %d, want %d",
			apiErr.Details.AckImpact, impact.Affected)
	}

	// A wrong number is refused too — a stale count means the operator was
	// looking at a different catalogue than the one they are changing.
	wrong, _ := doJSON(t, r, http.MethodPut,
		fmt.Sprintf("/v1/commerce/internal/categories/%s/attributes?ack_impact=%d", books, impact.Affected+7),
		map[string]any{"items": next}, nil)
	if wrong.Code != http.StatusConflict {
		t.Fatalf("a mismatched ack_impact answered %d, want 409", wrong.Code)
	}

	// With the right number: applied.
	ok, _ := doJSON(t, r, http.MethodPut,
		fmt.Sprintf("/v1/commerce/internal/categories/%s/attributes?ack_impact=%d", books, impact.Affected),
		map[string]any{"items": next}, nil)
	if ok.Code != http.StatusOK {
		t.Fatalf("the acknowledged edit answered %d, want 200: %s", ok.Code, ok.Body.String())
	}

	textbooks := categoryIDBySlug(t, "books-textbooks")
	_, doc := fetchSchema(t, r, textbooks, "", nil)
	for _, g := range doc.Groups {
		for _, f := range g.Attributes {
			if f.Code == "pages" && !f.Required {
				t.Fatal("the acknowledged edit must reach the inherited form")
			}
		}
	}
}

func TestPublishBumpsTheSchemaVersionOnTheNextResponse(t *testing.T) {
	lockSchemaState(t)
	r := liveEngine(t)
	textbooks := categoryIDBySlug(t, "books-textbooks")

	_, before := fetchSchema(t, r, textbooks, "", nil)
	beforeETag, _ := fetchSchema(t, r, textbooks, "", nil)

	w, _ := doJSON(t, r, http.MethodPost, "/v1/commerce/internal/attribute-schema/publish", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("publish: %d %s", w.Code, w.Body.String())
	}

	after, doc := fetchSchema(t, r, textbooks, "", nil)
	if doc.SchemaVersion != before.SchemaVersion+1 {
		t.Fatalf("schema_version went %d → %d, want +1 after a publish",
			before.SchemaVersion, doc.SchemaVersion)
	}
	if after.Header().Get("ETag") == beforeETag.Header().Get("ETag") {
		t.Fatal("a publish must change the ETag; forcing every cached form to be refetched is the " +
			"whole point of the operation")
	}
}
