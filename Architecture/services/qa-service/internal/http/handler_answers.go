package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

func (h *Handler) CreateAnswer(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	qID, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}

	var body struct {
		Body        string `json:"body"`
		BodyHTML    string `json:"body_html"`
		IsAnonymous bool   `json:"is_anonymous,omitempty"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	answer, err := h.svc.CreateAnswer(c.Request.Context(), qID, userID, body.Body, body.BodyHTML, body.IsAnonymous)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "CREATE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusCreated, answer, nil)
}

func (h *Handler) ListAnswers(c *gin.Context) {
	qID, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}
	limit, offset := parsePagination(c)
	sortBy := c.DefaultQuery("sort", "votes")
	viewerID := optionalUserID(c)

	answers, err := h.svc.ListAnswers(c.Request.Context(), qID, viewerID, sortBy, limit, offset)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, answers, nil)
}

func (h *Handler) GetAnswer(c *gin.Context) {
	aID, ok := parseUUID(c, "answerId")
	if !ok {
		return
	}

	answer, err := h.svc.GetAnswer(c.Request.Context(), aID, optionalUserID(c))
	if err != nil {
		respondServiceError(c, err, http.StatusNotFound, "NOT_FOUND")
		return
	}
	api.JSON(c.Writer, http.StatusOK, answer, nil)
}

func (h *Handler) UpdateAnswer(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	aID, ok := parseUUID(c, "answerId")
	if !ok {
		return
	}

	var body struct {
		Body     string `json:"body"`
		BodyHTML string `json:"body_html"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	answer, err := h.svc.UpdateAnswer(c.Request.Context(), aID, userID, body.Body, body.BodyHTML)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "UPDATE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, answer, nil)
}

func (h *Handler) DeleteAnswer(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	aID, ok := parseUUID(c, "answerId")
	if !ok {
		return
	}

	if err := h.svc.DeleteAnswer(c.Request.Context(), aID, userID); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "DELETE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

func (h *Handler) SelectBestAnswer(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	qID, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}

	var body struct {
		AnswerID string `json:"answer_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	aID, ok := parseUUIDString(c, body.AnswerID, "answer_id")
	if !ok {
		return
	}

	if err := h.svc.SelectBestAnswer(c.Request.Context(), qID, aID, userID); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SELECT_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "selected"}, nil)
}

func (h *Handler) UnselectBestAnswer(c *gin.Context) {
	qID, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}
	// Audit CQ3: previously bypassed service entirely and called the
	// store directly with no auth check. Only the question author can
	// unselect their accepted answer.
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	q, err := h.svc.Store().GetQuestion(c.Request.Context(), qID)
	if err != nil || q == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "question not found", nil)
		return
	}
	if q.AuthorID != userID {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "NOT_AUTHOR", "only the question author can unselect a best answer", nil)
		return
	}

	if err := h.svc.Store().UnselectBestAnswer(c.Request.Context(), qID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "UNSELECT_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unselected"}, nil)
}
