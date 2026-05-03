package http

import (
	"net/http"

	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// estimateRequest mirrors the API spec for POST /v1/rider/estimate.
type estimateRequest struct {
	PickupLat       float64   `json:"pickup_lat"`
	PickupLng       float64   `json:"pickup_lng"`
	DropLat         float64   `json:"drop_lat"`
	DropLng         float64   `json:"drop_lng"`
	VehicleType     string    `json:"vehicle_type"`
	CityID          uuid.UUID `json:"city_id"`
	SurgeMultiplier float64   `json:"surge_multiplier,omitempty"`
}

// PostEstimate — POST /v1/rider/estimate. Public.
func (h *Handler) PostEstimate(c *gin.Context) {
	var body estimateRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.EstimateFare(c.Request.Context(), service.FareEstimateRequest{
		PickupLat:       body.PickupLat,
		PickupLng:       body.PickupLng,
		DropLat:         body.DropLat,
		DropLng:         body.DropLng,
		VehicleType:     body.VehicleType,
		CityID:          body.CityID,
		SurgeMultiplier: body.SurgeMultiplier,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "FARE_ESTIMATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}
