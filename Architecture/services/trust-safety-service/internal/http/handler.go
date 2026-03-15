package http

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/shared/api"
	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// hasScope reports whether the space-separated scopes string contains the exact target scope.
func hasScope(scopes, target string) bool {
	for _, s := range strings.Fields(scopes) {
		if s == target {
			return true
		}
	}
	return false
}

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
		v1.GET("/:id", h.GetReport)
		v1.PATCH("/:id", h.UpdateReport)
	}

	appeals := r.Group("/v1/appeals")
	{
		appeals.POST("", h.SubmitAppeal)
		appeals.GET("", h.AdminListAppeals)
		appeals.PATCH("/:id", h.ReviewAppeal)
	}

	keywords := r.Group("/v1/keyword-filters")
	{
		keywords.POST("", h.AddKeywordFilter)
		keywords.GET("", h.GetKeywordFilters)
	}

	teen := r.Group("/v1/teen-accounts")
	{
		teen.POST("", h.UpsertTeenAccount)
		teen.GET("/:userId", h.GetTeenAccount)
	}

	mediaLabels := r.Group("/v1/media-labels")
	{
		mediaLabels.POST("", h.AddMediaLabel)
		mediaLabels.GET("/:mediaId", h.GetMediaLabels)
	}

	strikes := r.Group("/v1/strikes")
	{
		strikes.POST("", h.IssueStrike)
		strikes.GET("/:userId", h.GetUserStrikes)
	}

	verification := r.Group("/v1/verification-requests")
	{
		verification.POST("", h.SubmitVerificationRequest)
		verification.GET("", h.AdminListVerificationRequests)
		verification.PATCH("/:id", h.ReviewVerificationRequest)
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
	scopes := c.GetHeader("X-Scopes")
	if !hasScope(scopes, "admin") {
		api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "Admin scope required", nil, nil)
		return
	}

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

func (h *Handler) GetReport(c *gin.Context) {
	scopes := c.GetHeader("X-Scopes")
	if !hasScope(scopes, "admin") {
		api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "Admin scope required", nil, nil)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid report ID", nil, nil)
		return
	}
	// We need to call store directly or add GetReport to service
	// Call through service
	report, err := h.svc.GetReport(c.Request.Context(), id.String())
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Report not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, report, nil)
}

type UpdateReportRequest struct {
	Status          string  `json:"status" binding:"required,oneof=reviewing resolved dismissed"`
	AssignedTo      *string `json:"assigned_to,omitempty"`
	ResolutionNotes string  `json:"resolution_notes"`
}

func (h *Handler) UpdateReport(c *gin.Context) {
	scopes := c.GetHeader("X-Scopes")
	if !hasScope(scopes, "admin") {
		api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "Admin scope required", nil, nil)
		return
	}
	actorID := c.GetHeader("X-User-Id")
	if actorID == "" {
		actorID = "system-admin"
	}
	reportID := c.Param("id")
	if _, err := uuid.Parse(reportID); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid report ID", nil, nil)
		return
	}
	var req UpdateReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	var assignedTo *uuid.UUID
	if req.AssignedTo != nil {
		id, err := uuid.Parse(*req.AssignedTo)
		if err == nil {
			assignedTo = &id
		}
	}
	report, err := h.svc.UpdateReport(c.Request.Context(), actorID, reportID, req.Status, assignedTo, req.ResolutionNotes)
	if err != nil {
		log.Printf("UpdateReport error: %v", err)
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, report, nil)
}
