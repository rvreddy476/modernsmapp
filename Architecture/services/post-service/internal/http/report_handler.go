package http

import (
	"errors"
	"net/http"
	"time"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/atpost/post-service/internal/store/postgres"
)

// RegisterReportRoutes registers content report endpoints.
func (h *Handler) RegisterReportRoutes(r *gin.Engine) {
	// REMOVED (security sweep #23, 21 Oct 2026): POST /v1/reports. The
	// gateway sends /v1/reports to trust-safety-service, which dedupes and
	// caps reports; this copy had neither, and a comment report through it
	// auto-hid the comment (comment_report_holds.go) — one user, any comment,
	// if the prefix were ever repointed. The service-side SubmitReport stays
	// for the consumers that call it in-process.

	// Admin-facing: list and review reports. Moderator-or-higher scope is
	// checked here, not left to gateway route order.
	reports := r.Group("/v1/admin/reports", requireModerator())
	{
		reports.GET("", h.ListReports)
		reports.PATCH("/:reportId", h.ReviewReport)
	}

	// Tier 2b: comment moderation queue
	mod := r.Group("/v1/admin/comments", requireModerator())
	{
		mod.GET("/moderation", h.ListFlaggedComments)
		mod.PATCH("/:commentId/moderation", h.ModerateComment)
	}
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
// The actor is the gateway-verified moderator (requireModerator has already
// validated X-User-Id); the review is audited in post_admin_audit.
func (h *Handler) ReviewReport(c *gin.Context) {
	actor, ok := requireModeratorIdentity(c)
	if !ok {
		return
	}
	h.reviewReport(c, actor, nil)
}

// reviewReport is the review write shared by the LEGACY route and the
// admin-service token route. allow, when set, is asked about the report's
// target type before anything is written (it answers the refusal itself).
func (h *Handler) reviewReport(c *gin.Context, actor uuid.UUID, allow func(c *gin.Context, targetType string) bool) {
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
	if len(body.ReviewNote) > 2000 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "review_note must be at most 2000 characters", nil)
		return
	}

	if allow != nil {
		report, err := h.svc.GetContentReport(c.Request.Context(), reportID)
		if errors.Is(err, postgres.ErrReportNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Report not found", nil)
			return
		}
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to review report", nil)
			return
		}
		if !allow(c, report.TargetType) {
			return
		}
	}

	if err := h.svc.ReviewReport(c.Request.Context(), reportID, body.Status, actor, body.ReviewNote); err != nil {
		if errors.Is(err, postgres.ErrReportNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Report not found", nil)
			return
		}
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
// The actor is the gateway-verified moderator; the change is audited.
func (h *Handler) ModerateComment(c *gin.Context) {
	actor, ok := requireModeratorIdentity(c)
	if !ok {
		return
	}
	h.moderateComment(c, actor, nil)
}

// moderateComment is the comment moderation write shared by the LEGACY route
// and the admin-service token route. allow, when set, is asked about the
// requested status before anything is written.
func (h *Handler) moderateComment(c *gin.Context, actor uuid.UUID, allow func(c *gin.Context, status string) bool) {
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
	if allow != nil && !allow(c, body.Status) {
		return
	}
	if err := h.svc.SetCommentModerationStatus(c.Request.Context(), actor, commentID, body.Status); err != nil {
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
