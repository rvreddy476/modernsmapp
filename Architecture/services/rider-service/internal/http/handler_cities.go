package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// GetCities — GET /v1/rider/cities. Public; no auth.
func (h *Handler) GetCities(c *gin.Context) {
	cities, err := h.svc.ListCities(c.Request.Context())
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "CITIES_ERROR")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, cities)
}
