package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Chat-app pass (2026-09-05): community endpoints — admins, one-emoji
// reactions, reports, unmute, single update.

type AdminRequest struct {
	UserID uuid.UUID `json:"user_id" binding:"required"`
}

type ReactionRequest struct {
	Emoji string `json:"emoji" binding:"required"`
}

type ReportRequest struct {
	Reason  string `json:"reason" binding:"required"`
	Details string `json:"details"`
}

func parseChannelID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil)
		return uuid.Nil, false
	}
	return id, true
}

func parseUpdateID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("updateId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid update ID", nil)
		return uuid.Nil, false
	}
	return id, true
}

func optionalViewer(c *gin.Context) *uuid.UUID {
	if uid, err := uuid.Parse(c.GetHeader("X-User-Id")); err == nil {
		return &uid
	}
	return nil
}

// UnmuteChannel — DELETE /:channelId/subscribe/mute.
func (h *Handler) UnmuteChannel(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	channelID, ok := parseChannelID(c)
	if !ok {
		return
	}
	if err := h.svc.Unmute(c.Request.Context(), channelID, actorID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unmuted"}, nil)
}

// GetUpdate — GET /:channelId/updates/:updateId.
func (h *Handler) GetUpdate(c *gin.Context) {
	channelID, ok := parseChannelID(c)
	if !ok {
		return
	}
	updateID, ok := parseUpdateID(c)
	if !ok {
		return
	}
	view, err := h.svc.GetUpdate(c.Request.Context(), channelID, updateID, optionalViewer(c))
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, view, nil)
}

// ListAdmins — GET /:channelId/admins.
func (h *Handler) ListAdmins(c *gin.Context) {
	channelID, ok := parseChannelID(c)
	if !ok {
		return
	}
	admins, err := h.svc.ListAdmins(c.Request.Context(), channelID, optionalViewer(c))
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, admins, nil)
}

// AddAdmin — POST /:channelId/admins {user_id} (owner only).
func (h *Handler) AddAdmin(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	channelID, ok := parseChannelID(c)
	if !ok {
		return
	}
	var req AdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	if err := h.svc.AddAdmin(c.Request.Context(), channelID, actorID, req.UserID); err != nil {
		handleServiceError(c, err)
		return
	}
	admins, err := h.svc.ListAdmins(c.Request.Context(), channelID, &actorID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, admins, nil)
}

// RemoveAdmin — DELETE /:channelId/admins/:userId (owner only).
func (h *Handler) RemoveAdmin(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	channelID, ok := parseChannelID(c)
	if !ok {
		return
	}
	targetID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}
	if err := h.svc.RemoveAdmin(c.Request.Context(), channelID, actorID, targetID); err != nil {
		handleServiceError(c, err)
		return
	}
	admins, err := h.svc.ListAdmins(c.Request.Context(), channelID, &actorID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, admins, nil)
}

// ReactToUpdate — PUT /:channelId/updates/:updateId/reaction {emoji}.
func (h *Handler) ReactToUpdate(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	channelID, ok := parseChannelID(c)
	if !ok {
		return
	}
	updateID, ok := parseUpdateID(c)
	if !ok {
		return
	}
	var req ReactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	view, err := h.svc.ReactToUpdate(c.Request.Context(), channelID, updateID, userID, req.Emoji)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, view, nil)
}

// UnreactToUpdate — DELETE /:channelId/updates/:updateId/reaction.
func (h *Handler) UnreactToUpdate(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	channelID, ok := parseChannelID(c)
	if !ok {
		return
	}
	updateID, ok := parseUpdateID(c)
	if !ok {
		return
	}
	view, err := h.svc.UnreactToUpdate(c.Request.Context(), channelID, updateID, userID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, view, nil)
}

// GetReactions — GET /:channelId/updates/:updateId/reactions.
func (h *Handler) GetReactions(c *gin.Context) {
	channelID, ok := parseChannelID(c)
	if !ok {
		return
	}
	updateID, ok := parseUpdateID(c)
	if !ok {
		return
	}
	view, err := h.svc.GetUpdate(c.Request.Context(), channelID, updateID, optionalViewer(c))
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"update_id":       view.ID,
		"reaction_count":  view.ReactionCount,
		"reactions":       view.Reactions,
		"viewer_reaction": view.ViewerReaction,
	}, nil)
}

// ReportChannel — POST /:channelId/report {reason, details?}.
func (h *Handler) ReportChannel(c *gin.Context) {
	h.report(c, false)
}

// ReportUpdate — POST /:channelId/updates/:updateId/report {reason, details?}.
func (h *Handler) ReportUpdate(c *gin.Context) {
	h.report(c, true)
}

func (h *Handler) report(c *gin.Context, onUpdate bool) {
	reporterID, ok := getUserID(c)
	if !ok {
		return
	}
	channelID, ok := parseChannelID(c)
	if !ok {
		return
	}
	var updateID *uuid.UUID
	if onUpdate {
		id, ok := parseUpdateID(c)
		if !ok {
			return
		}
		updateID = &id
	}
	var req ReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	report, err := h.svc.Report(c.Request.Context(), channelID, updateID, reporterID, req.Reason, req.Details)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusAccepted, report, nil)
}
