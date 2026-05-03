package http

import (
	"errors"
	"net/http"

	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// GetPromptCatalog returns the static prompt catalog (v1).
func (h *Handler) GetPromptCatalog(c *gin.Context) {
	api.JSON(c.Writer, http.StatusOK, h.svc.PromptCatalog(c.Request.Context()), nil)
}

// ListPrompts returns the caller's answered prompts.
func (h *Handler) ListPrompts(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	prompts, err := h.svc.ListPrompts(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, prompts, nil)
}

// UpsertPrompt sets or updates the caller's answer for a prompt.
func (h *Handler) UpsertPrompt(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	promptID, ok := parseIntParam(c, "promptId")
	if !ok {
		return
	}
	var body struct {
		Answer string `json:"answer"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	prompt, err := h.svc.UpsertPrompt(c.Request.Context(), userID, promptID, body.Answer)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "UPSERT_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, prompt, nil)
}

// DeletePrompt removes the caller's answer for a prompt.
func (h *Handler) DeletePrompt(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	promptID, ok := parseIntParam(c, "promptId")
	if !ok {
		return
	}
	if err := h.svc.DeletePrompt(c.Request.Context(), userID, promptID); err != nil {
		if errors.Is(err, store.ErrPromptNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "prompt not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "DELETE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "deleted"}, nil)
}
