package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// Admin read-side handlers for /admin/dating console (PRODUCTION_GAP_
// ANALYSIS.md §P0-8). All routes sit inside the v1 group so the
// shared internal-service-key gate already applies — the gateway
// also enforces an admin-scope check before forwarding to these
// paths, so reaching this handler implies the caller is admin.

// ListReports — GET /v1/dating/admin/reports?status=&category=&limit=&offset=
func (h *Handler) ListReports(c *gin.Context) {
	status := c.Query("status")
	category := c.Query("category")
	limit := parseIntQuery(c, "limit", 50, 200)
	offset := parseIntQuery(c, "offset", 0, 100000)
	items, err := h.svc.ListReports(c.Request.Context(), status, category, limit, offset)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items, "limit": limit, "offset": offset}, nil)
}

// ListPanicEvents — GET /v1/dating/admin/safety/panic?limit=
func (h *Handler) ListPanicEvents(c *gin.Context) {
	limit := parseIntQuery(c, "limit", 100, 200)
	items, err := h.svc.ListPanicEvents(c.Request.Context(), limit)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items, "limit": limit}, nil)
}

// ListPendingPhotos — GET /v1/dating/admin/photos/pending?limit=
func (h *Handler) ListPendingPhotos(c *gin.Context) {
	limit := parseIntQuery(c, "limit", 50, 200)
	items, err := h.svc.ListPendingPhotos(c.Request.Context(), limit)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items, "limit": limit}, nil)
}

// parseIntQuery reads a positive int query param, clamped to [1, max]
// with a default fallback. Used by all three admin list endpoints.
func parseIntQuery(c *gin.Context, name string, def, max int) int {
	raw := c.Query(name)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}
