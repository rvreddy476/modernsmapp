package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ── V2 Comments ──────────────────────────────────────────────

func (h *Handler) ListGroupPostComments(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid post id", nil)
		return
	}
	limit, offset := parsePagination(c)
	comments, err := h.svc.ListGroupPostComments(c.Request.Context(), actorID, groupID, postID, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, comments, nil)
}

func (h *Handler) AddGroupPostComment(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid post id", nil)
		return
	}
	var req struct {
		Body     string     `json:"body" binding:"required"`
		ParentID *uuid.UUID `json:"parent_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "body is required", nil)
		return
	}
	comment, err := h.svc.AddGroupPostComment(c.Request.Context(), actorID, groupID, postID, req.Body, req.ParentID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, comment, nil)
}

func (h *Handler) DeleteGroupPostComment(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid post id", nil)
		return
	}
	commentID, err := uuid.Parse(c.Param("commentId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid comment id", nil)
		return
	}
	if err := h.svc.DeleteGroupPostComment(c.Request.Context(), actorID, groupID, postID, commentID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}
