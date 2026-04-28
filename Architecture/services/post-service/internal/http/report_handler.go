package http

import (
	"net/http"
	"time"

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

	// Tier 2b: comment moderation queue
	mod := r.Group("/v1/admin/comments")
	{
		mod.GET("/moderation", h.ListFlaggedComments)
		mod.PATCH("/:commentId/moderation", h.ModerateComment)
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

// ---------------------------------------------------------------------------
// Tier 2b: comment moderation
// ---------------------------------------------------------------------------

// ListFlaggedComments — GET /v1/admin/comments/moderation
// Query params:
//
//	status=review|hidden|removed|flagged|all (default all)
//	cursor=RFC3339 (created_at of the last seen row, descending)
//	limit=int (default 50, max 200)
//
// Returns the comments that need a moderator's eyes plus those that
// have already been actioned, so the audit trail is one fetch away.
func (h *Handler) ListFlaggedComments(c *gin.Context) {
	status := c.DefaultQuery("status", "")
	limit := 50
	if v := c.Query("limit"); v != "" {
		if n, err := parseIntParam(v); err == nil && n > 0 {
			limit = n
		}
	}
	cursor := time.Now()
	if v := c.Query("cursor"); v != "" {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			cursor = t
		}
	}
	rows, err := h.svc.ListFlaggedComments(c.Request.Context(), status, cursor, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if rows == nil {
		rows = []postgres.FlaggedComment{}
	}
	var meta *api.Meta
	if len(rows) == limit {
		meta = &api.Meta{NextCursor: rows[len(rows)-1].CreatedAt.Format(time.RFC3339Nano)}
	}
	api.JSON(c.Writer, http.StatusOK, rows, meta)
}

// ModerateComment — PATCH /v1/admin/comments/:commentId/moderation
// Body: {"status": "visible|hidden|removed|review"}
func (h *Handler) ModerateComment(c *gin.Context) {
	commentID, err := uuid.Parse(c.Param("commentId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid comment ID", nil)
		return
	}
	var body struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	if err := h.svc.SetCommentModerationStatus(c.Request.Context(), commentID, body.Status); err != nil {
		switch err.Error() {
		case "COMMENT_NOT_FOUND":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Comment not found", nil)
		default:
			if len(err.Error()) > 25 && err.Error()[:25] == "INVALID_MODERATION_STATUS" {
				api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
				return
			}
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": body.Status}, nil)
}
