package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/qa-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

func (h *Handler) ListTopics(c *gin.Context) {
	limit, offset := parsePagination(c)
	featuredOnly := c.Query("featured") == "true"

	topics, err := h.svc.ListTopics(c.Request.Context(), limit, offset, featuredOnly)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, topics, nil)
}

func (h *Handler) GetTopic(c *gin.Context) {
	topicID, ok := parseUUID(c, "topicId")
	if !ok {
		return
	}

	topic, err := h.svc.GetTopic(c.Request.Context(), topicID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "topic not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, topic, nil)
}

func (h *Handler) GetTopicBySlug(c *gin.Context) {
	slug := c.Param("slug")
	if slug == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "slug is required", nil, nil)
		return
	}

	topic, err := h.svc.GetTopicBySlug(c.Request.Context(), slug)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "topic not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, topic, nil)
}

func (h *Handler) CreateTopic(c *gin.Context) {
	var body store.CreateTopicParams
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}

	topic, err := h.svc.CreateTopic(c.Request.Context(), body)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "CREATE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, topic, nil)
}

func (h *Handler) GetTopicQuestions(c *gin.Context) {
	topicID, ok := parseUUID(c, "topicId")
	if !ok {
		return
	}
	limit, offset := parsePagination(c)
	sortBy := c.DefaultQuery("sort", "newest")

	questions, err := h.svc.GetTopicQuestions(c.Request.Context(), topicID, sortBy, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, questions, nil)
}

func (h *Handler) GetTopContributors(c *gin.Context) {
	topicID, ok := parseUUID(c, "topicId")
	if !ok {
		return
	}
	limit := 10
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 50 {
			limit = n
		}
	}

	contributors, err := h.svc.GetTopContributors(c.Request.Context(), topicID, limit)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, contributors, nil)
}
