package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

func (h *Handler) VoteQuestion(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	qID, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}

	var body struct {
		VoteType string `json:"vote_type"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}

	if err := h.svc.VoteQuestion(c.Request.Context(), userID, qID, body.VoteType); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "VOTE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "voted"}, nil)
}

func (h *Handler) RemoveQuestionVote(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	qID, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}

	if err := h.svc.RemoveQuestionVote(c.Request.Context(), userID, qID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "REMOVE_VOTE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "removed"}, nil)
}

func (h *Handler) VoteAnswer(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	aID, ok := parseUUID(c, "answerId")
	if !ok {
		return
	}

	var body struct {
		VoteType string `json:"vote_type"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}

	if err := h.svc.VoteAnswer(c.Request.Context(), userID, aID, body.VoteType); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "VOTE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "voted"}, nil)
}

func (h *Handler) RemoveAnswerVote(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	aID, ok := parseUUID(c, "answerId")
	if !ok {
		return
	}

	if err := h.svc.RemoveAnswerVote(c.Request.Context(), userID, aID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "REMOVE_VOTE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "removed"}, nil)
}

func (h *Handler) VoteComment(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	cID, ok := parseUUID(c, "commentId")
	if !ok {
		return
	}

	var body struct {
		VoteType string `json:"vote_type"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}

	if err := h.svc.VoteComment(c.Request.Context(), userID, cID, body.VoteType); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "VOTE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "voted"}, nil)
}

func (h *Handler) RemoveCommentVote(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	cID, ok := parseUUID(c, "commentId")
	if !ok {
		return
	}

	if err := h.svc.RemoveCommentVote(c.Request.Context(), userID, cID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "REMOVE_VOTE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "removed"}, nil)
}
