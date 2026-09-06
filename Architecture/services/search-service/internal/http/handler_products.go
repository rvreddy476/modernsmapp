package http

// Product facets, the product reindex, and the alias switch.
//
// ─── THE FACET RESPONSE CARRIES BOTH CODES AND LABELS ───────────────────
//
// Every facet and every value in the response below has a `code` and a
// `label`, and they are two different things on purpose.
//
// The code is what the index holds, what a filter is expressed in, and what
// a saved search stores. It never changes — commerce refuses to rename an
// attribute definition's code precisely because it is the join key to every
// value already stored against it.
//
// The label is what a buyer reads, and an operator may re-word it at any
// time in the admin console. If a client had to key on the label, that
// re-wording would break every stored filter and every bookmarked URL —
// so a rename would stop being a no-deploy change and become a migration.
//
// Carrying both is what keeps the two independent: the client renders the
// label and sends back the code.
//
// The definitions come from commerce at query time (cached ~60s in
// commerceclient), so ticking `is_filterable` on a definition adds a facet
// with no deploy of this service.

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/atpost/search-service/internal/commerceclient"
	"github.com/atpost/search-service/internal/reindex"
	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// WithCommerceClient wires the catalogue client used by the facet endpoint
// and the product reindex.
func (h *Handler) WithCommerceClient(c *commerceclient.Client) *Handler {
	h.commerceClient = c
	return h
}

// facetValueResponse is one bucket: the code a client filters by, the
// label it renders, and how many products carry it.
type facetValueResponse struct {
	Code  string `json:"code"`
	Label string `json:"label"`
	Count int64  `json:"count"`
}

// facetResponse is one facet.
type facetResponse struct {
	Code     string               `json:"code"`
	Label    string               `json:"label"`
	DataType string               `json:"data_type"`
	Unit     string               `json:"unit,omitempty"`
	Group    string               `json:"display_group,omitempty"`
	Values   []facetValueResponse `json:"values"`
}

// ProductFacets handles GET /v1/search/products/facets
//
//	?q=          the same query the results are for (optional — a browse)
//	?category=   category id, slug or name; matches descendants
//	?max_values= buckets per facet (default 50, max 200)
//
// Counted over the SAME query as /v1/search/products so the numbers on the
// rail agree with the page. A facet count that disagrees with the result
// count is worse than no facet at all: it tells a buyer a filter has 12
// matches and then shows them nine.
func (h *Handler) ProductFacets(c *gin.Context) {
	if h.commerceClient == nil || !h.commerceClient.Configured() {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable,
			"FACETS_UNCONFIGURED", "COMMERCE_SERVICE_URL is not configured on this deployment", nil)
		return
	}
	ctx := c.Request.Context()

	// The definitions ARE the aggregation. Fetched per request (cached
	// briefly) rather than compiled in, which is what makes adding a facet
	// an operator action instead of a release.
	defs, err := h.commerceClient.FacetDefinitions(ctx)
	if err != nil {
		slog.Error("ProductFacets: definitions unavailable", "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadGateway,
			"FACETS_UNAVAILABLE", "attribute definitions could not be read from commerce-service", nil)
		return
	}

	maxValues := 50
	if v := c.Query("max_values"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			maxValues = n
		}
	}

	codes := make([]string, 0, len(defs))
	for _, d := range defs {
		codes = append(codes, d.Code)
	}

	agg, err := h.store.ProductFacets(ctx, search.FacetQuery{
		Query:     c.Query("q"),
		Category:  c.Query("category"),
		Codes:     codes,
		MaxValues: maxValues,
	})
	if err != nil {
		slog.Error("ProductFacets error", "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError,
			"INTERNAL_ERROR", "Facet aggregation failed", nil)
		return
	}

	facets := make([]facetResponse, 0, len(defs))
	for _, d := range defs {
		buckets := agg.Buckets[d.Code]
		if len(buckets) == 0 {
			// A facet nothing in this result set answers is dropped rather
			// than rendered empty. An empty filter group is a control a
			// buyer can click that cannot change anything.
			continue
		}
		// Enum options carry authored labels; a free-text or numeric value
		// is its own label. Looked up from the SAME definitions the
		// aggregation was built from, so a bucket can never be labelled
		// from a stale definition set.
		labels := map[string]string{}
		for _, o := range d.Options {
			labels[o.Code] = o.Label
		}
		values := make([]facetValueResponse, 0, len(buckets))
		for _, b := range buckets {
			label := labels[b.Code]
			if label == "" {
				label = b.Code
			}
			values = append(values, facetValueResponse{Code: b.Code, Label: label, Count: b.Count})
		}
		facets = append(facets, facetResponse{
			Code:     d.Code,
			Label:    d.Label,
			DataType: d.DataType,
			Unit:     d.UnitFamily,
			Group:    d.Group,
			Values:   values,
		})
	}
	// Stable order: definition group, then code. The aggregation returns
	// facets in bucket order, which changes as the catalogue changes — a
	// filter rail whose groups reorder between two searches is unusable.
	sort.SliceStable(facets, func(i, j int) bool {
		if facets[i].Group != facets[j].Group {
			return facets[i].Group < facets[j].Group
		}
		return facets[i].Code < facets[j].Code
	})

	api.JSON(c.Writer, http.StatusOK, map[string]any{
		"total":  agg.Total,
		"facets": facets,
	}, nil)
}

