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
	out, err := h.svc.CreateRide(c.Request.Context(), uid, service.CreateRideRequest{
		PickupAddress:  body.Pickup.Address,
		PickupLat:      body.Pickup.Lat,
		PickupLng:      body.Pickup.Lng,
		DropAddress:    body.Drop.Address,
		DropLat:        body.Drop.Lat,
		DropLng:        body.Drop.Lng,
		VehicleType:    body.VehicleType,
		CityID:         body.CityID,
		PaymentMethod:  body.PaymentMethod,
		IdempotencyKey: body.IdempotencyKey,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "RIDE_CREATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, out)
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
