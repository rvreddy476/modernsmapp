package http

import (
	"context"
	"net/http"

	"github.com/atpost/chat-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Chat parity fix pass 2 (lane M, 2026-10-18): mark unread and conversation
// reports.

// ParityFix2Service is the fix-pass-2 surface. *service.Service implements
// it; a separate interface so the earlier stubs keep satisfying
// ParityService unchanged.
type ParityFix2Service interface {
	SetConversationUnread(ctx context.Context, userID, convID uuid.UUID, unread bool) error
	ReportConversation(ctx context.Context, userID, convID uuid.UUID, reason, note string) error
}

func (h *Handler) parityFix2(c *gin.Context) (ParityFix2Service, bool) {
	ps, ok := h.svc.(ParityFix2Service)
	if !ok {
		api.Error(c.Writer, http.StatusNotImplemented, "NOT_IMPLEMENTED", "not available", nil, nil)
		return nil, false
	}
	return ps, true
}

// SetConversationUnreadRequest is PUT .../unread. Unread is a pointer so a
// body without it is a 400, never a silent "false".
type SetConversationUnreadRequest struct {
	Unread *bool `json:"unread" binding:"required"`
}

// SetConversationUnread — PUT /v1/chat/conversations/:id/unread → 204.
func (h *Handler) SetConversationUnread(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	var req SetConversationUnreadRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Unread == nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", `body must be {"unread": true|false}`, nil, nil)
		return
	}
	ps, ok := h.parityFix2(c)
	if !ok {
		return
	}
	if err := ps.SetConversationUnread(c.Request.Context(), userID, convID, *req.Unread); err != nil {
		if writeParityError(c, err) {
			return
		}
		h.internalError(c, "could not update the unread mark", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ReportConversationRequest is POST .../report.
type ReportConversationRequest struct {
	Reason string `json:"reason" binding:"required"`
	Note   string `json:"note" binding:"max=2000"`
}

// ReportConversation — POST /v1/chat/conversations/:id/report → 202.
func (h *Handler) ReportConversation(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	var req ReportConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}
	ps, ok := h.parityFix2(c)
	if !ok {
		return
	}
	if err := ps.ReportConversation(c.Request.Context(), userID, convID, req.Reason, req.Note); err != nil {
		if writeParityError(c, err) {
			return
		}
		h.internalError(c, "could not report the conversation", err)
		return
	}
	api.JSON(c.Writer, http.StatusAccepted, map[string]string{"status": "reported"}, nil)
}
