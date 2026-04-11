package http

import (
	"io"
	"net/http"
	"strconv"

	"github.com/atpost/qa-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

func (h *Handler) ListCommunityQuestions(c *gin.Context) {
	communityID, ok := parseUUID(c, "communityId")
	if !ok {
		return
	}

	viewerID := optionalUserID(c)
	limit, offset := parsePagination(c)

	questions, availableTopics, settings, err := h.svc.ListCommunityQuestions(
		c.Request.Context(),
		communityID,
		viewerID,
		c.Query("topic"),
		c.DefaultQuery("sort", "recent"),
		c.Query("status"),
		limit,
		offset,
	)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}

	serializedTopics := make([]map[string]any, 0, len(availableTopics))
	for _, topic := range availableTopics {
		serializedTopics = append(serializedTopics, map[string]any{
			"id":             topic.Topic.ID,
			"name":           topic.Topic.Name,
			"slug":           topic.Topic.Slug,
			"description":    topic.Topic.Description,
			"question_count": topic.QuestionCount,
		})
	}

	api.JSON(c.Writer, http.StatusOK, map[string]any{
		"questions":        questions,
		"available_topics": serializedTopics,
		"pagination": map[string]any{
			"limit":    limit,
			"offset":   offset,
			"has_more": len(questions) == limit,
		},
		"community_qa_settings": settings,
	}, nil)
}

func (h *Handler) GetCommunityQASettings(c *gin.Context) {
	communityID, ok := parseUUID(c, "communityId")
	if !ok {
		return
	}

	settings, err := h.svc.GetCommunityQASettings(c.Request.Context(), communityID, optionalUserID(c))
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, settings, nil)
}

func (h *Handler) UpdateCommunityQASettings(c *gin.Context) {
	communityID, ok := parseUUID(c, "communityId")
	if !ok {
		return
	}
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var body service.UpdateCommunityQASettingsParams
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}

	settings, err := h.svc.UpdateCommunityQASettings(c.Request.Context(), communityID, userID, body)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "UPDATE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, settings, nil)
}

func (h *Handler) GetCommunityPopularTopics(c *gin.Context) {
	communityID, ok := parseUUID(c, "communityId")
	if !ok {
		return
	}

	limit := 10
	if raw := c.Query("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 50 {
			limit = parsed
		}
	}

	topics, err := h.svc.GetCommunityPopularTopics(c.Request.Context(), communityID, optionalUserID(c), limit)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{
		"community_id":   communityID,
		"popular_topics": topics,
	}, nil)
}

func (h *Handler) PinCommunityQuestion(c *gin.Context) {
	communityID, ok := parseUUID(c, "communityId")
	if !ok {
		return
	}
	questionID, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var body struct {
		Pinned *bool  `json:"pinned"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&body); err != nil && err != io.EOF {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}

	pinned := true
	if body.Pinned != nil {
		pinned = *body.Pinned
	}

	if err := h.svc.SetCommunityQuestionPinned(c.Request.Context(), communityID, questionID, userID, pinned, body.Reason); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "UPDATE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{
		"community_id": communityID,
		"question_id":  questionID,
		"is_pinned":    pinned,
	}, nil)
}

func (h *Handler) UnpinCommunityQuestion(c *gin.Context) {
	communityID, ok := parseUUID(c, "communityId")
	if !ok {
		return
	}
	questionID, ok := parseUUID(c, "questionId")
	if !ok {
		return
	}
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	if err := h.svc.SetCommunityQuestionPinned(c.Request.Context(), communityID, questionID, userID, false, ""); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "UPDATE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{
		"community_id": communityID,
		"question_id":  questionID,
		"is_pinned":    false,
	}, nil)
}
