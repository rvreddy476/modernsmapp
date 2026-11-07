package http

import (
	"net/http"

	"github.com/atpost/group-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ── Group Events ─────────────────────────────────────────────

func (h *Handler) CreateGroupEvent(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil)
		return
	}
	var event store.GroupEvent
	if err := c.ShouldBindJSON(&event); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid request body", nil)
		return
	}
	if err := h.svc.CreateGroupEvent(c.Request.Context(), actorID, groupID, &event); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, event, nil)
}

func (h *Handler) ListGroupEvents(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil)
		return
	}
	limit, offset := parsePagination(c)
	events, err := h.svc.ListGroupEvents(c.Request.Context(), actorID, groupID, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, events, nil)
}

func (h *Handler) GetGroupEvent(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil)
		return
	}
	eventID, err := uuid.Parse(c.Param("eventId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid event id", nil)
		return
	}
	event, err := h.svc.GetGroupEvent(c.Request.Context(), actorID, groupID, eventID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, event, nil)
}

func (h *Handler) DeleteGroupEvent(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil)
		return
	}
	eventID, err := uuid.Parse(c.Param("eventId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid event id", nil)
		return
	}
	if err := h.svc.DeleteGroupEvent(c.Request.Context(), actorID, groupID, eventID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) RSVPGroupEvent(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil)
		return
	}
	eventID, err := uuid.Parse(c.Param("eventId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid event id", nil)
		return
	}
	var req struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "status is required", nil)
		return
	}
	if err := h.svc.RSVPGroupEvent(c.Request.Context(), actorID, groupID, eventID, req.Status); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}
