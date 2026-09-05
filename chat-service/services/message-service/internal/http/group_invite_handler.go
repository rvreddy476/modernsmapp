package http

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/chat-message-service/internal/service"
	store "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-shared/api"
	"github.com/gin-gonic/gin"
)

// Chat-app pass (2026-09-05): group invite links.

// CreateInviteLinkRequest — both fields optional. expires_in_seconds <= 0
// (or absent) means the 7-day default; max_uses 0/absent means unlimited.
type CreateInviteLinkRequest struct {
	ExpiresInSeconds int64 `json:"expires_in_seconds"`
	MaxUses          *int  `json:"max_uses"`
}

// writeGroupError maps governance errors shared by the group endpoints.
// Returns true when handled.
func writeGroupError(c *gin.Context, err error) bool {
	switch {
	case errors.Is(err, service.ErrNotPermittedRole):
		api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
	case errors.Is(err, service.ErrRateLimited):
		api.Error(c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", err.Error(), nil, nil)
	case errors.Is(err, service.ErrInviteLinkNotFound):
		api.Error(c.Writer, http.StatusNotFound, "INVITE_NOT_FOUND", err.Error(), nil, nil)
	case errors.Is(err, service.ErrInviteLinkNotLive):
		api.Error(c.Writer, http.StatusGone, "INVITE_NOT_LIVE", err.Error(), nil, nil)
	case errors.Is(err, service.ErrInviteJoinBlocked):
		api.Error(c.Writer, http.StatusForbidden, "JOIN_NOT_ALLOWED", err.Error(), nil, nil)
	case errors.Is(err, store.ErrGroupFull):
		api.Error(c.Writer, http.StatusConflict, "GROUP_FULL", err.Error(), nil, nil)
	case errors.Is(err, service.ErrMediaNotAllowed):
		api.Error(c.Writer, http.StatusUnprocessableEntity, "MEDIA_NOT_ALLOWED", err.Error(), nil, nil)
	case err != nil && (strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "not a conversation member")):
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil, nil)
	default:
		return false
	}
	return true
}

// CreateInviteLink — POST /v1/chat/conversations/:id/invite-link (owner/admin).
func (h *Handler) CreateInviteLink(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	var req CreateInviteLinkRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
			return
		}
	}
	link, err := h.svc.CreateInviteLink(c.Request.Context(), userID, convID, time.Duration(req.ExpiresInSeconds)*time.Second, req.MaxUses)
	if err != nil {
		h.log.Warn("failed to create invite link", "err", err, "request_id", RequestIDFromContext(c))
		if writeGroupError(c, err) {
			return
		}
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, link, nil)
}

// GetInviteLink — GET /v1/chat/conversations/:id/invite-link (owner/admin).
func (h *Handler) GetInviteLink(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	link, err := h.svc.GetInviteLink(c.Request.Context(), userID, convID)
	if err != nil {
		if writeGroupError(c, err) {
			return
		}
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, link, nil)
}

// RevokeInviteLink — DELETE /v1/chat/conversations/:id/invite-link (owner/admin).
func (h *Handler) RevokeInviteLink(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	if err := h.svc.RevokeInviteLink(c.Request.Context(), userID, convID); err != nil {
		if writeGroupError(c, err) {
			return
		}
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "revoked"}, nil)
}

func parseInviteCode(c *gin.Context) (string, bool) {
	code := strings.ToUpper(strings.TrimSpace(c.Param("code")))
	if !service.ValidInviteCode(code) {
		api.Error(c.Writer, http.StatusNotFound, "INVITE_NOT_FOUND", "invite link not found", nil, nil)
		return "", false
	}
	return code, true
}

// PreviewInvite — GET /v1/chat/invites/:code (any authenticated user).
func (h *Handler) PreviewInvite(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	code, ok := parseInviteCode(c)
	if !ok {
		return
	}
	preview, err := h.svc.PreviewInvite(c.Request.Context(), userID, code)
	if err != nil {
		if writeGroupError(c, err) {
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load invite", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, preview, nil)
}

// JoinByInvite — POST /v1/chat/invites/:code/join (any authenticated user).
func (h *Handler) JoinByInvite(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	code, ok := parseInviteCode(c)
	if !ok {
		return
	}
	conv, err := h.svc.JoinByInvite(c.Request.Context(), userID, code)
	if err != nil {
		h.log.Warn("invite join failed", "err", err, "request_id", RequestIDFromContext(c))
		if writeGroupError(c, err) {
			return
		}
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, conv, nil)
}
