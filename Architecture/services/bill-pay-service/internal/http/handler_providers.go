package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// ListProviders handles GET /v1/billpay/providers?category=&state=&limit=
func (h *Handler) ListProviders(c *gin.Context) {
	category := c.Query("category")
	state := c.Query("state")
	limit := 100
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	provs, err := h.svc.ListProviders(c.Request.Context(), category, state, limit)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PROVIDERS_ERROR")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, provs)
}

// GetProvider handles GET /v1/billpay/providers/:id.
func (h *Handler) GetProvider(c *gin.Context) {
	id, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	prov, err := h.svc.GetProvider(c.Request.Context(), id)
	if err != nil {
		respondServiceError(c, err, http.StatusNotFound, "PROVIDER_NOT_FOUND")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, prov)
}
