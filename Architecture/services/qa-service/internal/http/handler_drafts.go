package http

import (
	"net/http"

	"github.com/atpost/qa-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// --- Question drafts ---

type questionDraftRequest struct {
	ID          *string  `json:"id,omitempty"`
	CommunityID *string  `json:"communityId,omitempty"`
	Title       string   `json:"title"`
	Body        string   `json:"body"`
	Tags        []string `json:"tags"`
	TopicIDs    []string `json:"topicIds"`
	IsAnonymous bool     `json:"isAnonymous,omitempty"`
}

func (h *Handler) UpsertQuestionDraft(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var body questionDraftRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	params := store.UpsertQuestionDraftParams{
		AuthorID:    userID,
		Title:       body.Title,
		Body:        body.Body,
		Tags:        body.Tags,
		IsAnonymous: body.IsAnonymous,
	}

	if body.ID != nil && *body.ID != "" {
		id, ok := parseUUIDString(c, *body.ID, "id")
		if !ok {
			return
		}
		params.ID = &id
	}

	if body.CommunityID != nil && *body.CommunityID != "" {
		cid, ok := parseUUIDString(c, *body.CommunityID, "communityId")
		if !ok {
			return
		}
		params.CommunityID = &cid
	}

	if len(body.TopicIDs) > 0 {
		topicIDs := make([]uuid.UUID, 0, len(body.TopicIDs))
		for _, raw := range body.TopicIDs {
			id, err := uuid.Parse(raw)
			if err != nil {
				api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_PARAM", "invalid topicId: "+raw, nil)
				return
			}
			topicIDs = append(topicIDs, id)
		}
		params.TopicIDs = topicIDs
	}

	draft, err := h.svc.UpsertQuestionDraft(c.Request.Context(), params)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "DRAFT_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, draft, nil)
}

func (h *Handler) ListQuestionDrafts(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit, offset := parsePagination(c)

	drafts, err := h.svc.ListQuestionDrafts(c.Request.Context(), userID, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{
		"drafts": drafts,
		"pagination": map[string]any{
			"limit":    limit,
			"offset":   offset,
			"has_more": len(drafts) == limit,
		},
	}, nil)
}

func (h *Handler) DeleteQuestionDraft(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	draftID, ok := parseUUID(c, "draftId")
	if !ok {
		return
	}

	if err := h.svc.DeleteQuestionDraft(c.Request.Context(), draftID, userID); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "DELETE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

// --- Answer drafts ---

type answerDraftRequest struct {
	ID          *string `json:"id,omitempty"`
	QuestionID  string  `json:"questionId"`
	Body        string  `json:"body"`
	IsAnonymous bool    `json:"isAnonymous,omitempty"`
}

func (h *Handler) UpsertAnswerDraft(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var body answerDraftRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	if body.QuestionID == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "questionId is required", nil)
		return
	}
	questionID, ok := parseUUIDString(c, body.QuestionID, "questionId")
	if !ok {
		return
	}

	params := store.UpsertAnswerDraftParams{
		AuthorID:    userID,
		QuestionID:  questionID,
		Body:        body.Body,
		IsAnonymous: body.IsAnonymous,
	}
	if body.ID != nil && *body.ID != "" {
		id, ok := parseUUIDString(c, *body.ID, "id")
		if !ok {
			return
		}
		params.ID = &id
	}

	draft, err := h.svc.UpsertAnswerDraft(c.Request.Context(), params)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "DRAFT_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, draft, nil)
}

func (h *Handler) ListAnswerDrafts(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit, offset := parsePagination(c)

	drafts, err := h.svc.ListAnswerDrafts(c.Request.Context(), userID, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{
		"drafts": drafts,
		"pagination": map[string]any{
			"limit":    limit,
			"offset":   offset,
			"has_more": len(drafts) == limit,
		},
	}, nil)
}

func (h *Handler) DeleteAnswerDraft(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	draftID, ok := parseUUID(c, "draftId")
	if !ok {
		return
	}

	if err := h.svc.DeleteAnswerDraft(c.Request.Context(), draftID, userID); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "DELETE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}
