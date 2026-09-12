package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Communities invite-only pilot (2026-09-12): moderation controls.
//
// Member-facing (owner/admin, X-User-Id):
//   DELETE /:channelId/members/:userId            remove a member
//   POST   /:channelId/members/:userId/ban        ban
//   DELETE /:channelId/members/:userId/ban        unban
//   GET    /:channelId/subscribers?role=          roster with roles
//
// Internal (X-Internal-Service-Key, registered only when the key is set):
//   GET    /internal/channel-reports
//   POST   /internal/channel-reports/:reportId/review
//   POST   /internal/channels/:channelId/suspend
//   DELETE /internal/channels/:channelId/suspend

func parseTargetUserID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return uuid.Nil, false
	}
	return id, true
}

// RemoveMember — DELETE /:channelId/members/:userId (owner/admin).
func (h *Handler) RemoveMember(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	channelID, ok := parseChannelID(c)
	if !ok {
		return
	}
	targetID, ok := parseTargetUserID(c)
	if !ok {
		return
	}
	if err := h.svc.RemoveMember(c.Request.Context(), channelID, actorID, targetID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "removed"}, nil)
}

// BanMember — POST /:channelId/members/:userId/ban (owner/admin).
func (h *Handler) BanMember(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	channelID, ok := parseChannelID(c)
	if !ok {
		return
	}
	targetID, ok := parseTargetUserID(c)
	if !ok {
		return
	}
	if err := h.svc.BanMember(c.Request.Context(), channelID, actorID, targetID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "banned"}, nil)
}

// UnbanMember — DELETE /:channelId/members/:userId/ban (owner/admin).
func (h *Handler) UnbanMember(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	channelID, ok := parseChannelID(c)
	if !ok {
		return
	}
	targetID, ok := parseTargetUserID(c)
	if !ok {
		return
	}
	if err := h.svc.UnbanMember(c.Request.Context(), channelID, actorID, targetID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unbanned"}, nil)
}

// --- Internal moderation routes ---

// ReviewReportRequest is the body of POST /internal/channel-reports/:id/review.
type ReviewReportRequest struct {
	Status string `json:"status" binding:"required"`
	Note   string `json:"note"`
}

// SuspendChannelRequest carries an optional audit reason.
type SuspendChannelRequest struct {
	Reason string `json:"reason"`
}

// ListChannelReports — GET /internal/channel-reports?status=&limit=&cursor=.
func (h *Handler) ListChannelReports(c *gin.Context) {
	limit := 50
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "50")); err == nil && l > 0 && l <= 200 {
		limit = l
	}
	items, next, err := h.svc.ListReports(c.Request.Context(), c.Query("status"), limit, c.Query("cursor"))
	if err != nil {
		handleServiceError(c, err)
		return
	}
	var meta *api.Meta
	if next != "" {
		meta = &api.Meta{NextCursor: next}
	}
	api.JSON(c.Writer, http.StatusOK, items, meta)
}

// ReviewChannelReport — POST /internal/channel-reports/:reportId/review.
// The reviewer is the X-User-Id the moderation tool passes alongside the
// internal key, so the audit trail names a person and not just "internal".
func (h *Handler) ReviewChannelReport(c *gin.Context) {
	reviewerID, ok := getUserID(c)
	if !ok {
		return
	}
	reportID, err := uuid.Parse(c.Param("reportId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid report ID", nil)
		return
	}
	var req ReviewReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	report, err := h.svc.ReviewReport(c.Request.Context(), reportID, reviewerID, req.Status, req.Note)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, report, nil)
}

// SuspendChannel — POST /internal/channels/:channelId/suspend. Writes the
// `suspended` status, which removes the channel from /discover, GET /{id},
// /my and the fan-out roster.
func (h *Handler) SuspendChannel(c *gin.Context) {
	channelID, ok := parseChannelID(c)
	if !ok {
		return
	}
	var req SuspendChannelRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
			return
		}
	}
	if err := h.svc.SuspendChannel(c.Request.Context(), channelID, req.Reason); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "suspended"}, nil)
}

// UnsuspendChannel — DELETE /internal/channels/:channelId/suspend.
func (h *Handler) UnsuspendChannel(c *gin.Context) {
	channelID, ok := parseChannelID(c)
	if !ok {
		return
	}
	if err := h.svc.UnsuspendChannel(c.Request.Context(), channelID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "active"}, nil)
}
