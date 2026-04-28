package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/atpost/post-service/internal/store/postgres"
)

// RegisterReportRoutes registers content report endpoints.
func (h *Handler) RegisterReportRoutes(r *gin.Engine) {
	// User-facing: submit a report
	r.POST("/v1/reports", h.SubmitReport)

	// Admin-facing: list and review reports
	reports := r.Group("/v1/admin/reports")
	{
		reports.GET("", h.ListReports)
		reports.PATCH("/:reportId", h.ReviewReport)
	}
}

// SubmitReport handles POST /v1/reports
func (h *Handler) SubmitReport(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var body struct {
		TargetType  string `json:"target_type" binding:"required"`
		TargetID    string `json:"target_id" binding:"required"`
		Reason      string `json:"reason" binding:"required"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", nil)
		return
	}

	targetID, err := uuid.Parse(body.TargetID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid target ID", nil)
		return
	}

	// Validate target_type
	validTypes := map[string]bool{"post": true, "comment": true, "reel": true, "video": true}
	if !validTypes[body.TargetType] {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid target type", nil)
		return
	}

	// Validate reason
	validReasons := map[string]bool{
		"spam": true, "harassment": true, "hate_speech": true,
		"violence": true, "nudity": true, "misinformation": true, "other": true,
	}
	if !validReasons[body.Reason] {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid reason", nil)
		return
	}

	report := &postgres.ContentReport{
		ReporterID:  userID,
		TargetType:  body.TargetType,
		TargetID:    targetID,
		Reason:      body.Reason,
		Description: body.Description,
	}

	if err := h.svc.SubmitReport(c.Request.Context(), report); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to submit report", nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, map[string]interface{}{
		"id":      report.ID,
		"status":  "pending",
		"message": "Report submitted successfully",
	}, nil)
}

// ListReports handles GET /v1/admin/reports?status=pending&limit=50&offset=0
func (h *Handler) ListReports(c *gin.Context) {
	status := c.DefaultQuery("status", "")
	limit := 50
	offset := 0

	if l := c.Query("limit"); l != "" {
		if v, err := parseIntParam(l); err == nil && v > 0 && v <= 100 {
			limit = v
		}
	}
	if o := c.Query("offset"); o != "" {
		if v, err := parseIntParam(o); err == nil && v >= 0 {
			offset = v
		}
	}

	reports, err := h.svc.ListReports(c.Request.Context(), status, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list reports", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, reports, nil)
}

// ReviewReport handles PATCH /v1/admin/reports/:reportId
func (h *Handler) ReviewReport(c *gin.Context) {
	reviewerID := c.GetHeader("X-User-Id")
	reportID, err := uuid.Parse(c.Param("reportId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid report ID", nil)
		return
	}

	var body struct {
		Status     string `json:"status" binding:"required"`
		ReviewNote string `json:"review_note"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", nil)
		return
	}

	validStatuses := map[string]bool{"reviewed": true, "resolved": true, "dismissed": true}
	if !validStatuses[body.Status] {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid status", nil)
		return
	}

	if err := h.svc.ReviewReport(c.Request.Context(), reportID, body.Status, reviewerID, body.ReviewNote); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to review report", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

func parseIntParam(s string) (int, error) {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, nil
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}
