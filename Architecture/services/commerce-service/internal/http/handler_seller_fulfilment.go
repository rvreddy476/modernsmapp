package http

// The seller's fulfilment writes and the order timeline.
//
//	POST /v1/commerce/seller/orders/:orderId/pack     confirmed -> packed
//	POST /v1/commerce/seller/orders/:orderId/cancel   confirmed|packed -> cancelled, body {reason}
//	POST /v1/commerce/seller/orders/:orderId/ship     books the shipment, body {courier, tracking_number}
//	GET  /v1/commerce/seller/orders/:orderId/history  order_status_history, oldest first
//
// None of these sit under a fenced prefix (handler_p0.go FencedPrefixes
// covers RFQ, organisations, bulk import, affiliate, payout, remittances,
// earnings and returns; /seller/orders is live), so they are reachable
// through FenceMiddleware without an exemption.
//
// Ownership is the seller-orders rule, not the buyer's: the caller's seller
// profile is resolved (403 NO_SELLER without one) and the order must carry
// that seller's lines. A seller with no line on the order gets 403, the same
// answer GET /seller/orders/:id gives, so the two clients need one rule.
//
// Repeats are idempotent: a second pack or cancel answers 200 with
// applied=false and the state the order is already in, matching what
// Store.CancelOrder has always done for a retried customer cancel (it does
// not refuse). A move the matrix forbids answers 409.

import (
	"errors"
	"net/http"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// sellerOrderCall resolves the caller's seller profile and the order id, or
// writes the refusal and returns false.
func (h *Handler) sellerOrderCall(c *gin.Context) (userID uuid.UUID, seller *postgres.Seller, orderID uuid.UUID, ok bool) {
	userID, ok = getUserID(c)
	if !ok {
		return
	}
	orderID, ok = parseUUID(c, "orderId")
	if !ok {
		return
	}
	seller, ok = h.sellerForCaller(c, userID)
	return
}

// writeSellerFulfilmentError maps the ownership and matrix refusals; anything
// else goes through the typed commerce mapping.
func writeSellerFulfilmentError(c *gin.Context, err error) {
	ctx, w := c.Request.Context(), c.Writer
	switch {
	case errors.Is(err, service.ErrOrderNotFound):
		api.ErrorWithContext(ctx, w, http.StatusNotFound, "NOT_FOUND", "order not found", nil)
	case errors.Is(err, service.ErrNotOrderOwner):
		api.ErrorWithContext(ctx, w, http.StatusForbidden, "FORBIDDEN", "no items for this seller", nil)
	case errors.Is(err, service.ErrOrderSharedWithOtherSellers):
		api.ErrorWithContext(ctx, w, http.StatusConflict, "ORDER_SHARED",
			"this order has lines from other sellers and cannot be moved by one of them", nil)
	case errors.Is(err, service.ErrCancelReasonRequired):
		api.ErrorWithContext(ctx, w, http.StatusBadRequest, "REASON_REQUIRED", err.Error(), nil)
	default:
		writeCommerceError(c, err)
	}
}

// SellerPackOrder POST /v1/commerce/seller/orders/:orderId/pack
func (h *Handler) SellerPackOrder(c *gin.Context) {
	userID, seller, orderID, ok := h.sellerOrderCall(c)
	if !ok {
		return
	}
	res, err := h.svc.SellerPackOrder(c.Request.Context(), seller.ID, userID, orderID)
	if err != nil {
		writeSellerFulfilmentError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}

type sellerCancelOrderReq struct {
	Reason string `json:"reason"`
}

// SellerCancelOrder POST /v1/commerce/seller/orders/:orderId/cancel
func (h *Handler) SellerCancelOrder(c *gin.Context) {
	userID, seller, orderID, ok := h.sellerOrderCall(c)
	if !ok {
		return
	}
	var req sellerCancelOrderReq
	// A missing or malformed body is the same as a missing reason, which
	// the service refuses by name; binding errors need no separate answer.
	_ = c.ShouldBindJSON(&req)
	res, err := h.svc.SellerCancelOrder(c.Request.Context(), seller.ID, userID, orderID, req.Reason)
	if err != nil {
		writeSellerFulfilmentError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}

// SellerShipOrder POST /v1/commerce/seller/orders/:orderId/ship
//
// The seller-prefixed spelling of POST /orders/:orderId/shipment, which the
// Android seller screen was written against. Same handler, same gate, same
// body; it exists so the seller surface is one prefix.
func (h *Handler) SellerShipOrder(c *gin.Context) {
	h.CreateShipment(c)
}

// SellerOrderHistory GET /v1/commerce/seller/orders/:orderId/history
func (h *Handler) SellerOrderHistory(c *gin.Context) {
	_, seller, orderID, ok := h.sellerOrderCall(c)
	if !ok {
		return
	}
	rows, err := h.svc.SellerOrderHistory(c.Request.Context(), seller.ID, orderID)
	if err != nil {
		writeSellerFulfilmentError(c, err)
		return
	}
	if rows == nil {
		rows = []*postgres.OrderStatusHistory{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"order_id": orderID, "history": rows}, nil)
}
