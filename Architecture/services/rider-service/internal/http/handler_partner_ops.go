package http

import (
	"net/http"

	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// PostGoOnline — POST /v1/rider/partners/me/online.
func (h *Handler) PostGoOnline(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	if err := h.svc.GoOnline(c.Request.Context(), uid); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "GO_ONLINE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"is_online": true})
}

// PostGoOffline — POST /v1/rider/partners/me/offline.
func (h *Handler) PostGoOffline(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	if err := h.svc.GoOffline(c.Request.Context(), uid); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "GO_OFFLINE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"is_online": false})
}

// updateLocationRequest is the body for POST /v1/rider/partners/me/location.
type updateLocationRequest struct {
	Lat     float64  `json:"lat"`
	Lng     float64  `json:"lng"`
	Speed   *float64 `json:"speed,omitempty"`
	Heading *float64 `json:"heading,omitempty"`
}

// PostUpdateLocation — POST /v1/rider/partners/me/location.
func (h *Handler) PostUpdateLocation(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	var body updateLocationRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.UpdateLocation(c.Request.Context(), uid, service.UpdateLocationRequest{
		Lat:     body.Lat,
		Lng:     body.Lng,
		Speed:   body.Speed,
		Heading: body.Heading,
	}); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "LOCATION_UPDATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"ok": true})
}

// GetPartnerDashboard — GET /v1/rider/partners/me/dashboard.
func (h *Handler) GetPartnerDashboard(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.GetPartnerDashboard(c.Request.Context(), uid)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "DASHBOARD_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// GetPartnerEarnings — GET /v1/rider/partners/me/earnings?period=today|week|month.
func (h *Handler) GetPartnerEarnings(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	period := c.Query("period")
	if period == "" {
		period = "today"
	}
	out, err := h.svc.GetPartnerEarnings(c.Request.Context(), uid, period)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "EARNINGS_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}
