package http

import (
	"net/http"

	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// addVehicleRequest is the body for POST /v1/rider/partners/me/vehicles.
type addVehicleRequest struct {
	VehicleType        string  `json:"vehicle_type"`
	RegistrationNumber string  `json:"registration_number"`
	Brand              *string `json:"brand,omitempty"`
	Model              *string `json:"model,omitempty"`
	Color              *string `json:"color,omitempty"`
	ManufactureYear    *int    `json:"manufacture_year,omitempty"`
	Year               *int    `json:"year,omitempty"` // alias accepted from mobile
	SeatCount          *int    `json:"seat_count,omitempty"`
	FuelType           *string `json:"fuel_type,omitempty"`
	IsEV               bool    `json:"is_ev,omitempty"`
}

// PostVehicle — POST /v1/rider/partners/me/vehicles.
func (h *Handler) PostVehicle(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	partner, err := h.svc.GetMyPartner(c.Request.Context(), uid)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PARTNER_FETCH_FAILED")
		return
	}
	var body addVehicleRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	year := body.ManufactureYear
	if year == nil {
		year = body.Year
	}
	v, err := h.svc.AddVehicle(c.Request.Context(), uid, partner.ID, service.AddVehicleRequest{
		VehicleType:        body.VehicleType,
		RegistrationNumber: body.RegistrationNumber,
		Brand:              body.Brand,
		Model:              body.Model,
		Color:              body.Color,
		ManufactureYear:    year,
		SeatCount:          body.SeatCount,
		FuelType:           body.FuelType,
		IsEV:               body.IsEV,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "VEHICLE_CREATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, v)
}

// GetMyVehicles — GET /v1/rider/partners/me/vehicles.
func (h *Handler) GetMyVehicles(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	vs, err := h.svc.ListMyVehicles(c.Request.Context(), uid)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "VEHICLE_LIST_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, vs)
}
