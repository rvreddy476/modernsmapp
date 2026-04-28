package http

import (
	"net/http"
	"time"

	"github.com/atpost/community-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type CreateEventRequest struct {
	Title        string  `json:"title" binding:"required"`
	Description  string  `json:"description"`
	Location     *string `json:"location"`
	StartsAt     string  `json:"starts_at" binding:"required"`
	EndsAt       *string `json:"ends_at"`
	IsOnline     bool    `json:"is_online"`
	MaxAttendees int     `json:"max_attendees"`
}

func (h *Handler) CreateEvent(c *gin.Context) {
	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid community id", nil)
		return
	}

	userID := c.GetHeader("X-User-ID")
	if userID == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil)
		return
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid user id", nil)
		return
	}

	var req CreateEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	startsAt, err := time.Parse(time.RFC3339, req.StartsAt)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_DATE", "starts_at must be RFC3339", nil)
		return
	}

	var endsAt *time.Time
	if req.EndsAt != nil {
		t, err := time.Parse(time.RFC3339, *req.EndsAt)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_DATE", "ends_at must be RFC3339", nil)
			return
		}
		endsAt = &t
	}

	event := &store.CommunityEvent{
		CommunityID:  communityID,
		CreatorID:    uid,
		Title:        req.Title,
		Description:  req.Description,
		Location:     req.Location,
		StartsAt:     startsAt,
		EndsAt:       endsAt,
		MaxAttendees: req.MaxAttendees,
	}

	if err := h.svc.Store().CreateEvent(c.Request.Context(), event); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "CREATE_FAILED", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, event, nil)
}

func (h *Handler) ListEvents(c *gin.Context) {
	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid community id", nil)
		return
	}

	events, err := h.svc.Store().ListEvents(c.Request.Context(), communityID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "LIST_FAILED", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, events, nil)
}
