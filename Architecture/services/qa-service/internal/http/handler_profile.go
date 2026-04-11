package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/qa-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *Handler) GetMyProfile(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	profile, err := h.svc.GetOrCreateProfile(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, profile, nil)
}

func (h *Handler) GetProfile(c *gin.Context) {
	targetID, ok := parseUUID(c, "userId")
	if !ok {
		return
	}

	profile, err := h.svc.GetProfile(c.Request.Context(), targetID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "profile not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, profile, nil)
}

func (h *Handler) UpdateProfile(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var body store.UpdateProfileParams
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}

	profile, err := h.svc.UpdateProfile(c.Request.Context(), userID, body)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "UPDATE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, profile, nil)
}

func (h *Handler) GetReputationHistory(c *gin.Context) {
	targetID, ok := parseUUID(c, "userId")
	if !ok {
		return
	}
	limit, offset := parsePagination(c)

	events, err := h.svc.GetReputationHistory(c.Request.Context(), targetID, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, events, nil)
}

func (h *Handler) GetBadges(c *gin.Context) {
	targetID, ok := parseUUID(c, "userId")
	if !ok {
		return
	}

	badges, err := h.svc.GetBadges(c.Request.Context(), targetID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, badges, nil)
}

func (h *Handler) GetUserQuestions(c *gin.Context) {
	targetID, ok := parseUUID(c, "userId")
	if !ok {
		return
	}
	limit, offset := parsePagination(c)

	questions, err := h.svc.ListQuestionsByAuthor(c.Request.Context(), targetID, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, questions, nil)
}

func (h *Handler) GetUserAnswers(c *gin.Context) {
	targetID, ok := parseUUID(c, "userId")
	if !ok {
		return
	}
	limit, offset := parsePagination(c)

	answers, err := h.svc.Store().ListAnswersByAuthor(c.Request.Context(), targetID, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, answers, nil)
}

func (h *Handler) GetLeaderboard(c *gin.Context) {
	var topicID *uuid.UUID
	if v := c.Query("topic_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "invalid topic_id", nil, nil)
			return
		}
		topicID = &id
	}
	limit := 20
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	profiles, err := h.svc.GetLeaderboard(c.Request.Context(), topicID, limit)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, profiles, nil)
}
