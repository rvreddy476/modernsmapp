package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// GetCategories handles GET /v1/billpay/categories.
func (h *Handler) GetCategories(c *gin.Context) {
	cats, err := h.svc.ListCategories(c.Request.Context())
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "CATEGORIES_ERROR")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, cats)
}
