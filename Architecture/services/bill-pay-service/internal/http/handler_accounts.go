package http

import (
	"net/http"

	"github.com/atpost/bill-pay-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// CreateAccount handles POST /v1/billpay/accounts.
func (h *Handler) CreateAccount(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	var req service.CreateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	acc, err := h.svc.CreateAccount(c.Request.Context(), uid, req)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "ACCOUNT_CREATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, acc)
}

// ListAccounts handles GET /v1/billpay/accounts.
func (h *Handler) ListAccounts(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	accs, err := h.svc.ListAccounts(c.Request.Context(), uid)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "ACCOUNTS_ERROR")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, accs)
}

// PatchAccount handles PATCH /v1/billpay/accounts/:id.
func (h *Handler) PatchAccount(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var req service.UpdateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	acc, err := h.svc.UpdateAccount(c.Request.Context(), uid, id, req)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "ACCOUNT_UPDATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, acc)
}

// DeleteAccount handles DELETE /v1/billpay/accounts/:id.
func (h *Handler) DeleteAccount(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.DeleteAccount(c.Request.Context(), uid, id); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "ACCOUNT_DELETE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"deleted": true})
}

// GetAccountBill handles GET /v1/billpay/accounts/:id/bill.
func (h *Handler) GetAccountBill(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	bill, err := h.svc.FetchBill(c.Request.Context(), uid, id)
	if err != nil {
		respondServiceError(c, err, http.StatusBadGateway, "BILL_FETCH_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, bill)
}
