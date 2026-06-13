package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// InitiateMaskedCallRequest carries the callee + optional ride.
type InitiateMaskedCallRequest struct {
	CalleeID uuid.UUID  `json:"callee_id"`
	RideID   *uuid.UUID `json:"ride_id,omitempty"`
}

// PostInitiateMaskedCall — POST /v1/rider/safety/masked-call
//
// Returns the proxy DID the caller mobile app dials so the callee's
// real number is never exposed.
func (h *Handler) PostInitiateMaskedCall(c *gin.Context) {
	raw := c.GetHeader("X-User-Id")
	uid, err := uuid.Parse(raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user id", nil)
		return
	}
	var req InitiateMaskedCallRequest
	if err := c.BindJSON(&req); err != nil || req.CalleeID == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "callee_id required", nil)
		return
	}
	res, err := h.svc.InitiateMaskedCall(c.Request.Context(), req.RideID, uid, req.CalleeID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "MASKED_CALL_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, res, nil)
}

// AdminListSafetyContactAlerts — GET /v1/rider/admin/safety/incidents/:id/alerts
func (h *Handler) AdminListSafetyContactAlerts(c *gin.Context) {
	incID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_INCIDENT_ID", err.Error(), nil)
		return
	}
	rows, err := h.svc.ListSafetyContactAlerts(c.Request.Context(), incID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "LIST_ALERTS_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"alerts": rows}, nil)
}
