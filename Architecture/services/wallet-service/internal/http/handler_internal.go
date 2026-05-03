package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// internalDebitRequest is the body for POST /v1/wallet/internal/debit. Used
// by Pulse Premium / Commerce / Food / Bill-pay to debit a user's wallet.
type internalDebitRequest struct {
	UserID          string `json:"user_id"`
	AmountPaise     int64  `json:"amount_paise"`
	MerchantService string `json:"merchant_service"`
	MerchantRef     string `json:"merchant_ref"`
	IdempotencyKey  string `json:"idempotency_key"`
}

// InternalDebit handles POST /v1/wallet/internal/debit. Internal-key gated.
func (h *Handler) InternalDebit(c *gin.Context) {
	var req internalDebitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	uid, err := uuid.Parse(req.UserID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid user_id", nil)
		return
	}
	out, svcErr := h.svc.MerchantDebit(c.Request.Context(), uid, req.AmountPaise, req.MerchantService, req.MerchantRef, req.IdempotencyKey)
	if svcErr != nil {
		respondServiceError(c, svcErr, http.StatusInternalServerError, "DEBIT_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

type internalRefundRequest struct {
	OriginalTransactionID string `json:"original_transaction_id"`
	AmountPaise           int64  `json:"amount_paise"`
	Reason                string `json:"reason"`
}

// InternalRefund handles POST /v1/wallet/internal/refund.
func (h *Handler) InternalRefund(c *gin.Context) {
	var req internalRefundRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	originalID, err := uuid.Parse(req.OriginalTransactionID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid original_transaction_id", nil)
		return
	}
	out, svcErr := h.svc.Refund(c.Request.Context(), originalID, req.AmountPaise, req.Reason)
	if svcErr != nil {
		respondServiceError(c, svcErr, http.StatusInternalServerError, "REFUND_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// InternalBalance handles GET /v1/wallet/internal/balance/:user_id.
func (h *Handler) InternalBalance(c *gin.Context) {
	uid, ok := parseUUIDParam(c, "user_id")
	if !ok {
		return
	}
	b, err := h.svc.GetBalance(c.Request.Context(), uid)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "BALANCE_ERROR")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, b)
}
