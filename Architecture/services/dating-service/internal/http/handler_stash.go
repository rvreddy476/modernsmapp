// HTTP handlers for /v1/dating/stash.
package http

import (
	"errors"
	"net/http"

	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type addStashRequest struct {
	CandidateID string `json:"candidate_id"`
}

// ListStash — GET /v1/dating/stash.
func (h *Handler) ListStash(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.ListStash(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// AddStash — POST /v1/dating/stash.
func (h *Handler) AddStash(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body addStashRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	candidate, err := uuid.Parse(body.CandidateID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid candidate_id", nil)
		return
	}
	st, err := h.svc.AddStash(c.Request.Context(), userID, candidate)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "ADD_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusCreated, st, nil)
}

// RemoveStash — DELETE /v1/dating/stash/:candidateId.
func (h *Handler) RemoveStash(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	candidate, ok := parseUUID(c, "candidateId")
	if !ok {
		return
	}
	reason := c.Query("reason")
	if err := h.svc.RemoveStash(c.Request.Context(), userID, candidate, reason); err != nil {
		if errors.Is(err, store.ErrStashNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "stash entry not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "DELETE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"removed": true}, nil)
}
