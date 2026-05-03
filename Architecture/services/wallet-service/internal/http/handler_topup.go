package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

type topUpRequest struct {
	AmountPaise    int64  `json:"amount_paise"`
	IdempotencyKey string `json:"idempotency_key"`
}

// PostTopUp handles POST /v1/wallet/top-up. Returns the UPI Intent URL the
// client opens to launch the user's UPI app.
func (h *Handler) PostTopUp(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req topUpRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	// The Idempotency-Key request header is the canonical fallback used by
	// the rest of the platform; honour it too.
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = c.GetHeader("Idempotency-Key")
	}
	out, err := h.svc.StartTopUp(c.Request.Context(), userID, req.AmountPaise, req.IdempotencyKey)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "TOPUP_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

type confirmTopUpRequest struct {
	UPITxnRef string `json:"upi_txn_ref"`
}

// PostConfirmTopUp handles POST /v1/wallet/top-up/:id/confirm. Idempotent:
// repeated calls with the same upi_txn_ref return the current state.
func (h *Handler) PostConfirmTopUp(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	txID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var req confirmTopUpRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	tx, err := h.svc.ConfirmTopUp(c.Request.Context(), userID, txID, req.UPITxnRef)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "TOPUP_CONFIRM_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, tx)
}

// GetTopUp handles GET /v1/wallet/top-up/:id. Returns the transaction so the
// client can poll for completion.
func (h *Handler) GetTopUp(c *gin.Context) {
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
