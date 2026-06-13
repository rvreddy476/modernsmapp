package http

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// CreateTicketRequest is the customer body.
type CreateTicketRequest struct {
	OrderID  *uuid.UUID `json:"order_id,omitempty"`
	Category string     `json:"category"`
	Subject  string     `json:"subject"`
	Detail   string     `json:"detail,omitempty"`
}

// CreateTicket — POST /v1/food/support/tickets
func (h *Handler) CreateTicket(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	var req CreateTicketRequest
	if err := c.BindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if req.Subject == "" || req.Category == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "subject + category required", nil)
		return
	}
	t, err := h.svc.CreateTicket(c.Request.Context(), postgres.CreateTicketInput{
		CustomerID: uid,
		OrderID:    req.OrderID,
		Category:   req.Category,
		Subject:    req.Subject,
		Detail:     req.Detail,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "CREATE_TICKET_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, t, nil)
}

// ListMyTickets — GET /v1/food/support/tickets/me
func (h *Handler) ListMyTickets(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	rows, err := h.svc.ListMyTickets(c.Request.Context(), uid)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "LIST_TICKETS_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"tickets": rows}, nil)
}

// GetTicket — GET /v1/food/support/tickets/:ticketId
func (h *Handler) GetTicket(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	tid, err := uuid.Parse(c.Param("ticketId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_TICKET_ID", err.Error(), nil)
		return
	}
	isAdmin := isAdminScope(c.GetHeader("X-Scopes"))
	t, msgs, err := h.svc.GetTicket(c.Request.Context(), uid, tid, isAdmin)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "TICKET_VIEW_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ticket": t, "messages": msgs}, nil)
}

// AppendMessageRequest carries one new message.
type AppendMessageRequest struct {
	Body string `json:"body"`
}

// AppendTicketMessage — POST /v1/food/support/tickets/:ticketId/messages
func (h *Handler) AppendTicketMessage(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	tid, err := uuid.Parse(c.Param("ticketId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_TICKET_ID", err.Error(), nil)
		return
	}
	var req AppendMessageRequest
	if err := c.BindJSON(&req); err != nil || strings.TrimSpace(req.Body) == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "body required", nil)
		return
	}
	isAdmin := isAdminScope(c.GetHeader("X-Scopes"))
	m, err := h.svc.AppendTicketMessage(c.Request.Context(), tid, uid, isAdmin, req.Body)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "TICKET_APPEND_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, m, nil)
}

// AdminListTickets — GET /v1/food/admin/support/tickets?status=&limit=
func (h *Handler) AdminListTickets(c *gin.Context) {
	status := c.Query("status")
	limit := 50
	if v, _ := strconv.Atoi(c.Query("limit")); v > 0 {
		limit = v
	}
	rows, err := h.svc.AdminListTickets(c.Request.Context(), status, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "TICKET_LIST_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"tickets": rows}, nil)
}

// AdminListRefunds — GET /v1/food/admin/refunds?status=&limit=
func (h *Handler) AdminListRefunds(c *gin.Context) {
	status := c.Query("status")
	limit := 50
	if v, _ := strconv.Atoi(c.Query("limit")); v > 0 {
		limit = v
	}
	rows, err := h.svc.AdminListRefunds(c.Request.Context(), status, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "REFUND_LIST_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"refunds": rows}, nil)
}

// AdminSetTicketStatusRequest is the admin status transition.
type AdminSetTicketStatusRequest struct {
	Status string `json:"status"`
}

// AdminSetTicketStatus — POST /v1/food/admin/support/tickets/:ticketId/status
func (h *Handler) AdminSetTicketStatus(c *gin.Context) {
	tid, err := uuid.Parse(c.Param("ticketId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_TICKET_ID", err.Error(), nil)
		return
	}
	var req AdminSetTicketStatusRequest
	if err := c.BindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.SetTicketStatus(c.Request.Context(), tid, req.Status); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "TICKET_STATUS_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ticket_id": tid.String(), "status": req.Status}, nil)
}

// RefundRequest body.
type CustomerRefundRequest struct {
	OrderID  uuid.UUID  `json:"order_id"`
	TicketID *uuid.UUID `json:"ticket_id,omitempty"`
	Amount   float64    `json:"amount"`
	Reason   string     `json:"reason,omitempty"`
}

// CreateRefundRequest — POST /v1/food/refunds
func (h *Handler) CreateRefundRequest(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	var req CustomerRefundRequest
	if err := c.BindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if req.Amount <= 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "amount must be > 0", nil)
		return
	}
	r, err := h.svc.CreateRefundRequest(c.Request.Context(), uid, req.OrderID, req.TicketID, req.Amount, req.Reason)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "REFUND_CREATE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, r, nil)
}

// AdminDecideRefundRequest body.
type AdminDecideRefundRequest struct {
	Status string `json:"status"` // approved | rejected
	Reason string `json:"reason,omitempty"`
}

// AdminDecideRefund — POST /v1/food/admin/refunds/:refundId/decide
func (h *Handler) AdminDecideRefund(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	rid, err := uuid.Parse(c.Param("refundId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REFUND_ID", err.Error(), nil)
		return
	}
	var req AdminDecideRefundRequest
	if err := c.BindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.DecideRefund(c.Request.Context(), uid, rid, req.Status, req.Reason); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "REFUND_DECIDE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"refund_id": rid.String(), "status": req.Status}, nil)
}

func isAdminScope(scopes string) bool {
	return hasAnyScope(scopes, "admin", "superadmin", "moderator")
}
