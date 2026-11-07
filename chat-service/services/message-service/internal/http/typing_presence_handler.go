// Typing, read and presence routes: per-conversation presence, the
// typing indicator, marking a conversation read and batch presence.

package http

import (
	"errors"
	"io"
	"net/http"

	"github.com/atpost/chat-message-service/internal/service"
	"github.com/atpost/chat-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// --- Typing & Read Receipts ---

func (h *Handler) GetConversationPresence(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	resp, err := h.svc.GetConversationPresence(c.Request.Context(), userID, convID)
	if err != nil {
		if errors.Is(err, service.ErrNotMember) {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "not a member of this conversation", nil, nil)
			return
		}
		h.log.Warn("failed to read presence", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to read presence", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

func (h *Handler) SetTyping(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	// CONTRACT §4: the body may carry {"is_typing": false} to stop. An
	// empty or absent body keeps the original meaning (started typing).
	var body struct {
		IsTyping *bool `json:"is_typing"`
	}
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&body); err != nil && !errors.Is(err, io.EOF) {
			api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
			return
		}
	}
	var err error
	if ps, ok := h.svc.(ParityService); ok && body.IsTyping != nil {
		err = ps.SetTypingState(c.Request.Context(), userID, convID, *body.IsTyping)
	} else {
		err = h.svc.SetTyping(c.Request.Context(), userID, convID)
	}
	if err != nil {
		h.writeServiceError(c, "failed to set typing", err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"}, nil)
}

type MarkReadRequest struct {
	MessageID string `json:"message_id" binding:"required"`
}

func (h *Handler) MarkRead(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	var req MarkReadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	if err := h.svc.MarkRead(c.Request.Context(), userID, convID, req.MessageID); err != nil {
		h.writeServiceError(c, "failed to mark read", err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"}, nil)
}

// --- Presence ---

type GetPresenceRequest struct {
	UserIDs []string `json:"user_ids" binding:"required"`
}

func (h *Handler) GetPresence(c *gin.Context) {
	requesterID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	var req GetPresenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	if len(req.UserIDs) > 200 {
		api.Error(c.Writer, http.StatusBadRequest, "TOO_MANY_IDS", "Maximum 200 user IDs per request", nil, nil)
		return
	}

	userIDs := make([]uuid.UUID, 0, len(req.UserIDs))
	for _, raw := range req.UserIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			continue
		}
		userIDs = append(userIDs, id)
	}

	presence, err := h.svc.GetPresence(c.Request.Context(), requesterID, userIDs)
	if err != nil {
		h.log.Error("failed to get presence", "err", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL", "Failed to check presence", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, presence, nil)
}
