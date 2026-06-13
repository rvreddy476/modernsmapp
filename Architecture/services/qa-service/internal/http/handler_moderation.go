package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

func (h *Handler) CreateReport(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var body struct {
		TargetType string `json:"target_type"`
		TargetID   string `json:"target_id"`
		Reason     string `json:"reason"`
		Details    string `json:"details"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	targetID, ok := parseUUIDString(c, body.TargetID, "target_id")
	if !ok {
		return
	}

	report, err := h.svc.CreateReport(c.Request.Context(), userID, body.TargetType, targetID, body.Reason, body.Details)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "CREATE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, report, nil)
}

func (h *Handler) ListReports(c *gin.Context) {
	if !h.requireModerator(c) {
		return
	}
	status := c.DefaultQuery("status", "pending")
	limit, offset := parsePagination(c)

	reports, err := h.svc.ListReports(c.Request.Context(), status, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, reports, nil)
}

func (h *Handler) GetReport(c *gin.Context) {
	if !h.requireModerator(c) {
		return
	}
	reportID, ok := parseUUID(c, "reportId")
	if !ok {
		return
	}

	report, err := h.svc.GetReport(c.Request.Context(), reportID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "report not found", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, report, nil)
}

func (h *Handler) ResolveReport(c *gin.Context) {
	if !h.requireModerator(c) {
		return
	}
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	reportID, ok := parseUUID(c, "reportId")
	if !ok {
		return
	}

	if err := h.svc.ResolveReport(c.Request.Context(), reportID, userID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "RESOLVE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "resolved"}, nil)
}

func (h *Handler) DismissReport(c *gin.Context) {
	if !h.requireModerator(c) {
		return
	}
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	reportID, ok := parseUUID(c, "reportId")
	if !ok {
		return
	}

	if err := h.svc.DismissReport(c.Request.Context(), reportID, userID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "DISMISS_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "dismissed"}, nil)
}

func (h *Handler) HideQuestion(c *gin.Context) {
	if !h.requireModerator(c) {
		return
	}
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	qID, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}

	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)

	if err := h.svc.HideContent(c.Request.Context(), "question", qID, userID, body.Reason); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "HIDE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "hidden"}, nil)
}

func (h *Handler) LockQuestion(c *gin.Context) {
	if !h.requireModerator(c) {
		return
	}
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	qID, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}

	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)

	if err := h.svc.LockQuestion(c.Request.Context(), qID, userID, body.Reason); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "LOCK_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "locked"}, nil)
}

func (h *Handler) MergeQuestion(c *gin.Context) {
	if !h.requireModerator(c) {
		return
	}
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	qID, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}

	var body struct {
		MergeIntoID string `json:"merge_into_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	mergeIntoID, ok := parseUUIDString(c, body.MergeIntoID, "merge_into_id")
	if !ok {
		return
	}

	if err := h.svc.MergeQuestion(c.Request.Context(), qID, mergeIntoID, userID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "MERGE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "merged"}, nil)
}

func (h *Handler) MarkDuplicate(c *gin.Context) {
	if !h.requireModerator(c) {
		return
	}
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	qID, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}

	var body struct {
		DuplicateOfID string `json:"duplicate_of_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	dupID, ok := parseUUIDString(c, body.DuplicateOfID, "duplicate_of_id")
	if !ok {
		return
	}

	if err := h.svc.MarkDuplicate(c.Request.Context(), qID, dupID, userID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "DUPLICATE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "marked_duplicate"}, nil)
}

func (h *Handler) HideAnswer(c *gin.Context) {
	if !h.requireModerator(c) {
		return
	}
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	aID, ok := parseUUID(c, "answerId")
	if !ok {
		return
	}

	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)

	if err := h.svc.HideContent(c.Request.Context(), "answer", aID, userID, body.Reason); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "HIDE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "hidden"}, nil)
}

func (h *Handler) HideComment(c *gin.Context) {
	if !h.requireModerator(c) {
		return
	}
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	commentID, ok := parseUUID(c, "commentId")
	if !ok {
		return
	}

	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)

	if err := h.svc.HideContent(c.Request.Context(), "comment", commentID, userID, body.Reason); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "HIDE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "hidden"}, nil)
}

func (h *Handler) ListModerationActions(c *gin.Context) {
	if !h.requireModerator(c) {
		return
	}
	limit, offset := parsePagination(c)

	actions, err := h.svc.ListModerationActions(c.Request.Context(), limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, actions, nil)
}
