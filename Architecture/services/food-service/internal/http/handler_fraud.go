package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// AdminTopFraudUsers — GET /v1/food/admin/fraud/top?window_hours=168&limit=50
func (h *Handler) AdminTopFraudUsers(c *gin.Context) {
	windowHours := 168
	limit := 50
	if v, err := strconv.Atoi(c.Query("window_hours")); err == nil && v > 0 {
		windowHours = v
	}
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 {
		limit = v
	}
	rows, err := h.svc.TopFraudUsers(c.Request.Context(), windowHours, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FRAUD_TOP_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"rows": rows}, nil)
}
