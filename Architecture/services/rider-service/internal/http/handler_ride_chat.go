package http

import (
	"net/http"
	"strings"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AppendRideMessageRequest carries the message body.
type AppendRideMessageRequest struct {
	Body string `json:"body"`
}

// PostRideMessage — POST /v1/rider/rides/:id/messages (party-only)
func (h *Handler) PostRideMessage(c *gin.Context) {
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
	var req AppendRideMessageRequest
	if err := c.BindJSON(&req); err != nil || strings.TrimSpace(req.Body) == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "body required", nil)
		return
	}
	isAdmin := hasAdminScope(c.GetHeader("X-Scopes"))
	m, err := h.svc.AppendRideMessage(c.Request.Context(), rideID, uid, req.Body, isAdmin)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "MESSAGE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, m, nil)
}

// ListRideMessages — GET /v1/rider/rides/:id/messages (party-only)
func (h *Handler) ListRideMessages(c *gin.Context) {
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
	isAdmin := hasAdminScope(c.GetHeader("X-Scopes"))
	rows, err := h.svc.ListRideMessages(c.Request.Context(), rideID, uid, isAdmin)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "LIST_MESSAGES_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"messages": rows}, nil)
}

// MarkRideMessageReadRequest carries the viewer role.
type MarkRideMessageReadRequest struct {
	Role string `json:"role"` // customer | partner | admin
}

// MarkRideMessageRead — POST /v1/rider/rides/:id/messages/:msgId/read
func (h *Handler) MarkRideMessageRead(c *gin.Context) {
	raw := c.GetHeader("X-User-Id")
	uid, err := uuid.Parse(raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user id", nil)
		return
	}
	msgID, err := uuid.Parse(c.Param("msgId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_MSG_ID", err.Error(), nil)
		return
	}
	var req MarkRideMessageReadRequest
	_ = c.BindJSON(&req)
	if req.Role == "" {
		req.Role = "customer"
	}
	if err := h.svc.MarkRideMessageRead(c.Request.Context(), msgID, uid, req.Role); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "MARK_READ_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"msg_id": msgID.String(), "read": true}, nil)
}

func hasAdminScope(scopes string) bool {
	for _, scope := range strings.Fields(scopes) {
		switch scope {
		case "admin", "superadmin", "moderator":
			return true
		}
	}
	return false
}
