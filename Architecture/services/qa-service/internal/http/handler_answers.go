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
		Body     string `json:"body"`
		BodyHTML string `json:"body_html"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}

	answer, err := h.svc.CreateAnswer(c.Request.Context(), qID, userID, body.Body, body.BodyHTML)
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

	answer, err := h.svc.Store().GetAnswer(c.Request.Context(), aID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "answer not found", nil, nil)
		return
	}
	if err := h.svc.EnsureQuestionVisible(c.Request.Context(), answer.QuestionID, optionalUserID(c)); err != nil {
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
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

	if err := h.svc.Store().UnselectBestAnswer(c.Request.Context(), qID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "UNSELECT_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unselected"}, nil)
}
