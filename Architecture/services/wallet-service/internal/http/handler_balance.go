package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// balanceResponse is the trimmed shape returned by GET /v1/wallet/balance —
// field set per spec §3.
type balanceResponse struct {
	AvailablePaise    int64  `json:"available_paise"`
	PendingInPaise    int64  `json:"pending_in_paise"`
	PendingOutPaise   int64  `json:"pending_out_paise"`
	KYCTier           string `json:"kyc_tier"`
	MonthlyLimitPaise int64  `json:"monthly_limit_paise"`
	IsFrozen          bool   `json:"is_frozen"`
}

// GetBalance handles GET /v1/wallet/balance.
func (h *Handler) GetBalance(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	b, err := h.svc.GetBalance(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "BALANCE_ERROR")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, balanceResponse{
		AvailablePaise:    b.AvailablePaise,
		PendingInPaise:    b.PendingInPaise,
		PendingOutPaise:   b.PendingOutPaise,
		KYCTier:           string(b.KYCTier),
		MonthlyLimitPaise: b.MonthlyLimitPaise,
		IsFrozen:          b.IsFrozen,
	})
}
