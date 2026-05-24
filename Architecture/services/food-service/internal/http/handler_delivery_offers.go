package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListMyDeliveryOffers — GET /v1/food/delivery/offers/me
func (h *Handler) ListMyDeliveryOffers(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	offers, err := h.svc.ListMyPendingDeliveryOffers(c.Request.Context(), uid)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "DELIVERY_OFFERS_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"offers": offers}, nil)
}

// AcceptDeliveryOffer — POST /v1/food/delivery/offers/:offerId/accept
func (h *Handler) AcceptDeliveryOffer(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	offerID, err := uuid.Parse(c.Param("offerId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_OFFER_ID", err.Error(), nil)
		return
	}
	if err := h.svc.AcceptDeliveryOffer(c.Request.Context(), uid, offerID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "DELIVERY_ACCEPT_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"offer_id": offerID.String(), "status": "accepted"}, nil)
}

// RejectDeliveryOfferRequest is the optional reason body.
type RejectDeliveryOfferRequest struct {
	Reason string `json:"reason,omitempty"`
}

// GetBatchForOrder — GET /v1/food/delivery/orders/:orderId/batch
//
// Returns the batch payload (members + sequence + status) for an order
// that's part of a multi-pickup batch, or 404 when the order is
// dispatched solo. Used by partner UI to render "Stop 1 of 2" and by
// customer UI to render the "delivered alongside another order"
// banner.
func (h *Handler) GetBatchForOrder(c *gin.Context) {
	if _, ok := h.requireUser(c); !ok {
		return
	}
	orderID, err := uuid.Parse(c.Param("orderId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ORDER_ID", err.Error(), nil)
		return
	}
	batch, err := h.svc.GetBatchForOrder(c.Request.Context(), orderID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "BATCH_NOT_FOUND", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, batch, nil)
}

// RejectDeliveryOffer — POST /v1/food/delivery/offers/:offerId/reject
func (h *Handler) RejectDeliveryOffer(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	offerID, err := uuid.Parse(c.Param("offerId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_OFFER_ID", err.Error(), nil)
		return
	}
	var req RejectDeliveryOfferRequest
	_ = c.BindJSON(&req)
	if err := h.svc.RejectDeliveryOffer(c.Request.Context(), uid, offerID, req.Reason); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "DELIVERY_REJECT_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"offer_id": offerID.String(), "status": "rejected"}, nil)
}
