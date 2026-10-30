package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// --- Question Follows ---

func (h *Handler) FollowQuestion(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	qID, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}

	if err := h.svc.FollowQuestion(c.Request.Context(), userID, qID); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "FOLLOW_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "followed"}, nil)
}

func (h *Handler) UnfollowQuestion(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	qID, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}

	if err := h.svc.UnfollowQuestion(c.Request.Context(), userID, qID); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "UNFOLLOW_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unfollowed"}, nil)
}

// --- Topic Follows ---

func (h *Handler) FollowTopic(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	topicID, ok := parseUUID(c, "topicId")
	if !ok {
		return
	}

	if err := h.svc.FollowTopic(c.Request.Context(), userID, topicID); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "FOLLOW_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "followed"}, nil)
}

func (h *Handler) UnfollowTopic(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	topicID, ok := parseUUID(c, "topicId")
	if !ok {
		return
	}

	if err := h.svc.UnfollowTopic(c.Request.Context(), userID, topicID); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "UNFOLLOW_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unfollowed"}, nil)
}

func (h *Handler) GetFollowedTopics(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	topics, err := h.svc.GetFollowedTopics(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, topics, nil)
}

// --- Contributor Follows ---

func (h *Handler) FollowContributor(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	targetID, ok := parseUUID(c, "userId")
	if !ok {
		return
	}

	if err := h.svc.FollowContributor(c.Request.Context(), userID, targetID); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "FOLLOW_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "followed"}, nil)
}

func (h *Handler) UnfollowContributor(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	targetID, ok := parseUUID(c, "userId")
	if !ok {
		return
	}

	if err := h.svc.UnfollowContributor(c.Request.Context(), userID, targetID); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "UNFOLLOW_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unfollowed"}, nil)
}

// --- Question Saves ---

func (h *Handler) SaveQuestion(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	qID, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}

	if err := h.svc.SaveQuestion(c.Request.Context(), userID, qID); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SAVE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "saved"}, nil)
}

func (h *Handler) UnsaveQuestion(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	qID, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}

	if err := h.svc.UnsaveQuestion(c.Request.Context(), userID, qID); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "UNSAVE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unsaved"}, nil)
}

func (h *Handler) GetSavedQuestions(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit, offset := parsePagination(c)

	questions, err := h.svc.GetSavedQuestions(c.Request.Context(), userID, limit, offset)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, questions, nil)
}

// --- Answer Saves ---

func (h *Handler) SaveAnswer(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	aID, ok := parseUUID(c, "answerId")
	if !ok {
		return
	}

	if err := h.svc.SaveAnswer(c.Request.Context(), userID, aID); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SAVE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "saved"}, nil)
}

func (h *Handler) UnsaveAnswer(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	aID, ok := parseUUID(c, "answerId")
	if !ok {
		return
	}

	if err := h.svc.UnsaveAnswer(c.Request.Context(), userID, aID); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "UNSAVE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unsaved"}, nil)
}

func (h *Handler) GetSavedAnswers(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit, offset := parsePagination(c)

	answers, err := h.svc.GetSavedAnswers(c.Request.Context(), userID, limit, offset)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, answers, nil)
}

// --- Answer Requests ---

func (h *Handler) CreateAnswerRequest(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	qID, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}

	var body struct {
		RequestedUserID string `json:"requested_user_id"`
		// TargetUserID is the web client's name for it (contract_aliases.go).
		TargetUserID string `json:"target_user_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	targetID, ok := parseUUIDString(c, firstNonEmpty(body.RequestedUserID, body.TargetUserID), "requested_user_id")
	if !ok {
		return
	}

	req, err := h.svc.CreateAnswerRequest(c.Request.Context(), qID, userID, targetID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "CREATE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusCreated, req, nil)
}

func (h *Handler) GetMyAnswerRequests(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit, offset := parsePagination(c)

	requests, err := h.svc.GetAnswerRequests(c.Request.Context(), userID, limit, offset)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, requests, nil)
}

func (h *Handler) RespondToAnswerRequest(c *gin.Context) {
	reqID, ok := parseUUID(c, "requestId")
	if !ok {
		return
	}
	// Audit CQ2: ensure the caller is the targeted user.
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var body struct {
		Status string `json:"status"`
		// Response is the web client's "accept"/"decline" (contract_aliases.go).
		Response string `json:"response"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	status := answerRequestStatus(body.Status, body.Response)

	if err := h.svc.RespondToAnswerRequest(c.Request.Context(), reqID, userID, status); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "RESPOND_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": status}, nil)
}
