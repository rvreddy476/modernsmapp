package http

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/shared/api"
	"github.com/atpost/shared/servicetoken"
	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	// verifier admits admin-service tokens on the InternalAdminPrefix family
	// (admin_token.go). nil: no token is accepted.
	verifier *servicetoken.Verifier
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes declares the key-gated routes. The token-only admin family
// is declared separately by RegisterAdminTokenRoutes.
func (h *Handler) RegisterRoutes(r gin.IRouter) {
	// Dating plan lane D8: dating-service opens a grievance per dating
	// report. Service callers only (user identity headers are refused).
	r.POST(DatingReportGrievancePath, h.LinkDatingReportGrievance)

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

	// Self-service filter keywords (Content preferences → Filter keywords).
	myKeywords := r.Group("/v1/users/me/keyword-filters")
	{
		myKeywords.GET("", h.GetMyKeywordFilters)
		myKeywords.PUT("", h.PutMyKeywordFilters)
	}

	// Peer services (feed-service) read a user's hide keywords here. The
	// whole router is behind RequireInternalKey already.
	r.GET("/v1/internal/keyword-filters", h.InternalGetKeywordFilters)

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

	// IT Rules 2021 grievance redressal.
	grievances := r.Group("/v1/grievances")
	{
		grievances.POST("", h.FileGrievance)                  // any user lodges a grievance
		grievances.GET("", h.ListGrievances)                  // ?mine=true: own; else officer queue (?status=, ?overdue=true)
		grievances.GET("/:id", h.GetGrievance)                // complainant or officer
		grievances.GET("/:id/history", h.GetGrievanceHistory) // admin: full audited history
		grievances.PATCH("/:id", h.UpdateGrievance)           // officer verdict and/or assignment
	}
}

type FileReportRequest struct {
	// reel and video remain accepted only as wire-compatibility aliases. The
	// service normalizes both to post before validation or persistence.
	EntityType string `json:"entity_type" binding:"required,oneof=user post comment reel video"`
	EntityID   string `json:"entity_id" binding:"required"`
	Reason     string `json:"reason" binding:"required"`
	Details    string `json:"details"`
}

func (h *Handler) FileReport(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req FileReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}

	entityID, err := uuid.Parse(req.EntityID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid entity ID", nil)
		return
	}

	report, err := h.svc.FileReport(c.Request.Context(), userID, entityID, req.EntityType, req.Reason, req.Details)
	if err != nil {
		if errors.Is(err, postgres.ErrActiveReportExists) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "ACTIVE_REPORT_EXISTS", "You already have an active report for this content", nil)
			return
		}
		slog.Error("FileReport error", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to file report", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, report, nil)
}

func (h *Handler) ListReports(c *gin.Context) {
	limit, offset := reportPagination(c)
	// ?mine=true is a user's own list; it has no meaning on the admin token path.
	if c.Query("mine") == "true" && !viaAdminToken(c) {
		userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
			return
		}
		reports, err := h.svc.ListOwnReports(c.Request.Context(), userID, limit, offset)
		if err != nil {
			slog.Error("ListOwnReports error", "error", err)
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list reports", nil)
			return
		}
		api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": reports}, nil)
		return
	}

	if !adminAllowed(c) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Admin scope required", nil)
		return
	}

	reports, err := h.svc.ListReports(c.Request.Context(), limit, offset)
	if err != nil {
		slog.Error("ListReports error", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list reports", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": reports}, nil)
}

func reportPagination(c *gin.Context) (int, int) {
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

	return limit, offset
}

func (h *Handler) GetReport(c *gin.Context) {
	if !adminAllowed(c) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Admin scope required", nil)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid report ID", nil)
		return
	}
	// We need to call store directly or add GetReport to service
	// Call through service
	report, err := h.svc.GetReport(c.Request.Context(), id.String())
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Report not found", nil)
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
	if !adminAllowed(c) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Admin scope required", nil)
		return
	}
	meta, ok := adminAuditMeta(c, "")
	if !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, codeActorRequired, "A verified admin user is required", nil)
		return
	}
	reportID := c.Param("id")
	if _, err := uuid.Parse(reportID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid report ID", nil)
		return
	}
	var req UpdateReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	var assignedTo *uuid.UUID
	if req.AssignedTo != nil {
		id, err := uuid.Parse(*req.AssignedTo)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid assigned_to", nil)
			return
		}
		assignedTo = &id
	}
	meta.Reason = req.ResolutionNotes
	report, err := h.svc.UpdateReport(c.Request.Context(), meta, reportID, req.Status, assignedTo, req.ResolutionNotes)
	if err != nil {
		writeAuditedChangeError(c, "UpdateReport", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, report, nil)
}

// writeAuditedChangeError maps an audited report/appeal/grievance change
// failure to a response.
func writeAuditedChangeError(c *gin.Context, op string, err error) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, postgres.ErrActorRequired):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, codeActorRequired, "A verified admin user is required", nil)
	case errors.Is(err, pgx.ErrNoRows):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "Not found", nil)
	case errors.Is(err, postgres.ErrInvalidTransition):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_TRANSITION", err.Error(), nil)
	case errors.Is(err, postgres.ErrNoChange):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "NO_CHANGE", err.Error(), nil)
	default:
		slog.Error(op+" error", "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
	}
}
