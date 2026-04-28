package http

import (
	"errors"
	"net/http"

	"github.com/atpost/orders-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// CreateReturnRequest POST /v1/orders/:orderId/return
func (h *Handler) CreateReturnRequest(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orderID, err := uuid.Parse(c.Param("orderId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid order id", nil)
		return
	}
	var body struct {
		Reason       string   `json:"reason" binding:"required"`
		Description  string   `json:"description"`
		EvidenceURLs []string `json:"evidence_urls"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	ret, err := h.svc.CreateReturnRequest(c.Request.Context(), userID, orderID, body.Reason, body.Description, body.EvidenceURLs)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrReturnAlreadyExists):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "RETURN_EXISTS", err.Error(), nil)
		case err.Error() == "forbidden":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "access denied", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "CREATE_FAILED", err.Error(), nil)
		}
		return
	}
	api.JSON(c.Writer, http.StatusCreated, ret, nil)
}

// GetReturnRequest GET /v1/returns/:returnId
func (h *Handler) GetReturnRequest(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	returnID, err := uuid.Parse(c.Param("returnId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid return id", nil)
		return
	}
	ret, err := h.svc.GetReturnRequest(c.Request.Context(), userID, returnID)
	if err != nil {
		if err.Error() == "forbidden" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "access denied", nil)
		} else {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "return request not found", nil)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, ret, nil)
}

// ListBuyerReturns GET /v1/returns/buyer
func (h *Handler) ListBuyerReturns(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit, offset := parsePagination(c)
	returns, err := h.svc.ListReturnsByBuyer(c.Request.Context(), userID, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FETCH_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, returns, nil)
}

// ListSellerReturns GET /v1/returns/seller
func (h *Handler) ListSellerReturns(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	status := c.Query("status")
	limit, offset := parsePagination(c)
	returns, err := h.svc.ListReturnsBySeller(c.Request.Context(), userID, status, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FETCH_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, returns, nil)
}

// ApproveReturn POST /v1/returns/:returnId/approve
func (h *Handler) ApproveReturn(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	returnID, err := uuid.Parse(c.Param("returnId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid return id", nil)
		return
	}
	var body struct {
		SellerNote   string  `json:"seller_note"`
		RefundAmount float64 `json:"refund_amount" binding:"required"`
		RefundMethod string  `json:"refund_method" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.ApproveReturn(c.Request.Context(), userID, returnID, body.SellerNote, body.RefundAmount, body.RefundMethod); err != nil {
		switch {
		case errors.Is(err, service.ErrReturnNotFound):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		case err.Error() == "forbidden":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "access denied", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "APPROVE_FAILED", err.Error(), nil)
		}
		return
	}
	c.Status(http.StatusNoContent)
}

// RejectReturn POST /v1/returns/:returnId/reject
func (h *Handler) RejectReturn(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	returnID, err := uuid.Parse(c.Param("returnId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid return id", nil)
		return
	}
	var body struct {
		SellerNote string `json:"seller_note"`
	}
	c.ShouldBindJSON(&body) //nolint:errcheck
	if err := h.svc.RejectReturn(c.Request.Context(), userID, returnID, body.SellerNote); err != nil {
		switch {
		case errors.Is(err, service.ErrReturnNotFound):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		case err.Error() == "forbidden":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "access denied", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "REJECT_FAILED", err.Error(), nil)
		}
		return
	}
	c.Status(http.StatusNoContent)
}

// MarkItemReceived POST /v1/returns/:returnId/received
func (h *Handler) MarkItemReceived(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	returnID, err := uuid.Parse(c.Param("returnId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid return id", nil)
		return
	}
	if err := h.svc.MarkItemReceived(c.Request.Context(), userID, returnID); err != nil {
		switch {
		case errors.Is(err, service.ErrReturnNotFound):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		case err.Error() == "forbidden":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "access denied", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "UPDATE_FAILED", err.Error(), nil)
		}
		return
	}
	c.Status(http.StatusNoContent)
}

// UpdateReturnTracking POST /v1/returns/:returnId/tracking
func (h *Handler) UpdateReturnTracking(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	returnID, err := uuid.Parse(c.Param("returnId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid return id", nil)
		return
	}
	var body struct {
		TrackingNumber string `json:"tracking_number" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.UpdateReturnTracking(c.Request.Context(), userID, returnID, body.TrackingNumber); err != nil {
		switch {
		case errors.Is(err, service.ErrReturnNotFound):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		case err.Error() == "forbidden":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "access denied", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "UPDATE_FAILED", err.Error(), nil)
		}
		return
	}
	c.Status(http.StatusNoContent)
}
