package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// ListPayments handles GET /v1/billpay/payments?limit=&cursor=&status=
func (h *Handler) ListPayments(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	limit := 25
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	cursor := c.Query("cursor")
	status := c.Query("status")
	out, err := h.svc.ListPayments(c.Request.Context(), uid, status, cursor, limit)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PAYMENTS_ERROR")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// GetPayment handles GET /v1/billpay/payments/:id.
func (h *Handler) GetPayment(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	pmt, err := h.svc.GetPayment(c.Request.Context(), uid, id)
	if err != nil {
		respondServiceError(c, err, http.StatusNotFound, "PAYMENT_NOT_FOUND")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, pmt)
}
