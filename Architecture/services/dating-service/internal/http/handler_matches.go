// HTTP handlers for /v1/dating/matches and the internal first-message
// callback from message-service.
package http

import (
	"errors"
	"net/http"

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
