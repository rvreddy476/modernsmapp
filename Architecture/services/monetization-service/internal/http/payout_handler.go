package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Payout Statements
// ---------------------------------------------------------------------------

func (h *Handler) ListPayoutStatements(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	limit := 20
	if limitStr := c.Query("limit"); limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
		}
	}
	offset := 0
	if offsetStr := c.Query("offset"); offsetStr != "" {
		if n, err := strconv.Atoi(offsetStr); err == nil && n >= 0 {
			offset = n
		}
	}

	stmts, err := h.svc.ListPayoutStatements(c.Request.Context(), userID, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if stmts == nil {
		stmts = []postgres.PayoutStatement{}
	}

	api.JSON(c.Writer, http.StatusOK, stmts, nil)
}

func (h *Handler) GetPayoutStatement(c *gin.Context) {
	_, ok := getUserID(c)
	if !ok {
		return
	}

	stmtID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid statement ID", nil, nil)
		return
	}

	stmt, err := h.svc.GetPayoutStatement(c.Request.Context(), stmtID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if stmt == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Payout statement not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, stmt, nil)
}

// ---------------------------------------------------------------------------
// Payout Webhook
// ---------------------------------------------------------------------------

type PayoutWebhookRequest struct {
	ProviderReference string `json:"provider_reference" binding:"required"`
	Status            string `json:"status" binding:"required"`
	FailureReason     string `json:"failure_reason"`
}

func (h *Handler) HandlePayoutWebhook(c *gin.Context) {
	// No auth check — webhook signature would be verified by API gateway or middleware.
	var req PayoutWebhookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.HandlePayoutWebhook(c.Request.Context(), req.ProviderReference, req.Status, req.FailureReason); err != nil {
		if err.Error() == "PAYOUT_NOT_FOUND" {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Payout not found for provider reference", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "processed"}, nil)
}
