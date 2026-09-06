package http

// The read-back surface search-service pulls from.
//
// Three routes, all under /v1/commerce/internal and therefore behind
// X-Internal-Service-Key like every other route in that group:
//
//	GET /internal/products/:productId/search-doc   one document
//	GET /internal/products/search-docs             the reindex walk
//	GET /internal/search-facets                    the filterable definitions
//
// Internal and not public, for two reasons. The document carries a
// listing's state (`status`, `approval_status`, `visible`) including for
// products no buyer may see, which is a seller's private business. And it
// is shaped for an indexer, not for a client: a public surface would
// acquire clients, and then the shape could not change when the index
// needs a new field.
//
// NOTHING HERE CHANGES AN EXISTING RESPONSE. These are new routes; the
// buyer-facing product endpoints return exactly what they returned before.

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// AdminProductSearchDoc GET /v1/commerce/internal/products/:productId/search-doc
//
// The whole point of the read-back: whatever woke the consumer, this is
// what the product says NOW. The consumer indexes when `visible` is true
// and deletes when it is false — it does not act on the event's name.
//
// A product that no longer exists is a 404, and the consumer treats that
// as "delete the document" for the same reason.
func (h *Handler) AdminProductSearchDoc(c *gin.Context) {
	productID, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	doc, err := h.svc.ProductSearchDoc(c.Request.Context(), productID)
	if err != nil {
		if errors.Is(err, postgres.ErrProductNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound,
				"NOT_FOUND", "product not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError,
			"INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, doc, nil)
}

// AdminListProductSearchDocs GET /v1/commerce/internal/products/search-docs
//
//	?cursor=   opaque, from the previous page's next_cursor
//	?limit=    1..500, default 200
//	?include_hidden=true  drafts and rejections too (operator diagnosis)
//
// `visible_total` rides on every page so a reindex can report "indexed N
// of M" rather than only "indexed N" — the difference between a run that
// finished and a run that stopped.
func (h *Handler) AdminListProductSearchDocs(c *gin.Context) {
	limit := 200
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > 500 {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
				"INVALID_LIMIT", "limit must be between 1 and 500", nil)
			return
		}
		limit = n
	}
	visibleOnly := c.Query("include_hidden") != "true"

	page, err := h.svc.ListProductSearchDocs(c.Request.Context(), visibleOnly, c.Query("cursor"), limit)
	if err != nil {
		if errors.Is(err, service.ErrInvalidSearchDocCursor) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
				"INVALID_CURSOR", "cursor was not issued by this endpoint", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError,
			"INTERNAL_ERROR", err.Error(), nil)
		return
	}
	total, err := h.svc.CountVisibleProducts(c.Request.Context())
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError,
			"INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"items":         page.Items,
		"next_cursor":   page.NextCursor,
		"visible_total": total,
	}, nil)
}

// AdminSearchFacets GET /v1/commerce/internal/search-facets
//
// The attribute definitions a facet may be built from — those with
// `is_filterable` set — each with its active options, codes and labels
// both.
//
// This is what keeps "add a facet" a no-deploy change. search-service
// builds its aggregations from this response at query time, so an operator
// ticking is_filterable on a definition in the admin console makes it a
// facet on the next query, and re-wording its label changes what buyers
// read without invalidating a single stored value or saved filter.
func (h *Handler) AdminSearchFacets(c *gin.Context) {
	defs, err := h.svc.FacetDefinitions(c.Request.Context())
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError,
			"INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": defs}, nil)
}
