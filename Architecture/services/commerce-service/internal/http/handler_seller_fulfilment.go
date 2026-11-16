package http

// The seller's fulfilment writes and the order timeline.
//
//	POST /v1/commerce/seller/orders/:orderId/pack     confirmed -> packed
//	POST /v1/commerce/seller/orders/:orderId/cancel   confirmed|packed -> cancelled, body {reason}
//	POST /v1/commerce/seller/orders/:orderId/ship     books the shipment, body {courier, tracking_number}
//	POST /v1/commerce/seller/orders/:orderId/mark-delivered
//	                                                  shipped|out_for_delivery -> delivered, seller-shipped
//	                                                  parcels only, body {delivered_at?, note?, proof_media_id?}
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
	"io"
	"net/http"
	"time"

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
	// Mark delivered (seller_delivery.go).
	case errors.Is(err, service.ErrShipmentNotSellerManaged):
		api.ErrorWithContext(ctx, w, http.StatusConflict, "SHIPMENT_NOT_SELLER_MANAGED",
			"this order's parcel is tracked by a courier; it is marked delivered by the courier's scan", nil)
	case errors.Is(err, service.ErrOrderNotShipped):
		api.ErrorWithContext(ctx, w, http.StatusConflict, "ORDER_NOT_SHIPPED", err.Error(), nil)
	case errors.Is(err, service.ErrDeliveredAtInFuture):
		api.ErrorWithContext(ctx, w, http.StatusBadRequest, "DELIVERED_AT_IN_FUTURE", err.Error(), nil)
	case errors.Is(err, service.ErrDeliveredAtBeforeShipped):
		api.ErrorWithContext(ctx, w, http.StatusBadRequest, "DELIVERED_AT_BEFORE_SHIPPED", err.Error(), nil)
	case errors.Is(err, service.ErrDeliveryNoteTooLong):
		api.ErrorWithContext(ctx, w, http.StatusBadRequest, "NOTE_TOO_LONG",
			"the note can be at most 500 characters", nil)
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

type sellerMarkDeliveredReq struct {
	// RFC 3339; omitted means now.
	DeliveredAt  *time.Time `json:"delivered_at"`
	Note         string     `json:"note"`
	ProofMediaID *uuid.UUID `json:"proof_media_id"`
}

// SellerMarkDelivered POST /v1/commerce/seller/orders/:orderId/mark-delivered
//
// Only for an order the seller shipped with their own courier (every
// shipment seller_managed); see service.SellerMarkDelivered. The body is
// optional. A repeat answers 200 with applied=false.
func (h *Handler) SellerMarkDelivered(c *gin.Context) {
	userID, seller, orderID, ok := h.sellerOrderCall(c)
	if !ok {
		return
	}
	var req sellerMarkDeliveredReq
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY",
			"body must be {delivered_at?: RFC 3339 time, note?: string, proof_media_id?: uuid}", nil)
		return
	}
	res, err := h.svc.SellerMarkDelivered(c.Request.Context(), seller.ID, userID, orderID, service.MarkDeliveredInput{
		DeliveredAt: req.DeliveredAt, Note: req.Note, ProofMediaID: req.ProofMediaID,
	})
	if err != nil {
		writeSellerFulfilmentError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
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
