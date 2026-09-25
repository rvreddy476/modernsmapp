package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// SetGroupPostReaction — PUT /v1/groups/:groupId/posts/v2/:postId/reaction
//
// Body: {"reaction": "like" | "love" | "smile" | "wow" | "sad" | "angry"}.
// Sets the viewer's one reaction, replacing any previous one; the same
// reaction again is a no-op. 200 with the authoritative state; 400 on a
// malformed body; 422 VALIDATION_ERROR outside the allowlist; 403/404 per the
// group's access rule; nothing is written on any refusal.
func (h *Handler) SetGroupPostReaction(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	var req struct {
		Reaction string `json:"reaction" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "reaction is required", nil)
		return
	}
	state, err := h.svc.SetGroupPostReaction(c.Request.Context(), actorID, groupID, postID, req.Reaction)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, state, nil)
}

// RemoveGroupPostReaction — DELETE /v1/groups/:groupId/posts/v2/:postId/reaction
//
// Removes the viewer's reaction whatever it is. 200 with the authoritative
// state even when there was nothing to remove, so a retry is safe.
func (h *Handler) RemoveGroupPostReaction(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	state, err := h.svc.RemoveGroupPostReaction(c.Request.Context(), actorID, groupID, postID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, state, nil)
}
