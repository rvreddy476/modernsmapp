package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// PostNoShowRequest carries the optional reason.
type PostNoShowRequest struct {
	Reason string `json:"reason,omitempty"`
}

// PostMarkNoShow — POST /v1/rider/rides/:id/no-show (partner-only)
//
// Partner reports that the customer didn't arrive at pickup after
// the grace window. The mobile app enforces the wait timer; this
// endpoint is purely the server-side authoritative transition.
func (h *Handler) PostMarkNoShow(c *gin.Context) {
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
	var req PostNoShowRequest
	_ = c.BindJSON(&req)
	if err := h.svc.MarkRideNoShow(c.Request.Context(), uid, rideID, req.Reason); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "MARK_NO_SHOW_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ride_id": rideID.String(), "status": "cancelled", "reason": "customer_no_show"}, nil)
}
