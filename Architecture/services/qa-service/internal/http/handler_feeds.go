package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *Handler) GetHomeFeed(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit, offset := parsePagination(c)

	questions, err := h.svc.GetHomeFeed(c.Request.Context(), userID, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, questions, nil)
}

func (h *Handler) GetTrendingFeed(c *gin.Context) {
	limit, offset := parsePagination(c)

	questions, err := h.svc.GetTrendingQuestions(c.Request.Context(), limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, questions, nil)
}

func (h *Handler) GetUnansweredFeed(c *gin.Context) {
	var topicID *uuid.UUID
	if v := c.Query("topic_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "invalid topic_id", nil, nil)
			return
		}
		topicID = &id
	}
	limit, offset := parsePagination(c)

	questions, err := h.svc.GetUnansweredQuestions(c.Request.Context(), topicID, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, questions, nil)
}

func (h *Handler) GetFollowingFeed(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit, offset := parsePagination(c)

	questions, err := h.svc.GetFollowingFeed(c.Request.Context(), userID, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, questions, nil)
}

func (h *Handler) GetForYouFeed(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit, offset := parsePagination(c)

	questions, err := h.svc.GetForYouFeed(c.Request.Context(), userID, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, questions, nil)
}

func (h *Handler) GetLocalFeed(c *gin.Context) {
	latStr := c.Query("lat")
	lngStr := c.Query("lng")
	if latStr == "" || lngStr == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "lat and lng are required", nil, nil)
		return
	}
	lat, err := strconv.ParseFloat(latStr, 64)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "invalid lat", nil, nil)
		return
	}
	lng, err := strconv.ParseFloat(lngStr, 64)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "invalid lng", nil, nil)
		return
	}
	radiusKm := 50
	if v := c.Query("radius_km"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			radiusKm = n
		}
	}
	limit, offset := parsePagination(c)

	questions, err := h.svc.GetLocalFeed(c.Request.Context(), lat, lng, radiusKm, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, questions, nil)
}

func (h *Handler) GetAnswerQueue(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit, offset := parsePagination(c)

	questions, err := h.svc.GetAnswerQueue(c.Request.Context(), userID, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, questions, nil)
}
