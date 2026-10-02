package http

import (
	"errors"
	"net/http"

	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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
	// Lane D10: an empty list is [], never null.
	if prompts == nil {
		prompts = []store.Prompt{}
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

// PutPromptClip — PUT /v1/dating/prompts/:promptId/clip {media_id}
// (mechanic M15): attach an uploaded voice or video clip.
func (h *Handler) PutPromptClip(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	promptID, ok := parseIntParam(c, "promptId")
	if !ok {
		return
	}
	var body struct {
		MediaID uuid.UUID `json:"media_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.MediaID == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "media_id is required", nil)
		return
	}
	out, err := h.svc.PutPromptClip(c.Request.Context(), userID, promptID, body.MediaID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "CLIP_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// DeletePromptClip — DELETE /v1/dating/prompts/:promptId/clip (mechanic M15).
func (h *Handler) DeletePromptClip(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	promptID, ok := parseIntParam(c, "promptId")
	if !ok {
		return
	}
	if err := h.svc.DeletePromptClip(c.Request.Context(), userID, promptID); err != nil {
		if errors.Is(err, store.ErrPromptNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "prompt not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "DELETE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "deleted"}, nil)
}

// GetPromptClip — GET /v1/dating/people/:userId/prompts/:promptId/clip
// (mechanic M15): 307 to a short-lived URL for an approved clip the caller
// may see; 404 otherwise, whatever the reason.
func (h *Handler) GetPromptClip(c *gin.Context) {
	viewerID, ok := getUserID(c)
	if !ok {
		return
	}
	ownerID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid user id", nil)
		return
	}
	promptID, ok := parseIntParam(c, "promptId")
	if !ok {
		return
	}
	u, err := h.svc.PromptClipURL(c.Request.Context(), viewerID, ownerID, promptID)
	if err != nil {
		if errors.Is(err, store.ErrPromptNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "CLIP_NOT_FOUND", "clip not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "CLIP_FAILED")
		return
	}
	c.Header("Cache-Control", "private, max-age=60")
	c.Redirect(http.StatusTemporaryRedirect, u)
}

// ListPendingClips — GET /v1/dating/admin/clips/pending (mechanic M15).
func (h *Handler) ListPendingClips(c *gin.Context) {
	out, err := h.svc.ListPendingClipReviews(c.Request.Context(), 50)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// ReviewPromptClip — POST /v1/dating/admin/clips/review {user_id, prompt_id,
// decision, reason} (mechanic M15): a moderator approves or rejects a clip.
func (h *Handler) ReviewPromptClip(c *gin.Context) {
	adminID, ok := adminActor(c)
	if !ok {
		return
	}
	var body struct {
		UserID   uuid.UUID `json:"user_id"`
		PromptID int       `json:"prompt_id"`
		Decision string    `json:"decision"`
		Reason   *string   `json:"reason"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.UserID == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "user_id, prompt_id and decision are required", nil)
		return
	}
	if err := h.svc.ReviewPromptClip(c.Request.Context(), adminID, body.UserID, body.PromptID, body.Decision, body.Reason); err != nil {
		if errors.Is(err, store.ErrPromptNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "clip not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "REVIEW_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": body.Decision}, nil)
}
