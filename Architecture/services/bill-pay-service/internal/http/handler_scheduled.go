package http

import (
	"net/http"

	"github.com/atpost/bill-pay-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// ListScheduled handles GET /v1/billpay/scheduled.
func (h *Handler) ListScheduled(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.ListScheduled(c.Request.Context(), uid)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SCHEDULED_ERROR")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// CreateScheduled handles POST /v1/billpay/scheduled.
func (h *Handler) CreateScheduled(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	var req service.CreateScheduledRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.CreateScheduled(c.Request.Context(), uid, req)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SCHEDULED_CREATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, out)
}

// PatchScheduled handles PATCH /v1/billpay/scheduled/:id.
func (h *Handler) PatchScheduled(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var req service.UpdateScheduledActiveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if req.IsActive == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "is_active required", nil)
		return
	}
	out, err := h.svc.UpdateScheduledActive(c.Request.Context(), uid, id, *req.IsActive)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SCHEDULED_UPDATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// DeleteScheduled handles DELETE /v1/billpay/scheduled/:id.
func (h *Handler) DeleteScheduled(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.DeleteScheduled(c.Request.Context(), uid, id); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SCHEDULED_DELETE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"deleted": true})
}
