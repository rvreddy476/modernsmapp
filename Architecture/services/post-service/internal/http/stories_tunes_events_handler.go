// stories_tunes_events_handler.go holds the smaller legacy surfaces: story
// delete, tunes (create, delete, get) and events (create, get, RSVP, RSVP
// list). Moved out of handler.go unchanged.

package http

import (
	"net/http"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ============================================================
// Story Handlers
// ============================================================

func (h *Handler) DeleteStory(c *gin.Context) {
	authorIDStr := c.GetHeader("X-User-Id")
	authorID, err := uuid.Parse(authorIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	storyIDStr := c.Param("storyId")
	storyID, err := uuid.Parse(storyIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid story ID", nil)
		return
	}

	if err := h.svc.DeleteStory(c.Request.Context(), storyID, authorID); err != nil {
		if err.Error() == "STORY_NOT_FOUND" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Story not found or not yours", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

// ============================================================
// Tune Handlers
// ============================================================

func (h *Handler) CreateTune(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	// The post detail's read gate (read_gate.go, 2026-09-29).
	if !h.requirePostReadable(c, postID, &userID) {
		return
	}
	if err := h.svc.CreateTune(c.Request.Context(), userID, postID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]bool{"ok": true}, nil)
}

func (h *Handler) DeleteTune(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	if err := h.svc.DeleteTune(c.Request.Context(), userID, postID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]bool{"ok": true}, nil)
}

func (h *Handler) GetTune(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	// The post detail's read gate (read_gate.go, 2026-09-29).
	if !h.requirePostReadable(c, postID, &userID) {
		return
	}
	tuned, err := h.svc.HasTune(c.Request.Context(), userID, postID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]bool{"tuned": tuned}, nil)
}

// ============================================================
// Event Handlers
// ============================================================

func (h *Handler) CreateEvent(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	var req struct {
		Title        string     `json:"title" binding:"required"`
		Description  string     `json:"description"`
		StartsAt     time.Time  `json:"starts_at" binding:"required"`
		EndsAt       *time.Time `json:"ends_at"`
		LocationName *string    `json:"location_name"`
		LocationLat  *float64   `json:"location_lat"`
		LocationLng  *float64   `json:"location_lng"`
		CoverMediaID *uuid.UUID `json:"cover_media_id"`
		IsTicketed   bool       `json:"is_ticketed"`
		TicketPrice  *float64   `json:"ticket_price"`
		MaxAttendees *int       `json:"max_attendees"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	event, err := h.svc.CreateEvent(c.Request.Context(), service.CreateEventInput{
		CreatorID:    userID,
		Title:        req.Title,
		Description:  req.Description,
		StartsAt:     req.StartsAt,
		EndsAt:       req.EndsAt,
		LocationName: req.LocationName,
		LocationLat:  req.LocationLat,
		LocationLng:  req.LocationLng,
		CoverMediaID: req.CoverMediaID,
		IsTicketed:   req.IsTicketed,
		TicketPrice:  req.TicketPrice,
		MaxAttendees: req.MaxAttendees,
	})
	if err != nil {
		// The cover is claimed with media-service (service/media_claims.go):
		// 403 / 422 / 503 for the cover, 400 for the rest as before.
		if writeOwnMediaError(c, err) {
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "CREATE_EVENT_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, event, nil)
}

func (h *Handler) GetEvent(c *gin.Context) {
	eventID, err := uuid.Parse(c.Param("eventId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid event ID", nil)
		return
	}
	event, err := h.svc.GetEvent(c.Request.Context(), eventID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, event, nil)
}

func (h *Handler) RSVPEvent(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	eventID, err := uuid.Parse(c.Param("eventId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid event ID", nil)
		return
	}
	var req struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	if err := h.svc.RSVPEvent(c.Request.Context(), eventID, userID, req.Status); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "RSVP_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]bool{"ok": true}, nil)
}

func (h *Handler) GetEventRSVPs(c *gin.Context) {
	eventID, err := uuid.Parse(c.Param("eventId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid event ID", nil)
		return
	}
	limit := 20
	offset := 0
	rsvps, err := h.svc.GetEventRSVPs(c.Request.Context(), eventID, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if rsvps == nil {
		rsvps = []postgres.EventRSVP{}
	}
	api.JSON(c.Writer, http.StatusOK, rsvps, nil)
}
