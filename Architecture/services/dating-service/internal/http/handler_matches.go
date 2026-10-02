// HTTP handlers for /v1/dating/matches and the internal first-message
// callback from message-service.
package http

import (
	"errors"
	"net/http"

	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListMatches — GET /v1/dating/matches?status=...
func (h *Handler) ListMatches(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	status := c.DefaultQuery("status", "all")
	out, err := h.svc.ListMatches(c.Request.Context(), userID, status)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// GetMatch — GET /v1/dating/matches/:id.
func (h *Handler) GetMatch(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	matchID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	// Lane D3: 403 for a non-participant; 404 when the pair is blocked
	// either way or the other participant is deleted or suspended.
	m, err := h.svc.GetMatchViewForUser(c.Request.Context(), matchID, userID)
	if err != nil {
		if errors.Is(err, store.ErrMatchNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "match not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, m, nil)
}

// CloseMatch — POST /v1/dating/matches/:id/close.
func (h *Handler) CloseMatch(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	matchID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.CloseMatch(c.Request.Context(), matchID, userID); err != nil {
		if errors.Is(err, store.ErrMatchNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "match not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "CLOSE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"closed": true}, nil)
}

// ExtendMatch — POST /v1/dating/matches/:id/extend (premium only).
func (h *Handler) ExtendMatch(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	matchID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.ExtendMatchFor(c.Request.Context(), matchID, userID)
	if err != nil {
		if errors.Is(err, store.ErrMatchNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "match not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "EXTEND_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// openingAnswerRequest is the body of POST /matches/:id/opening-answer.
type openingAnswerRequest struct {
	QuestionID string `json:"question_id"`
	Answer     string `json:"answer"`
}

// PostOpeningAnswer — POST /v1/dating/matches/:id/opening-answer
//
// Mechanic M5: the person waiting on a first-move match answers one of the
// first mover's opening questions; the answer becomes the first message.
func (h *Handler) PostOpeningAnswer(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	matchID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	var body openingAnswerRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	qid, err := uuid.Parse(body.QuestionID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid question_id", nil)
		return
	}
	out, err := h.svc.SendOpeningAnswer(c.Request.Context(), matchID, userID, qid, body.Answer)
	if err != nil {
		if errors.Is(err, store.ErrMatchNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "match not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "OPENING_ANSWER_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusCreated, out, nil)
}

// firstMoveRequest is the body of PUT /first-move. Absent fields are left
// as they are; "questions": [] removes every question.
type firstMoveRequest struct {
	Enabled   *bool     `json:"enabled"`
	Questions *[]string `json:"questions"`
}

// GetFirstMove — GET /v1/dating/first-move (mechanic M5).
func (h *Handler) GetFirstMove(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.GetFirstMove(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// PutFirstMove — PUT /v1/dating/first-move (mechanic M5).
func (h *Handler) PutFirstMove(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body firstMoveRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	var questions []string
	if body.Questions != nil {
		questions = *body.Questions
		if questions == nil {
			questions = []string{}
		}
	}
	out, err := h.svc.PutFirstMove(c.Request.Context(), userID, body.Enabled, questions)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "UPDATE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// MatchFirstMessage — POST /v1/dating/internal/matches/:id/first-message
// Service-only endpoint for the chat/message consumer when a message is
// sent in a dating-match conversation. Registered behind
// requireServiceCaller(OpMatchFirstMessage): a service token or the legacy
// internal key, and never a request carrying a gateway user identity. The
// old /v1/dating/matches/:id/first-message path answers 410.
func (h *Handler) MatchFirstMessage(c *gin.Context) {
	matchID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	var body struct {
		ActorID string `json:"actor_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	actor, err := uuid.Parse(body.ActorID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid actor_id", nil)
		return
	}
	if err := h.svc.RecordFirstMessage(c.Request.Context(), matchID, actor); err != nil {
		if errors.Is(err, store.ErrMatchNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "match not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "RECORD_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"recorded": true}, nil)
}

// readReceiptsRequest is the body of PUT /read-receipts.
type readReceiptsRequest struct {
	Enabled bool `json:"enabled"`
}

// GetReadReceipts — GET /v1/dating/read-receipts (mechanic M9).
func (h *Handler) GetReadReceipts(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.GetReadReceipts(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// PutReadReceipts — PUT /v1/dating/read-receipts {enabled} (mechanic M9).
func (h *Handler) PutReadReceipts(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body readReceiptsRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.PutReadReceipts(c.Request.Context(), userID, body.Enabled)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "UPDATE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// GetPastMatches — GET /v1/dating/past-matches (mechanic M19): the caller's
// matches that ended in the last 30 days, each reportable through
// POST /v1/dating/safety/report. 404 MECHANIC_NOT_ENABLED while the flag is off.
func (h *Handler) GetPastMatches(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.PastMatches(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	c.JSON(http.StatusOK, out)
}

// PostDateFeedback — POST /v1/dating/matches/:id/date-feedback (mechanic
// M14): how the date went. 201 with offer_report when the caller did not
// feel safe; 400 INVALID_DATE_FEEDBACK; 404 for someone else's match;
// 429 DATE_FEEDBACK_LIMIT.
func (h *Handler) PostDateFeedback(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	matchID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid match id", nil)
		return
	}
	var body service.DateFeedbackInput
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.PostDateFeedback(c.Request.Context(), userID, matchID, body)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "DATE_FEEDBACK_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusCreated, out, nil)
}

// GetDateCheckins — GET /v1/dating/date-checkins (mechanic M14): the asks
// still waiting for the caller's answer.
func (h *Handler) GetDateCheckins(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.DateCheckins(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// PostKindCheck — POST /v1/dating/kind-check {text} (mechanic M13).
func (h *Handler) PostKindCheck(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.KindCheck(c.Request.Context(), userID, body.Text)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "KIND_CHECK_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// PostBothered — POST /v1/dating/matches/:id/bothered {bothered} (mechanic
// M13): the caller's answer to "did this message bother you?".
func (h *Handler) PostBothered(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	matchID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid match id", nil)
		return
	}
	var body struct {
		Bothered *bool `json:"bothered"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Bothered == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "bothered is required", nil)
		return
	}
	out, err := h.svc.Bothered(c.Request.Context(), userID, matchID, *body.Bothered)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "FEEDBACK_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusCreated, out, nil)
}

// GetCommentFilter — GET /v1/dating/comment-filter (mechanic M13).
func (h *Handler) GetCommentFilter(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.GetCommentFilter(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// PutCommentFilter — PUT /v1/dating/comment-filter {filter_unkind, words}
// (mechanic M13). 400 INVALID_COMMENT_FILTER.
func (h *Handler) PutCommentFilter(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body service.CommentFilterView
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.PutCommentFilter(c.Request.Context(), userID, body)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "UPDATE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}
