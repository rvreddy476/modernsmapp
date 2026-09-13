package http

import (
	"errors"
	"net/http"

	"github.com/atpost/food-service/internal/store/postgres"
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
		if writeKnownError(c, err) {
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "PICKUP_VERIFY_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"order_id": orderID.String(), "status": "PICKED_UP"}, nil)
}

// CustomerVerifyDeliveryOTP — POST /v1/food/orders/:orderId/verify-delivery
//
// GONE (B5c). The customer used to both see delivery_code and submit it, so
// the code proved nothing about the handover. The customer now SHOWS the code
// (order detail, while the food is with the rider) and the rider enters it at
// POST /v1/food/delivery/assignments/:assignmentId/verify-delivery. This route
// never marks an order delivered; it answers 410 with a stable code so an old
// client can tell the user what changed.
func (h *Handler) CustomerVerifyDeliveryOTP(c *gin.Context) {
	if _, ok := h.requireUser(c); !ok {
		return
	}
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusGone, "FOOD_DELIVERY_CODE_ENTERED_BY_RIDER",
		"show the delivery code to your delivery partner; they enter it to complete the delivery", nil)
}

// RiderVerifyDeliveryCode — POST /v1/food/delivery/assignments/:assignmentId/verify-delivery
//
// The rider holding the assignment enters the code the customer shows. Only
// their own active assignment, only after pickup; wrong codes are counted and
// the assignment locks after store.MaxDeliveryCodeAttempts.
func (h *Handler) RiderVerifyDeliveryCode(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	assignmentID, err := uuid.Parse(c.Param("assignmentId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ASSIGNMENT_ID", "assignment id must be a UUID", nil)
		return
	}
	var req VerifyOTPRequest
	if err := c.BindJSON(&req); err != nil || req.Code == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "code is required", nil)
		return
	}
	v, err := h.svc.RiderVerifyDeliveryCode(c.Request.Context(), uid, assignmentID, req.Code)
	if err != nil {
		if errors.Is(err, postgres.ErrDeliveryCodeInvalid) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, "FOOD_DELIVERY_CODE_INVALID", "the delivery code does not match", nil)
			return
		}
		if writeKnownError(c, err) {
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "DELIVERY_VERIFY_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"assignment_id": assignmentID.String(),
		"order_id":      v.OrderID.String(),
		"status":        "DELIVERED",
	}, nil)
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