// ReindexProducts handles POST /v1/search/internal/reindex/products
//
// Synchronous, unlike the users reindex, and deliberately: an operator
// running this wants the counts. A 202 with "check the logs" is fine for a
// job nobody verifies; it is not fine for the one call that answers "is
// the product index complete?".
func (h *Handler) ReindexProducts(c *gin.Context) {
	if h.commerceClient == nil || !h.commerceClient.Configured() {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable,
			"REINDEX_UNCONFIGURED", "COMMERCE_SERVICE_URL is not configured on this deployment", nil)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 15*time.Minute)
	defer cancel()

	res, err := reindex.ReindexProducts(ctx, h.commerceClient, h.store, slog.Default())
	if err != nil {
		slog.Error("admin reindex/products failed", "err", err,
			"fetched", res.Fetched, "indexed", res.Indexed)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadGateway,
			"REINDEX_FAILED", err.Error(), res)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}

// ProductsAlias handles GET /v1/search/internal/products/alias — which
// physical index the `products` alias currently resolves to, and how many
// documents each candidate holds.
//
// Both counts, because the number that matters during a rollback is the
// one for the index you are about to move BACK to. An operator deciding
// whether to revert needs to know products_v1 still has documents in it
// before they find out the hard way.
func (h *Handler) ProductsAlias(c *gin.Context) {
	ctx := c.Request.Context()
	target, err := h.store.ProductsAliasTarget(ctx)
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError,
			"ALIAS_UNREADABLE", err.Error(), nil)
		return
	}
	v1, _ := h.store.CountIndexDocs(ctx, search.IndexProductsV1)
	v2, _ := h.store.CountIndexDocs(ctx, search.IndexProductsV2)
	api.JSON(c.Writer, http.StatusOK, map[string]any{
		"alias":  search.IndexProducts,
		"target": target,
		"counts": map[string]int64{
			search.IndexProductsV1: v1,
			search.IndexProductsV2: v2,
		},
	}, nil)
}

type moveAliasRequest struct {
	Index string `json:"index"`
}

// MoveProductsAlias handles POST /v1/search/internal/products/alias
//
//	{"index": "products_v1"}   roll back
//	{"index": "products_v2"}   roll forward
//
// This is the rollback. One atomic call, no deploy, no restart, and
// products_v1 is still sitting there with its documents because nothing in
// this service deletes it.
//
// The target is checked against the two known index names rather than
// accepted as free text: an alias pointed at a typo resolves to nothing,
// and every product search then returns an index-not-found error — which
// is a worse outage than whatever the operator was rolling back from.
func (h *Handler) MoveProductsAlias(c *gin.Context) {
	var req moveAliasRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"INVALID_BODY", err.Error(), nil)
		return
	}
	if req.Index != search.IndexProductsV1 && req.Index != search.IndexProductsV2 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"UNKNOWN_INDEX",
			"index must be "+search.IndexProductsV1+" or "+search.IndexProductsV2, nil)
		return
	}
	if err := h.store.MoveProductsAlias(c.Request.Context(), req.Index); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError,
			"ALIAS_MOVE_FAILED", err.Error(), nil)
		return
	}
	target, _ := h.store.ProductsAliasTarget(c.Request.Context())
	slog.Warn("search: products alias moved", "alias", search.IndexProducts, "target", target)
	api.JSON(c.Writer, http.StatusOK, map[string]any{
		"alias":  search.IndexProducts,
		"target": target,
	}, nil)
}
