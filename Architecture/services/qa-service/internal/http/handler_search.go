package http

import (
	"net/http"
	"strings"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// SearchQuestions implements GET /v1/qa/search?q=&community_id=&topic_id=&limit=&offset=
// Full-text, indexed, bounded and rate limited for signed-out callers
// (search_limit.go, store/search_match.go; audit M6).
func (h *Handler) SearchQuestions(c *gin.Context) {
	q := clipSearchQuery(strings.TrimSpace(c.Query("q")))
	if len(q) < 2 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_PARAM", "q must be at least 2 characters", nil)
		return
	}

	var communityID *uuid.UUID
	if raw := c.Query("community_id"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_PARAM", "invalid community_id", nil)
			return
		}
		communityID = &parsed
	}

	var topicID *uuid.UUID
	if raw := c.Query("topic_id"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_PARAM", "invalid topic_id", nil)
			return
		}
		topicID = &parsed
	}

	limit, offset := clampSearchPage(parsePagination(c))
	if !h.allowSearch(c) {
		return
	}

	questions, err := h.svc.SearchQuestions(c.Request.Context(), q, communityID, topicID, optionalUserID(c), limit, offset)
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
