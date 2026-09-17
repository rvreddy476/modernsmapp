package http

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// cancelRideRequest is the body for POST /v1/rider/rides/:id/cancel.
type cancelRideRequest struct {
	Reason           string `json:"reason"`
	IdempotencyKey   string `json:"idempotency_key,omitempty"`
	ExpectedRevision int    `json:"expected_revision,omitempty"`
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
	expRev := body.ExpectedRevision
	if expRev == 0 {
		expRev = parseExpectedRevision(c)
	}
	if expRev <= 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "EXPECTED_REVISION_REQUIRED", "expected_revision or If-Match header is mandatory for ride cancellation", nil)
		return
	}
	out, err := h.svc.CancelRide(c.Request.Context(), uid, rideID, "customer", service.CancelRideRequest{
		Reason:           body.Reason,
		IdempotencyKey:   body.IdempotencyKey,
		ExpectedRevision: expRev,
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

func parseExpectedRevision(c *gin.Context) int {
	if revStr := c.GetHeader("If-Match"); revStr != "" {
		if rev, err := strconv.Atoi(strings.Trim(revStr, `"`)); err == nil {
			return rev
		}
	}
	if revStr := c.GetHeader("X-Expected-Revision"); revStr != "" {
		if rev, err := strconv.Atoi(revStr); err == nil {
			return rev
		}
	}
	return 0
}

type arrivingRequest struct {
	ExpectedRevision int `json:"expected_revision,omitempty"`
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
	var body arrivingRequest
	_ = c.ShouldBindJSON(&body)
	expRev := body.ExpectedRevision
	if expRev == 0 {
		expRev = parseExpectedRevision(c)
	}
	if expRev <= 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "EXPECTED_REVISION_REQUIRED", "expected_revision or If-Match header is mandatory", nil)
		return
	}
	if err := h.svc.MarkArriving(c.Request.Context(), uid, rideID, expRev); err != nil {
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
	var body arrivingRequest
	_ = c.ShouldBindJSON(&body)
	expRev := body.ExpectedRevision
	if expRev == 0 {
		expRev = parseExpectedRevision(c)
	}
	if expRev <= 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "EXPECTED_REVISION_REQUIRED", "expected_revision or If-Match header is mandatory", nil)
		return
	}
	if err := h.svc.MarkArrived(c.Request.Context(), uid, rideID, expRev); err != nil {
		mapTransitionError(c, err, "RIDE_ARRIVED_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"ok": true})
}

// startRideRequest is the body for POST /v1/rider/rides/:id/start.
type startRideRequest struct {
	OTP              string `json:"otp"`
	ExpectedRevision int    `json:"expected_revision,omitempty"`
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
	expRev := body.ExpectedRevision
	if expRev == 0 {
		expRev = parseExpectedRevision(c)
	}
	if expRev <= 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "EXPECTED_REVISION_REQUIRED", "expected_revision or If-Match header is mandatory", nil)
		return
	}
	if err := h.svc.StartRide(c.Request.Context(), uid, rideID, body.OTP, expRev); err != nil {
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
	ExpectedRevision int     `json:"expected_revision,omitempty"`
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
	idempKey := c.GetHeader("Idempotency-Key")
	if idempKey == "" {
		idempKey = body.IdempotencyKey
	}
	if idempKey == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key is mandatory for completion", nil)
		return
	}
	expRev := body.ExpectedRevision
	if expRev == 0 {
		expRev = parseExpectedRevision(c)
	}
	if expRev <= 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "EXPECTED_REVISION_REQUIRED", "expected_revision or If-Match header is mandatory", nil)
		return
	}
	out, err := h.svc.CompleteRide(c.Request.Context(), uid, rideID, service.CompleteRideRequest{
		FinalDistanceKM:  body.FinalDistanceKM,
		FinalDurationMin: body.FinalDurationMin,
		IdempotencyKey:   idempKey,
		ExpectedRevision: expRev,
	})
	if err != nil {
		mapTransitionError(c, err, "RIDE_COMPLETE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// mapTransitionError forwards domain errors with appropriate HTTP status codes.
func mapTransitionError(c *gin.Context, err error, defaultCode string) {
	msg := err.Error()
	if strings.Contains(msg, "conflict: revision conflict") || strings.Contains(msg, "revision conflict") {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "REVISION_CONFLICT", "stale state; refresh and try again", nil)
		return
	}
	if strings.Contains(msg, "invalid state transition") || strings.Contains(msg, "invalid_transition:") || strings.Contains(msg, "conflict: invalid state transition") {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "INVALID_TRANSITION", msg, nil)
		return
	}
	if strings.Contains(msg, "forbidden: otp verification temporarily locked") || strings.Contains(msg, "verification locked") {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "OTP_LOCKED", msg, nil)
		return
	}
	if strings.Contains(msg, "forbidden: otp mismatch") || strings.Contains(msg, "forbidden: invalid otp") {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "OTP_MISMATCH", msg, nil)
		return
	}
	if strings.Contains(msg, "forbidden: otp expired") {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "OTP_EXPIRED", msg, nil)
		return
	}
	respondServiceError(c, err, http.StatusInternalServerError, defaultCode)
}
