package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

func (h *Handler) ListTopics(c *gin.Context) {
	limit, offset := parsePagination(c)
	featuredOnly := c.Query("featured") == "true"

	topics, err := h.svc.ListTopics(c.Request.Context(), limit, offset, featuredOnly)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "topic not found", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, topic, nil)
}

func (h *Handler) GetTopicBySlug(c *gin.Context) {
	slug := c.Param("slug")
	if slug == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_PARAM", "slug is required", nil)
		return
	}

	topic, err := h.svc.GetTopicBySlug(c.Request.Context(), slug)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "topic not found", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, topic, nil)
}

// Topic writes are the admin console (admin_topics.go, audit M3);
// POST /v1/qa/topics answers 410.

func (h *Handler) GetTopicQuestions(c *gin.Context) {
	topicID, ok := parseUUID(c, "topicId")
	if !ok {
		return
	}
	limit, offset := parsePagination(c)
	sortBy := c.DefaultQuery("sort", "newest")

	questions, err := h.svc.GetTopicQuestions(c.Request.Context(), topicID, optionalUserID(c), sortBy, limit, offset)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
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
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, contributors, nil)
}
