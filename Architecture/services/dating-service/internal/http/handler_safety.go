// HTTP handlers for /v1/dating/safety (spec §15 safety center).
//
// CRITICAL RULES #6: every error path is explicit. Panic + report MUST
// persist before responding 200; the service layer enforces this — this
// file just maps service errors to status codes.
package http

import (
	"net/http"
	"time"

	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// PostPanic — POST /v1/dating/safety/panic.
func (h *Handler) PostPanic(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body service.PanicRequest
	// Body is optional — a panic with no metadata still fires.
	_ = c.ShouldBindJSON(&body)
	if err := h.svc.RecordPanic(c.Request.Context(), userID, body); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PANIC_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"recorded": true}, nil)
}

// shareLocationRequest is the body for POST /v1/dating/safety/share-location.
type shareLocationRequest struct {
	ContactID       string   `json:"contact_id"`
	DurationMinutes int      `json:"duration_minutes"`
	Latitude        *float64 `json:"latitude,omitempty"`
	Longitude       *float64 `json:"longitude,omitempty"`
}

// PostShareLocation — POST /v1/dating/safety/share-location.
func (h *Handler) PostShareLocation(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body shareLocationRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	contact, err := parseUUIDValue(body.ContactID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid contact_id", nil)
		return
	}
	out, err := h.svc.ShareLocation(c.Request.Context(), userID, service.LocationShareRequest{
		ContactID:       contact,
		DurationMinutes: body.DurationMinutes,
		Latitude:        body.Latitude,
		Longitude:       body.Longitude,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SHARE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// scheduleMeetRequest is the body for POST /v1/dating/safety/meet.
type scheduleMeetRequest struct {
	WithUserID string    `json:"with_user_id"`
	When       time.Time `json:"when"`
	Latitude   float64   `json:"latitude"`
	Longitude  float64   `json:"longitude"`
	Venue      string    `json:"venue"`
}

// PostScheduleMeet — POST /v1/dating/safety/meet.
func (h *Handler) PostScheduleMeet(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body scheduleMeetRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	with, err := parseUUIDValue(body.WithUserID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid with_user_id", nil)
		return
	}
	out, err := h.svc.ScheduleMeet(c.Request.Context(), userID, service.MeetRequest{
		WithUserID: with,
		When:       body.When,
		Latitude:   body.Latitude,
		Longitude:  body.Longitude,
		Venue:      body.Venue,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "MEET_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusCreated, out, nil)
}

// meetCheckInRequest — body for POST /v1/dating/safety/meet/:id/check-in.
type meetCheckInRequest struct {
	Status string `json:"status"`
}

// PostMeetCheckIn — POST /v1/dating/safety/meet/:id/check-in.
func (h *Handler) PostMeetCheckIn(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	meetID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	var body meetCheckInRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.MeetCheckIn(c.Request.Context(), meetID, userID, body.Status); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "CHECKIN_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": body.Status}, nil)
}

// blockRequest — body for POST /v1/dating/safety/block.
type blockRequest struct {
	TargetUserID string `json:"target_user_id"`
}

// PostBlock — POST /v1/dating/safety/block.
func (h *Handler) PostBlock(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body blockRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	target, err := parseUUIDValue(body.TargetUserID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid target_user_id", nil)
		return
	}
	if err := h.svc.Block(c.Request.Context(), userID, target); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "BLOCK_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"blocked": true}, nil)
}

// reportRequest — body for POST /v1/dating/safety/report.
type reportRequest struct {
	TargetID string `json:"target_id"`
	Category string `json:"category"`
	Details  string `json:"details"`
}

// PostReport — POST /v1/dating/safety/report.
func (h *Handler) PostReport(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body reportRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	target, err := parseUUIDValue(body.TargetID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid target_id", nil)
		return
	}
	out, err := h.svc.Report(c.Request.Context(), userID, service.ReportRequest{
		TargetID: target,
		Category: body.Category,
		Details:  body.Details,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "REPORT_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusCreated, out, nil)
}

