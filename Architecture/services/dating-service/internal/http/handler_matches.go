// HTTP handlers for /v1/dating/matches and the internal first-message
// callback from message-service.
package http

import (
	"errors"
	"net/http"
	"os"

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
	m, err := h.svc.GetMatch(c.Request.Context(), matchID)
	if err != nil {
		if errors.Is(err, store.ErrMatchNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "match not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	if userID != m.UserA && userID != m.UserB {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "not a participant", nil)
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
	if err := h.svc.ExtendMatch(c.Request.Context(), matchID, userID); err != nil {
		if errors.Is(err, store.ErrMatchNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "match not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "EXTEND_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"extended": true, "extra_days": 7}, nil)
}

// MatchFirstMessage — POST /v1/dating/matches/:id/first-message
// Internal-only endpoint called by the message-service consumer when a
// message is sent in a dating-match conversation. Authenticated by the
// X-Internal-Service-Key header (env: INTERNAL_SERVICE_KEY).
func (h *Handler) MatchFirstMessage(c *gin.Context) {
	if !verifyInternalKey(c) {
		return
	}
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

// verifyInternalKey checks the X-Internal-Service-Key header against the
// configured INTERNAL_SERVICE_KEY env var. When the env var is unset the
// endpoint is locked (returns 401) so a missing config doesn't open a hole.
func verifyInternalKey(c *gin.Context) bool {
	expected := os.Getenv("INTERNAL_SERVICE_KEY")
	if expected == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "AUTH_REQUIRED", "internal endpoint disabled", nil)
		return false
	}
	if c.GetHeader("X-Internal-Service-Key") != expected {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "AUTH_REQUIRED", "invalid internal key", nil)
		return false
	}
	return true
}
