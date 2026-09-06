package search

// Attribute facets, built from the attribute definitions at query time.
//
// ─── WHY THE AGGREGATION IS BUILT PER REQUEST ───────────────────────────
//
// The list of codes this aggregation buckets on is not in this file. It
// comes from commerce-service, from the attribute definitions an operator
// has marked `is_filterable` in the admin console — see
// commerceclient.FacetDefinitions.
//
// The alternative is a hardcoded list of facetable codes here, and the
// difference between the two is what "add a facet" costs. With a hardcoded
// list it costs a code change, a review, a build and a deploy of a search
// engine, for a decision that is entirely about merchandising. Built from
// the definitions, an operator ticks a checkbox and the next query has the
// facet. That is the same reason the definitions carry the checkbox at all.
//
// ─── CODES BUCKET, LABELS RENDER ────────────────────────────────────────
//
// Everything in THIS file is codes. The bucket keys are definition codes
// and enum-value codes, because that is what the documents hold and what a
// client echoes back as a filter. Labels are attached one layer up, in the
// HTTP handler, from the same definitions the aggregation was built from.
//
// Keeping them apart is what makes renaming a field a no-deploy change: a
// label is presentation and may be re-worded at any time, and if a stored
// filter or an index document keyed on the label, re-wording it would
// silently break every saved search that used it.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

// FacetBucket is one value of one facet: the CODE and how many matching
// products carry it.
type FacetBucket struct {
	Code  string `json:"code"`
	Count int64  `json:"count"`
}

// FacetQuery is the search the facets are counted over. It must be the
// same search the results come from — a facet rail counted over a
// different query tells the buyer how many results a filter would give
// them and then gives them a different number.
type FacetQuery struct {
	Query    string
	Category string
	// Codes are the definition codes to bucket, from the definitions.
	Codes []string
	// MaxValues caps the buckets per facet. A colour facet with 4000
	// buckets is not a filter rail, it is a denial of service on the
	// client's renderer.
	MaxValues int
}

// FacetResponse is the raw counts, before labels are attached.
type FacetResponse struct {
	// Total is how many products matched the query the facets were counted
	// over, so a client can show "412 results" beside the rail.
	Total   int64
	Buckets map[string][]FacetBucket
}

// ProductFacets counts, per requested definition code, how many matching
// products carry each value.
//
// The nested aggregation is the point. `attributes` is a nested field
// (see productsv2.go), so the sub-aggregation buckets on `attributes.code`
// and then on `attributes.value` WITHIN the same nested document — which
// is what makes "binding=hardcover: 12" mean twelve hardcovers rather than
// twelve products that have a binding and, somewhere unrelated, the word
// hardcover.
func (s *Store) ProductFacets(ctx context.Context, fq FacetQuery) (*FacetResponse, error) {
	if fq.MaxValues <= 0 || fq.MaxValues > 200 {
		fq.MaxValues = 50
	}

	must := []interface{}{}
	if fq.Query != "" {
		must = append(must, map[string]interface{}{
			"multi_match": map[string]interface{}{
				"query":  fq.Query,
				"fields": []string{"title^3", "brand_name^2", "description", "category_names", "seller_name", "search_keywords"},
			},
		})
	} else {
		must = append(must, map[string]interface{}{"match_all": map[string]interface{}{}})
	}
	if fq.Category != "" {
		// The SAME clause the result query uses. Exported from
		// opensearch.go for exactly this reason: a facet rail counted with
		// a different category rule than the results is a rail whose
		// numbers do not add up to the page.
		must = append(must, CategoryFilterClause(fq.Category))
	}

	q := map[string]interface{}{
		// size:0 — a facet request wants counts, not documents. Asking for
		// documents as well doubles the response for a rail that renders
		// none of them.
		"size": 0,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{"must": must},
		},
		"track_total_hits": true,
	}

	// No codes is not an error and not an empty aggregation: it means no
	// definition is marked filterable, and the honest answer is a rail with
	// no facets and a correct total.
	if len(fq.Codes) > 0 {
		q["aggs"] = map[string]interface{}{
			"attributes": map[string]interface{}{
				"nested": map[string]interface{}{"path": "attributes"},
				"aggs": map[string]interface{}{
					"filterable": map[string]interface{}{
						// The definitions decide what is bucketed. This
						// `terms` filter IS the "built from the definitions
						// at query time" part — swap the definitions and the
						// aggregation changes with no deploy.
						"filter": map[string]interface{}{
							"terms": map[string]interface{}{"attributes.code": fq.Codes},
						},
						"aggs": map[string]interface{}{
							"by_code": map[string]interface{}{
								"terms": map[string]interface{}{
									"field": "attributes.code",
									"size":  len(fq.Codes),
								},
								"aggs": map[string]interface{}{
									"values": map[string]interface{}{
										"terms": map[string]interface{}{
											"field": "attributes.value",
											"size":  fq.MaxValues,
											// Ties broken by key so two runs
											// over an unchanged index return
											// the same rail in the same order.
											"order": []interface{}{
												map[string]interface{}{"_count": "desc"},
												map[string]interface{}{"_key": "asc"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}
	}

	// Through encodeQuery, so the viewer's block scope is applied to the
	// counts as well as to the results. A facet counted without it would
	// tell a buyer there are 12 matches and then show them 9.
	buf, err := encodeQuery(ctx, q, ownerFieldForIndex(IndexProducts))
	if err != nil {
		return nil, err
	}
	req := opensearchapi.SearchRequest{
		Index: []string{IndexProducts},
		Body:  buf,
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil, fmt.Errorf("facet search error [%s]: %s", IndexProducts, res.String())
	}

	var parsed struct {
		Hits struct {
			Total struct {
				Value int64 `json:"value"`
			} `json:"total"`
		} `json:"hits"`
		Aggregations struct {
			Attributes struct {
				Filterable struct {
					ByCode struct {
						Buckets []struct {
							Key    string `json:"key"`
							Values struct {
								Buckets []struct {
									Key      string `json:"key"`
									DocCount int64  `json:"doc_count"`
								} `json:"buckets"`
							} `json:"values"`
						} `json:"buckets"`
					} `json:"by_code"`
				} `json:"filterable"`
			} `json:"attributes"`
		} `json:"aggregations"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	out := &FacetResponse{
		Total:   parsed.Hits.Total.Value,
		Buckets: map[string][]FacetBucket{},
	}
	for _, code := range parsed.Aggregations.Attributes.Filterable.ByCode.Buckets {
		vals := make([]FacetBucket, 0, len(code.Values.Buckets))
		for _, v := range code.Values.Buckets {
			vals = append(vals, FacetBucket{Code: v.Key, Count: v.DocCount})
		}
		out.Buckets[code.Key] = vals
	}
	return out, nil
}

// RefreshProducts forces the products alias's index to make recent writes
// searchable.
//
// Only the reindex and the tests call it. A refresh per document would
// destroy indexing throughput, which is exactly why OpenSearch does not do
// it by default — but a reindex that returns before its documents are
// searchable is a reindex whose reported count cannot be checked.
func (s *Store) RefreshProducts(ctx context.Context) error {
	req := opensearchapi.IndicesRefreshRequest{Index: []string{IndexProducts}}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("refresh %s: %s", IndexProducts, res.String())
	}
	return nil
}
