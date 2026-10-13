package http

import (
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
)

func (h *Handler) GetPartnerRides(c *gin.Context) {
	u, ok := getUserID(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	r, err := h.svc.ListPartnerRides(c.Request.Context(), u, limit)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PARTNER_HISTORY_UNAVAILABLE")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, r)
}

func (h *Handler) PostPartnerRideSupport(c *gin.Context) {
	u, ok := getUserID(c)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var in struct {
		Category    string `json:"category"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "Enter a support category and description", nil)
		return
	}
	r, err := h.svc.CreatePartnerRideSupport(c.Request.Context(), u, id, in.Category, in.Description)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PARTNER_SUPPORT_UNAVAILABLE")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, gin.H{"id": r.ID, "ride_id": r.RideID, "status": r.Status, "created_at": r.CreatedAt})
}

func (h *Handler) GetPartnerActiveRide(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	r, err := h.svc.GetPartnerActiveRide(c.Request.Context(), uid)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PARTNER_RIDE_READ_FAILED")
		return
	}
	// Do not use customerRide: it exposes the customer's verification code.
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"ride": r})
}

func (h *Handler) GetPartnerRide(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	r, err := h.svc.GetPartnerRide(c.Request.Context(), uid, id)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PARTNER_RIDE_READ_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, r)
}
