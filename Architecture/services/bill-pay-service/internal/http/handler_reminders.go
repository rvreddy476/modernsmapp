package http

import (
	"net/http"

	"github.com/atpost/bill-pay-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// ListReminders handles GET /v1/billpay/reminders.
func (h *Handler) ListReminders(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.ListReminders(c.Request.Context(), uid)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "REMINDERS_ERROR")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// CreateReminder handles POST /v1/billpay/reminders.
func (h *Handler) CreateReminder(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	var req service.CreateReminderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	r, err := h.svc.CreateReminder(c.Request.Context(), uid, req)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "REMINDER_CREATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, r)
}

// DeleteReminder handles DELETE /v1/billpay/reminders/:id.
func (h *Handler) DeleteReminder(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.DeleteReminder(c.Request.Context(), uid, id); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "REMINDER_DELETE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"deleted": true})
}
