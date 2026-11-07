// Return route handlers: create, read, approve and reject a return, the
// refund preview and the buyer's return list. Moved out of handler.go
// unchanged.

package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ─── Return handlers ─────────────────────────────────────────────

// createReturnItem is one line of the multi-item return body (Phase 2.3).
type createReturnItem struct {
	OrderItemID       uuid.UUID `json:"order_item_id" binding:"required"`
	SellerID          uuid.UUID `json:"seller_id" binding:"required"`
	ReasonCode        string    `json:"reason_code" binding:"required"`
	ReasonDescription *string   `json:"reason_description"`
}

// createReturnReq accepts both the multi-item shape `{items:[...]}` and
// the legacy single-item top-level shape — Phase 2.3 lets the mobile app
// fold its current N-call fan-out into a single request without breaking
// the existing single-item callers.
type createReturnReq struct {
	Items           []createReturnItem `json:"items"`
	PickupAddressID *uuid.UUID         `json:"pickup_address_id"`
	// Legacy single-item top-level fields:
	OrderItemID       *uuid.UUID `json:"order_item_id"`
	SellerID          *uuid.UUID `json:"seller_id"`
	ReasonCode        string     `json:"reason_code"`
	ReasonDescription *string    `json:"reason_description"`
}

func (h *Handler) CreateReturn(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	var req createReturnReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	var items []service.BulkReturnItemInput
	multiItem := len(req.Items) > 0
	if multiItem {
		items = make([]service.BulkReturnItemInput, 0, len(req.Items))
		for _, it := range req.Items {
			items = append(items, service.BulkReturnItemInput{
				OrderItemID:       it.OrderItemID,
				SellerID:          it.SellerID,
				ReasonCode:        it.ReasonCode,
				ReasonDescription: it.ReasonDescription,
			})
		}
	} else if req.OrderItemID != nil && req.SellerID != nil {
		items = []service.BulkReturnItemInput{{
			OrderItemID:       *req.OrderItemID,
			SellerID:          *req.SellerID,
			ReasonCode:        req.ReasonCode,
			ReasonDescription: req.ReasonDescription,
		}}
	} else {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY",
			"either items[] or order_item_id+seller_id is required", nil)
		return
	}

	out, err := h.svc.CreateReturnRequestBulk(c.Request.Context(), orderID, userID, items)
	if err != nil && len(out) == 0 {
		handleErr(c, err)
		return
	}
	// Single-item legacy callers get the un-wrapped response. Multi-item
	// callers always get `{items:[...]}` so partial-success is observable.
	if !multiItem && len(out) == 1 {
		api.JSON(c.Writer, http.StatusCreated, out[0], nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, gin.H{"items": out}, nil)
}

// GetReturn GET /v1/commerce/returns/:returnId — Phase 2.2.
// Customer or seller of the return only; mobile was previously listing
// /me/returns and filtering client-side because this endpoint didn't
// exist.
func (h *Handler) GetReturn(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	returnID, ok := parseUUID(c, "returnId")
	if !ok {
		return
	}
	r, err := h.svc.GetReturnRequest(c.Request.Context(), returnID, userID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrReturnNotFound):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		case errors.Is(err, service.ErrNotReturnParty):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		default:
			handleErr(c, err)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, r, nil)
}

// ApproveReturn is called by the seller (or admin) to approve a customer's
// return request. Books the reverse-pickup label and initiates the refund.
//
//	POST /v1/commerce/returns/:returnId/approve
func (h *Handler) ApproveReturn(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	returnID, ok := parseUUID(c, "returnId")
	if !ok {
		return
	}
	out, err := h.svc.ApproveReturn(c.Request.Context(), returnID, actorID)
	if err != nil {
		if errors.Is(err, service.ErrNotReturnSeller) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "APPROVE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

type rejectReturnReq struct {
	Reason string `json:"reason" binding:"required"`
}

// RejectReturn closes a return with the seller's stated reason.
//
//	POST /v1/commerce/returns/:returnId/reject
func (h *Handler) RejectReturn(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	returnID, ok := parseUUID(c, "returnId")
	if !ok {
		return
	}
	var req rejectReturnReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.RejectReturn(c.Request.Context(), returnID, actorID, req.Reason)
	if err != nil {
		if errors.Is(err, service.ErrNotReturnSeller) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "REJECT_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// PreviewReturnRefund GET /v1/commerce/returns/:returnId/refund-preview —
// Phase 4.3. Customer or seller can call; refuses other actors.
func (h *Handler) PreviewReturnRefund(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	returnID, ok := parseUUID(c, "returnId")
	if !ok {
		return
	}
	amount, err := h.svc.PreviewReturnRefund(c.Request.Context(), returnID, userID)
	if err != nil {
		if errors.Is(err, service.ErrReturnNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "return not found", nil)
			return
		}
		if errors.Is(err, service.ErrNotReturnParty) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "not a party to this return", nil)
			return
		}
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"refund_amount": amount}, nil)
}

func (h *Handler) ListMyReturns(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	returns, err := h.svc.ListMyReturns(c.Request.Context(), userID, limit, offset)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, returns, nil)
}
