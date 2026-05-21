package http

import (
	"net/http"
	"strings"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AppendOrderMessageRequest carries the message body.
type AppendOrderMessageRequest struct {
	Body string `json:"body"`
}

// PostOrderMessage — POST /v1/food/orders/:orderId/messages
//
// Anyone party to the order may post (customer, restaurant owner,
// delivery partner). Admins can also post via X-Scopes override; their
// messages render with author_role=admin.
func (h *Handler) PostOrderMessage(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	orderID, err := uuid.Parse(c.Param("orderId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ORDER_ID", err.Error(), nil)
		return
	}
	var req AppendOrderMessageRequest
	if err := c.BindJSON(&req); err != nil || strings.TrimSpace(req.Body) == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "body required", nil)
		return
	}
	isAdmin := isAdminScope(c.GetHeader("X-Scopes"))
	m, err := h.svc.AppendOrderMessage(c.Request.Context(), orderID, uid, req.Body, isAdmin)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "MESSAGE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, m, nil)
}

// ListOrderMessages — GET /v1/food/orders/:orderId/messages
func (h *Handler) ListOrderMessages(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	orderID, err := uuid.Parse(c.Param("orderId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ORDER_ID", err.Error(), nil)
		return
	}
	isAdmin := isAdminScope(c.GetHeader("X-Scopes"))
	rows, err := h.svc.ListOrderMessages(c.Request.Context(), orderID, uid, isAdmin)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "LIST_MESSAGES_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"messages": rows}, nil)
}

// MarkOrderMessageReadRequest carries the role the viewer is acting as.
type MarkOrderMessageReadRequest struct {
	Role string `json:"role"` // customer | restaurant | delivery | admin
}

// MarkOrderMessageRead — POST /v1/food/orders/:orderId/messages/:msgId/read
func (h *Handler) MarkOrderMessageRead(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	msgID, err := uuid.Parse(c.Param("msgId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_MSG_ID", err.Error(), nil)
		return
	}
	var req MarkOrderMessageReadRequest
	_ = c.BindJSON(&req)
	if req.Role == "" {
		req.Role = "customer"
	}
	if err := h.svc.MarkOrderMessageRead(c.Request.Context(), msgID, uid, req.Role); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "MARK_READ_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"msg_id": msgID.String(), "read": true}, nil)
}
