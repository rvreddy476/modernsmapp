package http

// Stream moderation v2 routes (1 Oct 2026), behind the internal key like
// every /v1/livestream route; the actor is the gateway's X-User-Id.

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/service"
	"github.com/atpost/shared/api"
)

// RemoveChatMessage — DELETE /v1/livestream/streams/:id/chat/:messageId
// (host or stream moderator). The message is hidden for everyone and
// chat.removed is published. 200 {"data":{"status":"removed","message_id"}}.
func (h *Handler) RemoveChatMessage(c *gin.Context) {
	actorID, ok := requireUserID(c)
	if !ok {
		return
	}
	streamID, ok := requireUUID(c, "id")
	if !ok {
		return
	}
	msgID, ok := requireUUID(c, "messageId")
	if !ok {
		return
	}
	if err := h.svc.RemoveChatMessage(c.Request.Context(), streamID, actorID, msgID); err != nil {
		writeModerationErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{"status": "removed", "message_id": msgID}, nil)
}

type banRequest struct {
	UserID uuid.UUID `json:"user_id" binding:"required"`
	Reason string    `json:"reason"`
}

// BanUser — POST /v1/livestream/streams/:id/bans {user_id, reason}
// (host or moderator). 200 {"data":{"status":"banned","user_id"}}.
func (h *Handler) BanUser(c *gin.Context) {
	actorID, ok := requireUserID(c)
	if !ok {
		return
	}
	streamID, ok := requireUUID(c, "id")
	if !ok {
		return
	}
	var body banRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.Ban(c.Request.Context(), streamID, actorID, body.UserID, body.Reason); err != nil {
		writeModerationErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{"status": "banned", "user_id": body.UserID}, nil)
}

// UnbanUser — DELETE /v1/livestream/streams/:id/bans/:userId (host or
// moderator). 200 {"data":{"status":"unbanned","user_id"}}.
func (h *Handler) UnbanUser(c *gin.Context) {
	actorID, ok := requireUserID(c)
	if !ok {
		return
	}
	streamID, ok := requireUUID(c, "id")
	if !ok {
		return
	}
	targetID, ok := requireUUID(c, "userId")
	if !ok {
		return
	}
	if err := h.svc.Unban(c.Request.Context(), streamID, actorID, targetID); err != nil {
		writeModerationErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{"status": "unbanned", "user_id": targetID}, nil)
}

// ListBans — GET /v1/livestream/streams/:id/bans (host or moderator).
// 200 {"data":[{stream_id,user_id,banned_by,reason,created_at}]}.
func (h *Handler) ListBans(c *gin.Context) {
	actorID, ok := requireUserID(c)
	if !ok {
		return
	}
	streamID, ok := requireUUID(c, "id")
	if !ok {
		return
	}
	bans, err := h.svc.ListBans(c.Request.Context(), streamID, actorID)
	if err != nil {
		writeModerationErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, bans, nil)
}

type moderatorsRequest struct {
	UserIDs []uuid.UUID `json:"user_ids"`
}

// SetModerators — PUT /v1/livestream/streams/:id/moderators {user_ids}
// (host only, at most 5). 200 {"data":{"user_ids":[...]}}.
func (h *Handler) SetModerators(c *gin.Context) {
	hostID, ok := requireUserID(c)
	if !ok {
		return
	}
	streamID, ok := requireUUID(c, "id")
	if !ok {
		return
	}
	var body moderatorsRequest
	if err := c.ShouldBindJSON(&body); err != nil || body.UserIDs == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "user_ids (array) is required", nil)
		return
	}
	ids, err := h.svc.SetModerators(c.Request.Context(), streamID, hostID, body.UserIDs)
	if err != nil {
		writeModerationErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{"user_ids": ids}, nil)
}

// ListModerators — GET /v1/livestream/streams/:id/moderators (anyone who
// can see the stream). 200 {"data":{"user_ids":[...]}}.
func (h *Handler) ListModerators(c *gin.Context) {
	streamID, ok := requireUUID(c, "id")
	if !ok {
		return
	}
	ids, err := h.svc.ListModerators(c.Request.Context(), streamID, optionalUserID(c))
	if err != nil {
		writeModerationErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{"user_ids": ids}, nil)
}

type reportRequest struct {
	Reason    string     `json:"reason" binding:"required"`
	MessageID *uuid.UUID `json:"message_id"`
	Note      string     `json:"note"`
}

// ReportStream — POST /v1/livestream/streams/:id/reports
// {reason: spam|harassment|hate|nudity|violence|scam|other, message_id?, note?}
// 201 {"data": <report>}; 409 ALREADY_REPORTED; 429 RATE_LIMITED.
func (h *Handler) ReportStream(c *gin.Context) {
	reporterID, ok := requireUserID(c)
	if !ok {
		return
	}
	streamID, ok := requireUUID(c, "id")
	if !ok {
		return
	}
	var body reportRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	rep, err := h.svc.Report(c.Request.Context(), streamID, reporterID, service.ReportInput{
		Reason: body.Reason, MessageID: body.MessageID, Note: body.Note,
	})
	if err != nil {
		writeModerationErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, rep, nil)
}
