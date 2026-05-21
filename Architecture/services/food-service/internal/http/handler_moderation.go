package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ReportMenuItemRequest is the customer-side complaint body.
type ReportMenuItemRequest struct {
	Category string `json:"category"`
	Detail   string `json:"detail,omitempty"`
}

// ReportMenuItem — POST /v1/food/menu-items/:itemId/report
func (h *Handler) ReportMenuItem(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	itemID, err := uuid.Parse(c.Param("itemId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ITEM_ID", err.Error(), nil)
		return
	}
	var req ReportMenuItemRequest
	if err := c.BindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if req.Category == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "category required", nil)
		return
	}
	rep, err := h.svc.ReportMenuItem(c.Request.Context(), uid, itemID, req.Category, req.Detail)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "REPORT_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, rep, nil)
}

// AdminListPendingModeration — GET /v1/food/admin/moderation/queue
func (h *Handler) AdminListPendingModeration(c *gin.Context) {
	limit := 50
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
		limit = l
	}
	items, err := h.svc.ListPendingModeration(c.Request.Context(), limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "MODERATION_QUEUE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items}, nil)
}

// AdminModerateMenuItemRequest is the admin verdict body.
type AdminModerateMenuItemRequest struct {
	Status string `json:"status"` // approved | rejected | pending_review | flagged
	Reason string `json:"reason,omitempty"`
}

// AdminModerateMenuItem — POST /v1/food/admin/moderation/menu-items/:itemId
func (h *Handler) AdminModerateMenuItem(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	itemID, err := uuid.Parse(c.Param("itemId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ITEM_ID", err.Error(), nil)
		return
	}
	var req AdminModerateMenuItemRequest
	if err := c.BindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.ModerateMenuItem(c.Request.Context(), uid, itemID, req.Status, req.Reason); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "MODERATE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"item_id": itemID.String(), "status": req.Status}, nil)
}
