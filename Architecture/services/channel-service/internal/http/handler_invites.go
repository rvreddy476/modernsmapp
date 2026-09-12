package http

import (
	"net/http"
	"strings"
	"time"

	"github.com/atpost/channel-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// Communities invite-only pilot (2026-09-12): invite links for broadcast
// channels. Route shape and error vocabulary mirror the group links in
// chat-service (message-service/internal/http/group_invite_handler.go).

// CreateInviteRequest — both fields optional. expires_in_seconds <= 0 (or
// absent) means the 7-day default; max_uses 0/absent means unlimited.
type CreateInviteRequest struct {
	ExpiresInSeconds int64 `json:"expires_in_seconds"`
	MaxUses          *int  `json:"max_uses"`
}

func parseInviteCode(c *gin.Context) (string, bool) {
	code := strings.ToUpper(strings.TrimSpace(c.Param("code")))
	if !service.ValidInviteCode(code) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "INVITE_NOT_FOUND", "invite link not found", nil)
		return "", false
	}
	return code, true
}

// CreateInvite — POST /:channelId/invite-link (owner/admin). Rotates any
// live invite, so a leaked link is replaced with one call.
func (h *Handler) CreateInvite(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	channelID, ok := parseChannelID(c)
	if !ok {
		return
	}
	var req CreateInviteRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
			return
		}
	}
	inv, err := h.svc.CreateInvite(c.Request.Context(), channelID, actorID,
		time.Duration(req.ExpiresInSeconds)*time.Second, req.MaxUses)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, inv, nil)
}

// GetInvite — GET /:channelId/invite-link (owner/admin).
func (h *Handler) GetInvite(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	channelID, ok := parseChannelID(c)
	if !ok {
		return
	}
	inv, err := h.svc.GetInvite(c.Request.Context(), channelID, actorID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, inv, nil)
}

// RevokeInvite — DELETE /:channelId/invite-link (owner/admin). Idempotent.
func (h *Handler) RevokeInvite(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	channelID, ok := parseChannelID(c)
	if !ok {
		return
	}
	if err := h.svc.RevokeInvite(c.Request.Context(), channelID, actorID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "revoked"}, nil)
}

// PreviewInvite — GET /invites/:code. Unauthenticated-safe: an X-User-Id is
// optional and only decides is_member. Discloses name, description, avatar,
// member count and is_member, and nothing else about a private community.
func (h *Handler) PreviewInvite(c *gin.Context) {
	code, ok := parseInviteCode(c)
	if !ok {
		return
	}
	preview, err := h.svc.PreviewInvite(c.Request.Context(), code, optionalViewer(c))
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, preview, nil)
}

// JoinByInvite — POST /invites/:code/join. Consumes one use and subscribes
// the caller. Idempotent for an existing member.
func (h *Handler) JoinByInvite(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	code, ok := parseInviteCode(c)
	if !ok {
		return
	}
	ch, err := h.svc.JoinByInvite(c.Request.Context(), code, userID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, ch, nil)
}
