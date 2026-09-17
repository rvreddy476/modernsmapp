package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/rider-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// estimateRequest mirrors the API spec for POST /v1/rider/estimate.
type estimateRequest struct {
	PickupLat     float64   `json:"pickup_lat"`
	PickupLng     float64   `json:"pickup_lng"`
	PickupLabel   string    `json:"pickup_label,omitempty"`
	PickupPlaceID string    `json:"pickup_place_id,omitempty"`
	DropLat       float64   `json:"drop_lat"`
	DropLng       float64   `json:"drop_lng"`
	DropLabel     string    `json:"drop_label,omitempty"`
	DropPlaceID   string    `json:"drop_place_id,omitempty"`
	VehicleType   string    `json:"vehicle_type,omitempty"`
	CityID        uuid.UUID `json:"city_id"`
}

// PostEstimate — POST /v1/rider/estimate. Public or authenticated.
func (h *Handler) PostEstimate(c *gin.Context) {
	var body estimateRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	var uidPtr *uuid.UUID
	if uid, ok := getUserID(c); ok {
		uidPtr = &uid
	}
	out, err := h.svc.EstimateFare(c.Request.Context(), service.FareEstimateRequest{
		CustomerUserID: uidPtr,
		PickupLat:      body.PickupLat,
		PickupLng:      body.PickupLng,
		PickupLabel:    body.PickupLabel,
		PickupPlaceID:  body.PickupPlaceID,
		DropLat:        body.DropLat,
		DropLng:        body.DropLng,
		DropLabel:      body.DropLabel,
		DropPlaceID:    body.DropPlaceID,
		VehicleType:    body.VehicleType,
		CityID:         body.CityID,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "FARE_ESTIMATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// GetServiceability — GET /v1/rider/serviceability?lat=&lng=. Public.
func (h *Handler) GetServiceability(c *gin.Context) {
	latStr := c.Query("lat")
	lngStr := c.Query("lng")
	if latStr == "" || lngStr == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_PARAMS", "lat and lng query parameters required", nil)
		return
	}
	lat, err1 := strconv.ParseFloat(latStr, 64)
	lng, err2 := strconv.ParseFloat(lngStr, 64)
	if err1 != nil || err2 != nil || lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_COORDINATES", "invalid latitude or longitude", nil)
		return
	}
	city, err := h.svc.Store().FindServiceableCity(c.Request.Context(), lat, lng)
	if err != nil {
		if errors.Is(err, store.ErrCityNotFound) {
			api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{
				"serviceable":             false,
				"city_id":                 nil,
				"city_name":               "",
				"supported_vehicle_types": []string{},
				"message":                 "Location is outside serviceable service zones",
			})
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "SERVICEABILITY_FAILED")
		return
	}

	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{
		"serviceable": true,
		"city_id":     city.ID,
		"city_name":   city.Name,
		"supported_vehicle_types": []string{
			"bike", "auto",
		},
	})
}
