package http

// The facet rail.
//
// Two claims are being tested, and they are the two the step is about.
//
//  1. The aggregation is BUILT FROM THE DEFINITIONS. The codes it buckets
//     on come from commerce at query time, so ticking `is_filterable` in
//     the admin console adds a facet with no deploy of this service. The
//     test proves it by changing what commerce says and watching the
//     OpenSearch request body change.
//
//  2. The response carries CODES AND LABELS. The code is the stable key a
//     client filters and stores by; the label is presentation and may be
//     re-worded at any moment. A response with only one of them makes
//     renaming a field either impossible or breaking.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/search-service/internal/commerceclient"
	"github.com/atpost/search-service/internal/store/search"
	"github.com/gin-gonic/gin"
)

// ─── A fake OpenSearch that captures the query body it was sent ─────────

type fakeAggOS struct {
	mu       sync.Mutex
	lastBody map[string]any
	response string
	srv      *httptest.Server
}

func newFakeAggOS(t *testing.T) (*search.Store, *fakeAggOS) {
	t.Helper()
	f := &fakeAggOS{response: `{"hits":{"total":{"value":0}},"aggregations":{}}`}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/_search") {
			body, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.lastBody = map[string]any{}
			_ = json.Unmarshal(body, &f.lastBody)
			resp := f.response
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, resp)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"acknowledged":true}`)
	}))
	t.Cleanup(f.srv.Close)

	store, err := search.New(f.srv.URL)
	if err != nil {
		t.Fatalf("search.New: %v", err)
	}
	return store, f
}

func (f *fakeAggOS) body() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastBody
}

func (f *fakeAggOS) respondWith(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.response = s
}

// ─── A fake commerce serving attribute definitions ──────────────────────

func newFakeDefinitions(t *testing.T, defs []commerceclient.FacetDefinition) *commerceclient.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"items": defs},
		})
	}))
	t.Cleanup(srv.Close)
	return commerceclient.New(srv.URL, "test-key")
}

func bindingDefinition() commerceclient.FacetDefinition {
	return commerceclient.FacetDefinition{
		Code:     "binding",
		Label:    "Binding",
		DataType: "enum",
		Group:    "Specifications",
		Options: []commerceclient.FacetOption{
			{Code: "hardcover", Label: "Hardcover", SortOrder: 1},
			{Code: "paperback", Label: "Paperback", SortOrder: 2},
		},
	}
}

func facetRequest(t *testing.T, h *Handler, target string) (int, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/search/products/facets", h.ProductFacets)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	r.ServeHTTP(w, req)

	var parsed map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, w.Body.String())
	}
	return w.Code, parsed
}

// ─── The tests ──────────────────────────────────────────────────────────

// Claim 1: the definitions decide what is aggregated.
func TestTheAggregationIsBuiltFromTheDefinitions(t *testing.T) {
	store, os := newFakeAggOS(t)

	h := (&Handler{store: store}).WithCommerceClient(
		newFakeDefinitions(t, []commerceclient.FacetDefinition{bindingDefinition()}))
	if code, _ := facetRequest(t, h, "/v1/search/products/facets?q=narayan"); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if got := aggregatedCodes(t, os.body()); len(got) != 1 || got[0] != "binding" {
		t.Fatalf("aggregated codes = %v, want [binding]", got)
	}

	// An operator ticks is_filterable on a second definition. No deploy,
	// no restart — the very next query aggregates it.
	h2 := (&Handler{store: store}).WithCommerceClient(
		newFakeDefinitions(t, []commerceclient.FacetDefinition{
			bindingDefinition(),
			{Code: "author", Label: "Author", DataType: "text", Group: "Specifications"},
		}))
	if code, _ := facetRequest(t, h2, "/v1/search/products/facets?q=narayan"); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	got := aggregatedCodes(t, os.body())
	if len(got) != 2 {
		t.Fatalf("aggregated codes = %v, want both definitions.\n"+
			"If this list is compiled in rather than fetched, adding a facet costs a release.", got)
	}
}

// aggregatedCodes digs the definition codes back out of the query body the
// handler sent, which is the only honest way to assert "built from the
// definitions".
func aggregatedCodes(t *testing.T, body map[string]any) []string {
	t.Helper()
	if body == nil {
		t.Fatal("no query was sent to OpenSearch")
	}
	aggs, ok := body["aggs"].(map[string]any)
	if !ok {
		t.Fatalf("query carried no aggregations: %v", body)
	}
	attributes, _ := aggs["attributes"].(map[string]any)
	if _, isNested := attributes["nested"]; !isNested {
		t.Fatalf("the attributes aggregation is not `nested`: %v.\n"+
			"Without nesting, code and value are independent lists and a facet count means "+
			"nothing.", attributes)
	}
	inner, _ := attributes["aggs"].(map[string]any)
	filterable, _ := inner["filterable"].(map[string]any)
	filter, _ := filterable["filter"].(map[string]any)
	terms, _ := filter["terms"].(map[string]any)
	raw, _ := terms["attributes.code"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		out = append(out, fmt.Sprint(v))
	}
	return out
}

// Claim 2: codes AND labels in the response.
func TestAFacetResponseCarriesCodesAndLabels(t *testing.T) {
	store, os := newFakeAggOS(t)
	os.respondWith(`{
		"hits": {"total": {"value": 12}},
		"aggregations": {
			"attributes": {
				"filterable": {
					"by_code": {
						"buckets": [
							{"key": "binding", "values": {"buckets": [
								{"key": "paperback", "doc_count": 9},
								{"key": "hardcover", "doc_count": 3}
							]}}
						]
					}
				}
			}
		}
	}`)

	h := (&Handler{store: store}).WithCommerceClient(
		newFakeDefinitions(t, []commerceclient.FacetDefinition{bindingDefinition()}))
	code, body := facetRequest(t, h, "/v1/search/products/facets?q=narayan")
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, body)
	}
	data, _ := body["data"].(map[string]any)
	if data["total"] != float64(12) {
		t.Fatalf("total = %v, want 12 — the rail has to agree with the result page", data["total"])
	}
	facets, _ := data["facets"].([]any)
	if len(facets) != 1 {
		t.Fatalf("facets = %v, want one", facets)
	}
	facet, _ := facets[0].(map[string]any)
	if facet["code"] != "binding" {
		t.Fatalf("facet code = %v, want the DEFINITION CODE — a client filters by it and stores "+
			"it in a saved search", facet["code"])
	}
	if facet["label"] != "Binding" {
		t.Fatalf("facet label = %v, want the authored label", facet["label"])
	}
	values, _ := facet["values"].([]any)
	if len(values) != 2 {
		t.Fatalf("values = %v, want two buckets", values)
	}
	first, _ := values[0].(map[string]any)
	if first["code"] != "paperback" || first["label"] != "Paperback" || first["count"] != float64(9) {
		t.Fatalf("first value = %v, want {code: paperback, label: Paperback, count: 9}.\n"+
			"Both halves travel: the code because renaming the label must not break a stored "+
			"filter, the label because search-service has no authority to invent presentation.",
			first)
	}
}

// The rename that must stay a no-deploy change: the same code, a new label,
// and nothing in the index or in a stored filter has to move.
func TestRenamingALabelChangesOnlyTheLabel(t *testing.T) {
	store, os := newFakeAggOS(t)
	os.respondWith(`{
		"hits": {"total": {"value": 1}},
		"aggregations": {"attributes": {"filterable": {"by_code": {"buckets": [
			{"key": "binding", "values": {"buckets": [{"key": "paperback", "doc_count": 1}]}}
		]}}}}
	}`)

	renamed := bindingDefinition()
	renamed.Label = "Cover type"
	renamed.Options[1].Label = "Soft cover"

	h := (&Handler{store: store}).WithCommerceClient(
		newFakeDefinitions(t, []commerceclient.FacetDefinition{renamed}))
	_, body := facetRequest(t, h, "/v1/search/products/facets")

	data, _ := body["data"].(map[string]any)
	facets, _ := data["facets"].([]any)
	facet, _ := facets[0].(map[string]any)
	if facet["code"] != "binding" {
		t.Fatalf("the code moved with the label (%v); a rename would invalidate every stored "+
			"filter and every document already indexed", facet["code"])
	}
	if facet["label"] != "Cover type" {
		t.Fatalf("label = %v, want the new wording", facet["label"])
	}
	values, _ := facet["values"].([]any)
	first, _ := values[0].(map[string]any)
	if first["code"] != "paperback" || first["label"] != "Soft cover" {
		t.Fatalf("value = %v, want the same code under the new label", first)
	}
}

// A facet nothing in the result set answers is dropped. An empty filter
// group is a control a buyer can click that cannot change anything.
func TestFacetsWithNoMatchesAreNotRendered(t *testing.T) {
	store, os := newFakeAggOS(t)
	os.respondWith(`{"hits":{"total":{"value":3}},"aggregations":{"attributes":{"filterable":{"by_code":{"buckets":[]}}}}}`)

	h := (&Handler{store: store}).WithCommerceClient(
		newFakeDefinitions(t, []commerceclient.FacetDefinition{bindingDefinition()}))
	_, body := facetRequest(t, h, "/v1/search/products/facets")

	data, _ := body["data"].(map[string]any)
	facets, _ := data["facets"].([]any)
	if len(facets) != 0 {
		t.Fatalf("facets = %v, want none", facets)
	}
	if data["total"] != float64(3) {
		t.Fatalf("total = %v, want the real total even with no facets", data["total"])
	}
}

// The category filter on the rail must be the SAME clause the results use,
// including the ancestor match — or the counts will not add up to the page.
func TestTheFacetQueryFiltersOnTheWholeCategoryChain(t *testing.T) {
	store, os := newFakeAggOS(t)
	h := (&Handler{store: store}).WithCommerceClient(
		newFakeDefinitions(t, []commerceclient.FacetDefinition{bindingDefinition()}))

	if code, _ := facetRequest(t, h, "/v1/search/products/facets?category=books"); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	raw, _ := json.Marshal(os.body())
	for _, field := range []string{"category_ids", "category_slugs", "category_names"} {
		if !strings.Contains(string(raw), field) {
			t.Fatalf("the facet query does not filter on %s: %s.\n"+
				"A rail counted with a narrower category rule than the results reports numbers "+
				"the page cannot produce.", field, raw)
		}
	}
}

// Without a commerce URL the rail is unavailable and says so, rather than
// answering "this catalogue has no filters" — which is indistinguishable
// from the truth and therefore worse.
func TestAnUnconfiguredFacetRailSaysSo(t *testing.T) {
	store, _ := newFakeAggOS(t)
	h := &Handler{store: store}
	code, _ := facetRequest(t, h, "/v1/search/products/facets")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", code)
	}
}
