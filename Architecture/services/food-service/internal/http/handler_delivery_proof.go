package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// VerifyOTPRequest carries either pickup_code or delivery_code.
type VerifyOTPRequest struct {
	Code string `json:"code"`
}

// PartnerVerifyPickupOTP — POST /v1/food/partner/orders/:orderId/verify-pickup
// Restaurant agent submits the partner's pickup OTP to mark the order
// physically handed off.
func (h *Handler) PartnerVerifyPickupOTP(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	orderID, err := uuid.Parse(c.Param("orderId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ORDER_ID", err.Error(), nil)
		return
	}
	var req VerifyOTPRequest
	if err := c.BindJSON(&req); err != nil || req.Code == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "code is required", nil)
		return
	}
	if err := h.svc.VerifyPickupCode(c.Request.Context(), uid, orderID, req.Code); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "PICKUP_VERIFY_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"order_id": orderID.String(), "status": "PICKED_UP"}, nil)
}

// CustomerVerifyDeliveryOTP — POST /v1/food/orders/:orderId/verify-delivery
// Customer enters the delivery OTP shown on partner's screen.
func (h *Handler) CustomerVerifyDeliveryOTP(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	orderID, err := uuid.Parse(c.Param("orderId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ORDER_ID", err.Error(), nil)
		return
	}
	var req VerifyOTPRequest
	if err := c.BindJSON(&req); err != nil || req.Code == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "code is required", nil)
		return
	}
	if err := h.svc.VerifyDeliveryCode(c.Request.Context(), uid, orderID, req.Code); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "DELIVERY_VERIFY_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"order_id": orderID.String(), "status": "DELIVERED"}, nil)
}

// AttachProofRequest is the partner-side proof-of-handoff upload (MinIO
// object key the client already PUT to). `which` distinguishes pickup
// vs delivery proof.
type AttachProofRequest struct {
	Which string `json:"which"` // pickup | delivery
	URL   string `json:"url"`
}

// PartnerAttachProof — POST /v1/food/delivery/orders/:orderId/proof
func (h *Handler) PartnerAttachProof(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	orderID, err := uuid.Parse(c.Param("orderId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ORDER_ID", err.Error(), nil)
		return
	}
	var req AttachProofRequest
	if err := c.BindJSON(&req); err != nil || req.URL == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "url is required", nil)
		return
	}
	if err := h.svc.AttachProofURL(c.Request.Context(), uid, orderID, req.Which, req.URL); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "PROOF_ATTACH_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"order_id": orderID.String(), "which": req.Which}, nil)
}
