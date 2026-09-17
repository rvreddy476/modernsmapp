package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// rideLocation matches the spec body shape.
type rideLocation struct {
	Address string  `json:"address"`
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
}

// createRideRequest — body for POST /v1/rider/rides.
type createRideRequest struct {
	QuoteID        *uuid.UUID   `json:"quote_id,omitempty"`
	Pickup         rideLocation `json:"pickup"`
	Drop           rideLocation `json:"drop"`
	VehicleType    string       `json:"vehicle_type"`
	CityID         *uuid.UUID   `json:"city_id,omitempty"`
	PaymentMethod  string       `json:"payment_method,omitempty"`
	IdempotencyKey string       `json:"idempotency_key"`
}

// PostRide — POST /v1/rider/rides.
func (h *Handler) PostRide(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	var body createRideRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	headerKey := c.GetHeader("Idempotency-Key")
	bodyKey := body.IdempotencyKey
	if headerKey != "" && bodyKey != "" && headerKey != bodyKey {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "IDEMPOTENCY_KEY_MISMATCH", "Header Idempotency-Key and body idempotency_key must match", nil)
		return
	}
	idempKey := headerKey
	if idempKey == "" {
		idempKey = bodyKey
	}
	if idempKey == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is mandatory for booking creation", nil)
		return
	}

	out, err := h.svc.CreateRide(c.Request.Context(), uid, service.CreateRideRequest{
		QuoteID:        body.QuoteID,
		PickupAddress:  body.Pickup.Address,
		PickupLat:      body.Pickup.Lat,
		PickupLng:      body.Pickup.Lng,
		DropAddress:    body.Drop.Address,
		DropLat:        body.Drop.Lat,
		DropLng:        body.Drop.Lng,
		VehicleType:    body.VehicleType,
		CityID:         body.CityID,
		PaymentMethod:  body.PaymentMethod,
		IdempotencyKey: idempKey,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "RIDE_CREATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, out)
}

// GetActiveRide — GET /v1/rider/rides/active.
func (h *Handler) GetActiveRide(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.GetActiveRideForCustomer(c.Request.Context(), uid)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "ACTIVE_RIDE_FAILED")
		return
	}
	if out == nil {
		api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"active": false, "ride": nil})
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"active": true, "ride": out})
}

// GetRideReceipt — GET /v1/rider/rides/:id/receipt.
func (h *Handler) GetRideReceipt(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	rideID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	receipt, err := h.svc.GetRideReceipt(c.Request.Context(), uid, rideID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "RECEIPT_FETCH_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, receipt)
}

type confirmCashRequest struct {
	ExpectedRevision int `json:"expected_revision,omitempty"`
}

// PostConfirmCashPayment — POST /v1/rider/rides/:id/payment/cash-confirm.
func (h *Handler) PostConfirmCashPayment(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	rideID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body confirmCashRequest
	_ = c.ShouldBindJSON(&body)
	expRev := body.ExpectedRevision
	if expRev == 0 {
		expRev = parseExpectedRevision(c)
	}
	if err := h.svc.ConfirmCashPayment(c.Request.Context(), uid, rideID, expRev); err != nil {
		mapTransitionError(c, err, "CASH_CONFIRM_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"status": "succeeded", "message": "cash payment confirmed"})
}

// DeleteShareToken — DELETE /v1/rider/rides/:id/share.
func (h *Handler) DeleteShareToken(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	rideID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.Store().RevokeShareTokensForRide(c.Request.Context(), rideID, uid); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SHARE_REVOKE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"status": "revoked"})
}

// GetRide — GET /v1/rider/rides/:id.
func (h *Handler) GetRide(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	rideID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.GetRide(c.Request.Context(), uid, rideID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "RIDE_FETCH_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// GetMyRides — GET /v1/rider/rides/me.
func (h *Handler) GetMyRides(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	limit := 50
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	out, err := h.svc.ListMyRides(c.Request.Context(), uid, limit)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "RIDE_LIST_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}
