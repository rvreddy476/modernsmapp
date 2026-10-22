package http

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/atpost/chat-message-service/internal/service"
	"github.com/atpost/chat-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Delivered ticks (founder, 22 Oct 2026; service/receipt_ticks.go).

// TicksService is the delivered-ack surface (type-asserted like the parity
// routes so test doubles of ChatService need not implement it).
type TicksService interface {
	MarkDelivered(ctx context.Context, userID, convID uuid.UUID, messageID *uuid.UUID, ts *time.Time) (*service.DeliveredAck, error)
}

// MarkDeliveredRequest is POST /conversations/:id/delivered. Both fields
// are optional: ts is the server ts of the newest message the device has
// (preferred — nothing sent after it is marked delivered); without it the
// ack covers everything up to now.
type MarkDeliveredRequest struct {
	UpToMessageID string     `json:"up_to_message_id"`
	Ts            *time.Time `json:"ts"`
}

// MarkDelivered acknowledges that the caller's device received the
// conversation's messages up to the given one.
func (h *Handler) MarkDelivered(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	ts, ok := h.svc.(TicksService)
	if !ok {
		api.Error(c.Writer, http.StatusNotImplemented, "NOT_IMPLEMENTED", "not available", nil, nil)
		return
	}
	var req MarkDeliveredRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid delivered ack", nil, nil)
			return
		}
	}
	var msgID *uuid.UUID
	if req.UpToMessageID != "" {
		id, err := uuid.Parse(req.UpToMessageID)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "invalid up_to_message_id", nil, nil)
			return
		}
		msgID = &id
	}
	ack, err := ts.MarkDelivered(c.Request.Context(), userID, convID, msgID, req.Ts)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNotMember):
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "not found", nil, nil)
		case errors.Is(err, service.ErrRateLimited):
			api.Error(c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", err.Error(), nil, nil)
		default:
			h.internalError(c, "could not record delivery", err)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, ack, nil)
}
