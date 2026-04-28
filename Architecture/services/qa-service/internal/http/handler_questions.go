package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/qa-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *Handler) CreateQuestion(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var body store.CreateQuestionParams
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	q, err := h.svc.CreateQuestion(c.Request.Context(), userID, body)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "CREATE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusCreated, q, nil)
}

func (h *Handler) ListQuestions(c *gin.Context) {
	viewerID := optionalUserID(c)
	limit, offset := parsePagination(c)

	var communityID *uuid.UUID
	if raw := c.Query("community_id"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_PARAM", "invalid community_id", nil)
			return
		}
		communityID = &parsed
	}

	questions, err := h.svc.ListQuestions(
		c.Request.Context(),
		viewerID,
		c.Query("topic"),
		communityID,
		c.Query("scope"),
		c.DefaultQuery("sort", "recent"),
		c.Query("status"),
		limit,
		offset,
	)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]any{
		"questions": questions,
		"pagination": map[string]any{
			"limit":    limit,
			"offset":   offset,
			"has_more": len(questions) == limit,
		},
	}, nil)
}

func (h *Handler) GetQuestion(c *gin.Context) {
	qID, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}

	viewerID := optionalUserID(c)
	q, err := h.svc.GetQuestion(c.Request.Context(), qID, viewerID)
	if err != nil {
		respondServiceError(c, err, http.StatusNotFound, "NOT_FOUND")
		return
	}
	api.JSON(c.Writer, http.StatusOK, q, nil)
}

func (h *Handler) GetQuestionBySlug(c *gin.Context) {
	slug := c.Param("slug")
	if slug == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_PARAM", "slug is required", nil)
		return
	}

	q, err := h.svc.Store().GetQuestionBySlug(c.Request.Context(), slug)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "question not found", nil)
		return
	}
	q, err = h.svc.GetQuestion(c.Request.Context(), q.ID, optionalUserID(c))
	if err != nil {
		respondServiceError(c, err, http.StatusNotFound, "NOT_FOUND")
		return
	}
	api.JSON(c.Writer, http.StatusOK, q, nil)
}

func (h *Handler) UpdateQuestion(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	qID, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}

	var body store.UpdateQuestionParams
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	q, err := h.svc.UpdateQuestion(c.Request.Context(), qID, userID, body)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "UPDATE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, q, nil)
}

func (h *Handler) DeleteQuestion(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	qID, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}

	if err := h.svc.DeleteQuestion(c.Request.Context(), qID, userID); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "DELETE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

func (h *Handler) CloseQuestion(c *gin.Context) {
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
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	if err := h.svc.CloseQuestion(c.Request.Context(), qID, userID, body.Reason); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "CLOSE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "closed"}, nil)
}

func (h *Handler) ReopenQuestion(c *gin.Context) {
	qID, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}

	if err := h.svc.ReopenQuestion(c.Request.Context(), qID); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "REOPEN_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "reopened"}, nil)
}

func (h *Handler) GetSimilarQuestions(c *gin.Context) {
	title := c.Query("title")
	if title == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_PARAM", "title query param is required", nil)
		return
	}
	limit := 5
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 20 {
			limit = n
		}
	}

	questions, err := h.svc.FindSimilarQuestions(c.Request.Context(), title, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, questions, nil)
}

func (h *Handler) GetMyQuestions(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit, offset := parsePagination(c)

	questions, err := h.svc.ListQuestionsByAuthor(c.Request.Context(), userID, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, questions, nil)
}
