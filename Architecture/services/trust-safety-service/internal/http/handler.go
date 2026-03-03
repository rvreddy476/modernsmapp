package http

import (
	"log"
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	svc *service.Service
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	v1 := r.Group("/v1/reports")
	{
		v1.POST("", h.FileReport)
		v1.GET("", h.ListReports)
	}
}

type FileReportRequest struct {
	EntityType string `json:"entity_type" binding:"required,oneof=user post comment"`
	EntityID   string `json:"entity_id" binding:"required"`
	Reason     string `json:"reason" binding:"required"`
	Details    string `json:"details"`
}

func (h *Handler) FileReport(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req FileReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	entityID, err := uuid.Parse(req.EntityID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid entity ID", nil, nil)
		return
	}

	report, err := h.svc.FileReport(c.Request.Context(), userID, entityID, req.EntityType, req.Reason, req.Details)
	if err != nil {
		log.Printf("FileReport error: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to file report", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, report, nil)
}

func (h *Handler) ListReports(c *gin.Context) {
	// In a real app, check for ADMIN role here.
	// For V1, we assume internal network or basic auth handled by gateway/setup.

	limit := 20
	if l := c.Query("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 && val <= 100 {
			limit = val
		}
	}

	offset := 0
	if o := c.Query("offset"); o != "" {
		if val, err := strconv.Atoi(o); err == nil && val >= 0 {
			offset = val
		}
	}

	reports, err := h.svc.ListReports(c.Request.Context(), limit, offset)
	if err != nil {
		log.Printf("ListReports error: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list reports", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": reports}, nil)
}
