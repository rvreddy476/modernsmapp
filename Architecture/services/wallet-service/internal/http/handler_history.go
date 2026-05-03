package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/atpost/wallet-service/internal/service"
	"github.com/gin-gonic/gin"
)

// ListTransactions handles GET /v1/wallet/transactions.
func (h *Handler) ListTransactions(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 && v <= 100 {
			limit = v
		}
	}
	page, err := h.svc.ListHistory(c.Request.Context(), userID, service.HistoryFilter{
		Type:      c.Query("type"),
		Direction: c.Query("direction"),
		Cursor:    c.Query("cursor"),
		Limit:     limit,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "HISTORY_ERROR")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, page)
}

// GetTransactionDetail handles GET /v1/wallet/transactions/:id.
func (h *Handler) GetTransactionDetail(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	txID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	tx, err := h.svc.GetTransactionDetail(c.Request.Context(), userID, txID)
	if err != nil {
		respondServiceError(c, err, http.StatusNotFound, "NOT_FOUND")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, tx)
}

// ListRecipients handles GET /v1/wallet/recipients.
func (h *Handler) ListRecipients(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	rs, err := h.svc.ListFrequentRecipients(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "RECIPIENTS_ERROR")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, rs)
}
