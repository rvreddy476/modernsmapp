package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Admin wallet operations
// ---------------------------------------------------------------------------

func (h *Handler) FreezeWallet(c *gin.Context) {
	_, ok := getAdminID(c)
	if !ok {
		return
	}

	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	if err := h.svc.FreezeWallet(c.Request.Context(), userID); err != nil {
		if err.Error() == "WALLET_NOT_FOUND" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "WALLET_NOT_FOUND", "Wallet not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "frozen"}, nil)
}

func (h *Handler) UnfreezeWallet(c *gin.Context) {
	_, ok := getAdminID(c)
	if !ok {
		return
	}

	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	if err := h.svc.UnfreezeWallet(c.Request.Context(), userID); err != nil {
		if err.Error() == "WALLET_NOT_FOUND" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "WALLET_NOT_FOUND", "Wallet not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unfrozen"}, nil)
}

func (h *Handler) RebuildWallet(c *gin.Context) {
	_, ok := getAdminID(c)
	if !ok {
		return
	}

	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	newBalance, err := h.svc.RebuildWallet(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{
		"status":      "rebuilt",
		"new_balance": newBalance,
	}, nil)
}
