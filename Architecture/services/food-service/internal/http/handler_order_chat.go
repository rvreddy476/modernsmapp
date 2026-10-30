package http

import (
	"errors"
	"net/http"

	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Order chat (audit M3, 2026-10-30). The consumer routes are party-only:
// the customer, the restaurant owner, and the rider while inside the
// drop-off window. X-Scopes grants nothing here. Admins read and post only
// through the admin-service token family (registerAdminRoutes:
// GET/POST /orders/:orderId/messages), which names the signed actor.

// AppendOrderMessageRequest carries the message body.
type AppendOrderMessageRequest struct {
	Body string `json:"body"`
}

// writeChatError maps the chat and ticket refusals; false when err is not
// one of them.
func writeChatError(c *gin.Context, err error) bool {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrMessageBodyInvalid):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "FOOD_MESSAGE_BODY_INVALID", err.Error(),
			gin.H{"max_chars": service.MaxMessageBodyRunes})
	case errors.Is(err, service.ErrNotOrderParty):
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "FOOD_ORDER_CHAT_FORBIDDEN", err.Error(), nil)
	case errors.Is(err, service.ErrOrderMessageNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "FOOD_ORDER_MESSAGE_NOT_FOUND", err.Error(), nil)
	default:
		return false
	}
	return true
}

// bindMessageBody reads {"body": "..."}; a body that is not JSON is the
// same 400 as a blank one.
func bindMessageBody(c *gin.Context) (string, bool) {
	var req AppendOrderMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "body required", nil)
		return "", false
	}
	if err := service.ValidateMessageBody(req.Body); err != nil {
		writeChatError(c, err)
		return "", false
	}
	return req.Body, true
}

// PostOrderMessage — POST /v1/food/orders/:orderId/messages (a party), and
// POST <admin>/orders/:orderId/messages (admin-service token).
func (h *Handler) PostOrderMessage(c *gin.Context) { h.postOrderMessage(c, false) }

// AdminPostOrderMessage is the admin-token route; messages render with
// author_role=admin.
func (h *Handler) AdminPostOrderMessage(c *gin.Context) { h.postOrderMessage(c, true) }

func (h *Handler) postOrderMessage(c *gin.Context, isAdmin bool) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	orderID, err := uuid.Parse(c.Param("orderId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ORDER_ID", err.Error(), nil)
		return
	}
	body, ok := bindMessageBody(c)
	if !ok {
		return
	}
	m, err := h.svc.AppendOrderMessage(c.Request.Context(), orderID, uid, body, isAdmin)
	if err != nil {
		if writeChatError(c, err) {
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "MESSAGE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, m, nil)
}

// ListOrderMessages — GET /v1/food/orders/:orderId/messages (a party).
func (h *Handler) ListOrderMessages(c *gin.Context) { h.listOrderMessages(c, false) }

// AdminListOrderMessages — GET <admin>/orders/:orderId/messages.
func (h *Handler) AdminListOrderMessages(c *gin.Context) { h.listOrderMessages(c, true) }

func (h *Handler) listOrderMessages(c *gin.Context, isAdmin bool) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	orderID, err := uuid.Parse(c.Param("orderId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ORDER_ID", err.Error(), nil)
		return
	}
	rows, err := h.svc.ListOrderMessages(c.Request.Context(), orderID, uid, isAdmin)
	if err != nil {
		if writeChatError(c, err) {
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "LIST_MESSAGES_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"messages": rows}, nil)
}

// MarkOrderMessageReadRequest is the legacy body. role is ignored since
// audit M3: the server resolves the caller's role on the order.
type MarkOrderMessageReadRequest struct {
	Role string `json:"role"`
}

// MarkOrderMessageRead — POST /v1/food/orders/:orderId/messages/:msgId/read
//
// The message must belong to :orderId and the caller must be a party to that
// order (403 FOOD_ORDER_CHAT_FORBIDDEN; 404 FOOD_ORDER_MESSAGE_NOT_FOUND).
func (h *Handler) MarkOrderMessageRead(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	orderID, err := uuid.Parse(c.Param("orderId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ORDER_ID", err.Error(), nil)
		return
	}
	msgID, err := uuid.Parse(c.Param("msgId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_MSG_ID", err.Error(), nil)
		return
	}
	if err := h.svc.MarkOrderMessageRead(c.Request.Context(), orderID, msgID, uid); err != nil {
		if writeChatError(c, err) {
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "MARK_READ_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"msg_id": msgID.String(), "read": true}, nil)
}
