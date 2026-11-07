package http

import (
	"net/http"

	"github.com/atpost/group-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ── Word Blocklist ───────────────────────────────────────────

func (h *Handler) GetWordBlocklist(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil)
		return
	}
	words, err := h.svc.GetWordBlocklist(c.Request.Context(), actorID, groupID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	if words == nil {
		words = []string{}
	}
	api.JSON(c.Writer, http.StatusOK, words, nil)
}

func (h *Handler) AddWordToBlocklist(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil)
		return
	}
	var req struct {
		Word string `json:"word" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	if err := h.svc.AddWordToBlocklist(c.Request.Context(), actorID, groupID, req.Word); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) RemoveWordFromBlocklist(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil)
		return
	}
	word := c.Param("word")
	if err := h.svc.RemoveWordFromBlocklist(c.Request.Context(), actorID, groupID, word); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

// ── Post Approval Queue ──────────────────────────────────────

func (h *Handler) ListPendingPosts(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil)
		return
	}
	limit, offset := parsePagination(c)
	posts, err := h.svc.ListPendingPosts(c.Request.Context(), actorID, groupID, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	// An empty queue is [], never null: a client that maps over the result
	// must not have to guard for nil, and ListGroupEvents already shows what
	// happens when one forgets.
	if posts == nil {
		posts = []store.GroupPostV2{}
	}
	api.JSON(c.Writer, http.StatusOK, posts, nil)
}

func (h *Handler) ApprovePendingPost(c *gin.Context) {
	h.reviewPendingPost(c, true)
}

func (h *Handler) RejectPendingPost(c *gin.Context) {
	h.reviewPendingPost(c, false)
}

func (h *Handler) reviewPendingPost(c *gin.Context, approve bool) {
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
	if approve {
		err = h.svc.ApprovePendingPost(c.Request.Context(), actorID, groupID, postID)
	} else {
		err = h.svc.RejectPendingPost(c.Request.Context(), actorID, groupID, postID)
	}
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]bool{"ok": true}, nil)
}
