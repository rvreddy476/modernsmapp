package http

import (
	"net/http"
	"time"

	"github.com/atpost/community-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
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

// CreateEvent: moderators and above of the path community (K-M2). The gate
// is in service.CreateEvent.
func (h *Handler) CreateEvent(c *gin.Context) {
	ids, ok := pathIDs(c, "communityId")
	if !ok {
		return
	}
	actorID, ok := getUserID(c)
	if !ok {
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
		Title:        req.Title,
		Description:  req.Description,
		Location:     req.Location,
		StartsAt:     startsAt,
		EndsAt:       endsAt,
		MaxAttendees: req.MaxAttendees,
	}

	if err := h.svc.CreateEvent(c.Request.Context(), ids[0], actorID, event); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, event, nil)
}

// ListEvents: gated and paged (K-M1).
func (h *Handler) ListEvents(c *gin.Context) {
	ids, ok := pathIDs(c, "communityId")
	if !ok {
		return
	}
	viewer, ok := viewerFromHeader(c)
	if !ok {
		return
	}
	limit, offset := parsePagination(c)
	events, err := h.svc.ListEvents(c.Request.Context(), ids[0], viewer, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	if events == nil {
		events = []store.CommunityEvent{}
	}

	api.JSON(c.Writer, http.StatusOK, events, nil)
}
