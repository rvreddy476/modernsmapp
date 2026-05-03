package http

import (
	"net/http"
	"strings"

	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// cancelRideRequest is the body for POST /v1/rider/rides/:id/cancel.
type cancelRideRequest struct {
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// PostCancelRide — POST /v1/rider/rides/:id/cancel. Customers cancel their
// own rides. Partner cancellation goes through PostPartnerCancelRide below.
func (h *Handler) PostCancelRide(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	rideID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body cancelRideRequest
	_ = c.ShouldBindJSON(&body)
	out, err := h.svc.CancelRide(c.Request.Context(), uid, rideID, "customer", service.CancelRideRequest{
		Reason:         body.Reason,
		IdempotencyKey: body.IdempotencyKey,
	})
	if err != nil {
		mapTransitionError(c, err, "RIDE_CANCEL_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// rateRideRequest is the body for POST /v1/rider/rides/:id/rate.
type rateRideRequest struct {
	Rating  int16  `json:"rating"`
	Comment string `json:"comment,omitempty"`
}

// PostRateRide — POST /v1/rider/rides/:id/rate.
func (h *Handler) PostRateRide(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	rideID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body rateRideRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.RateRide(c.Request.Context(), uid, rideID, service.RateRideRequest{
		Rating:  body.Rating,
		Comment: body.Comment,
	}); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "RIDE_RATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"ok": true})
}

// GetShareRide — GET /v1/rider/rides/:id/share. Returns a one-time share URL
// for the ride. Idempotent — same token returned across calls.
func (h *Handler) GetShareRide(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	rideID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	baseURL := c.GetHeader("X-Public-Share-Base")
	out, err := h.svc.ShareRide(c.Request.Context(), uid, rideID, baseURL)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SHARE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// PostMarkArriving — POST /v1/rider/rides/:id/arriving (partner-side).
func (h *Handler) PostMarkArriving(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	rideID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.MarkArriving(c.Request.Context(), uid, rideID); err != nil {
		mapTransitionError(c, err, "RIDE_ARRIVING_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"ok": true})
}

// PostMarkArrived — POST /v1/rider/rides/:id/arrived (partner-side).
func (h *Handler) PostMarkArrived(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	rideID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.MarkArrived(c.Request.Context(), uid, rideID); err != nil {
		mapTransitionError(c, err, "RIDE_ARRIVED_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"ok": true})
}

// startRideRequest is the body for POST /v1/rider/rides/:id/start.
type startRideRequest struct {
	OTP string `json:"otp"`
}

// PostStartRide — POST /v1/rider/rides/:id/start (partner-side, OTP-gated).
func (h *Handler) PostStartRide(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	rideID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body startRideRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.StartRide(c.Request.Context(), uid, rideID, body.OTP); err != nil {
		mapTransitionError(c, err, "RIDE_START_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"ok": true})
}

// completeRideRequest is the body for POST /v1/rider/rides/:id/complete.
type completeRideRequest struct {
	FinalDistanceKM  float64 `json:"final_distance_km"`
	FinalDurationMin int     `json:"final_duration_min"`
	IdempotencyKey   string  `json:"idempotency_key"`
}

// PostCompleteRide — POST /v1/rider/rides/:id/complete (partner-side).
func (h *Handler) PostCompleteRide(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	rideID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body completeRideRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.CompleteRide(c.Request.Context(), uid, rideID, service.CompleteRideRequest{
		FinalDistanceKM:  body.FinalDistanceKM,
		FinalDurationMin: body.FinalDurationMin,
		IdempotencyKey:   body.IdempotencyKey,
	})
	if err != nil {
		mapTransitionError(c, err, "RIDE_COMPLETE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// mapTransitionError forwards "invalid_transition:" service errors as 409
// Conflict, defers everything else to the standard mapper.
func mapTransitionError(c *gin.Context, err error, defaultCode string) {
	msg := err.Error()
	if strings.Contains(msg, "invalid state transition") || strings.Contains(msg, "invalid_transition:") {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "INVALID_TRANSITION", msg, nil)
		return
	}
	respondServiceError(c, err, http.StatusInternalServerError, defaultCode)
}
