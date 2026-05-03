package http

import (
	"net/http"

	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// sosRequest is the body for POST /v1/rider/rides/:id/sos.
type sosRequest struct {
	Lat *float64 `json:"lat,omitempty"`
	Lng *float64 `json:"lng,omitempty"`
}

// PostSOS — POST /v1/rider/rides/:id/sos. Customer panic button.
func (h *Handler) PostSOS(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	rideID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body sosRequest
	_ = c.ShouldBindJSON(&body) // empty body OK
	out, err := h.svc.TriggerSOS(c.Request.Context(), uid, rideID, body.Lat, body.Lng)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SOS_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, out)
}

// PostShareToken — POST /v1/rider/rides/:id/share. Customer creates a
// 24-hour share link bound to the ride.
func (h *Handler) PostShareToken(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	rideID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.CreateShareToken(c.Request.Context(), uid, rideID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SHARE_TOKEN_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, out)
}

// GetSharedRide — GET /v1/rider/share/:token. PUBLIC (no auth required) —
// returns the redacted ride view to whoever has the link.
func (h *Handler) GetSharedRide(c *gin.Context) {
	token := c.Param("token")
	if token == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_TOKEN", "missing token", nil)
		return
	}
	out, err := h.svc.GetSharedRide(c.Request.Context(), token)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SHARE_FETCH_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// trustedContactRequest is the body for PUT /v1/rider/trusted-contact.
type trustedContactRequest struct {
	Name         string  `json:"name"`
	Phone        string  `json:"phone"`
	Relationship *string `json:"relationship,omitempty"`
	ShareOnSOS   bool    `json:"share_on_sos"`
}

// PutTrustedContact — PUT /v1/rider/trusted-contact.
func (h *Handler) PutTrustedContact(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	var body trustedContactRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.SetTrustedContact(c.Request.Context(), uid, service.SetTrustedContactRequest{
		Name:         body.Name,
		Phone:        body.Phone,
		Relationship: body.Relationship,
		ShareOnSOS:   body.ShareOnSOS,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "TRUSTED_CONTACT_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// GetTrustedContact — GET /v1/rider/trusted-contact.
func (h *Handler) GetTrustedContact(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.GetTrustedContact(c.Request.Context(), uid)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "TRUSTED_CONTACT_FETCH_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}
