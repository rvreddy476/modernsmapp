package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AdminHideRatingRequest carries the new visibility verdict.
type AdminHideRatingRequest struct {
	Visibility string `json:"visibility"` // hidden | flagged | public
}

// AdminHideRideRating — POST /v1/rider/admin/rides/:id/rating/visibility
func (h *Handler) AdminHideRideRating(c *gin.Context) {
	raw := c.GetHeader("X-User-Id")
	adminID, err := uuid.Parse(raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user id", nil)
		return
	}
	rideID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_RIDE_ID", err.Error(), nil)
		return
	}
	var req AdminHideRatingRequest
	if err := c.BindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.AdminHideRideRating(c.Request.Context(), adminID, rideID, req.Visibility); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "HIDE_RATING_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ride_id": rideID.String(), "rating_visibility": req.Visibility}, nil)
}

// PartnerRespondRatingRequest carries the partner's reply text.
type PartnerRespondRatingRequest struct {
	Response string `json:"response"`
}

// PostPartnerRespondRating — POST /v1/rider/rides/:id/rating/response
func (h *Handler) PostPartnerRespondRating(c *gin.Context) {
	raw := c.GetHeader("X-User-Id")
	uid, err := uuid.Parse(raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user id", nil)
		return
	}
	rideID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_RIDE_ID", err.Error(), nil)
		return
	}
	var req PartnerRespondRatingRequest
	if err := c.BindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.PartnerRespondToRating(c.Request.Context(), uid, rideID, req.Response); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "RATING_RESPONSE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ride_id": rideID.String(), "responded": true}, nil)
}
