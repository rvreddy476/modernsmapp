package http

import (
	"net/http"
	"strings"

	"github.com/atpost/group-service/internal/service"
	"github.com/atpost/group-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ── V2 Group Posts ───────────────────────────────────────────

func (h *Handler) CreateGroupPostV2(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil)
		return
	}
	var req service.CreateGroupPostV2Params
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid request body", nil)
		return
	}
	/*
		THE COMPATIBILITY HINGE.

		With no also_post_to, this answers exactly as it always has: the bare
		post. Every existing caller reads that shape, including the mobile app,
		which is explicitly not being changed. Returning the batch result
		unconditionally would break all of them.

		Only a request that actually names extra groups gets the batch answer.
	*/
	if len(req.AlsoPostTo) > 0 {
		/*
			A stable key is required, not optional, and the reason is the axios
			interceptor on the web: it stamps a FRESH uuid header per attempt,
			so a retry carrying only a header key looks like a new intent and
			posts again to every group that already succeeded. The body field
			is the client's promise that this is the same press of the button.
		*/
		key := resolveIdempotencyKey(c, req.IdempotencyKey)
		if key == "" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
				"IDEMPOTENCY_KEY_REQUIRED",
				"posting to several groups needs an idempotency_key, so a retry completes the batch instead of duplicating it", nil)
			return
		}
		result, err := h.svc.CrossPost(c.Request.Context(), actorID, groupID, req, key)
		if err != nil {
			handleServiceError(c, err)
			return
		}
		api.JSON(c.Writer, http.StatusCreated, result, nil)
		return
	}

	post, err := h.svc.CreateGroupPostV2(c.Request.Context(), actorID, groupID, req)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, post, nil)
}

func (h *Handler) GetGroupFeedV2(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil)
		return
	}
	var channelID *uuid.UUID
	if ch := c.Query("channel_id"); ch != "" {
		parsed, err := uuid.Parse(ch)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid channel_id", nil)
			return
		}
		channelID = &parsed
	}
	limit, offset := parsePagination(c)
	posts, err := h.svc.GetGroupFeedV2(c.Request.Context(), actorID, groupID, channelID, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, posts, nil)
}

/*
SearchGroupPostsV2 backs the group page's search box.

GET /v1/groups/:groupId/posts/v2/search?q=<text>&limit=&offset=

The response is the service's []store.GroupPostV2 passed straight through —
the SAME shape and the same type as GetGroupFeedV2 returns. Not merely for
consistency: that type's MarshalJSON masks an anonymous post's author
(internal/store/anonymous.go), so a bespoke response struct here would publish
the real author_id of every anonymous post that matched.
*/
func (h *Handler) SearchGroupPostsV2(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil)
		return
	}
	// Same wording as SearchGroups so the two search endpoints refuse an empty
	// query identically.
	if strings.TrimSpace(c.Query("q")) == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Missing search query parameter 'q'", nil)
		return
	}
	limit, offset := parsePagination(c)
	posts, err := h.svc.SearchGroupPostsV2(c.Request.Context(), actorID, groupID, c.Query("q"), limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	if posts == nil {
		posts = []store.GroupPostV2{}
	}
	api.JSON(c.Writer, http.StatusOK, posts, nil)
}

func (h *Handler) GetGroupPostV2(c *gin.Context) {
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
	post, err := h.svc.GetGroupPostV2(c.Request.Context(), actorID, groupID, postID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, post, nil)
}

func (h *Handler) DeleteGroupPostV2(c *gin.Context) {
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
	if err := h.svc.DeleteGroupPostV2(c.Request.Context(), actorID, groupID, postID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

// ── V2 Engagement ────────────────────────────────────────────

func (h *Handler) SparkGroupPost(c *gin.Context) {
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
		IsSupernova bool `json:"is_supernova"`
	}
	_ = c.ShouldBindJSON(&req)
	if err := h.svc.SparkGroupPost(c.Request.Context(), actorID, groupID, postID, req.IsSupernova); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) UnsparkGroupPost(c *gin.Context) {
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
	if err := h.svc.UnsparkGroupPost(c.Request.Context(), actorID, groupID, postID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) StashGroupPost(c *gin.Context) {
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
	if err := h.svc.StashGroupPost(c.Request.Context(), actorID, groupID, postID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) UnstashGroupPost(c *gin.Context) {
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
	if err := h.svc.UnstashGroupPost(c.Request.Context(), actorID, groupID, postID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) RecordGroupPostView(c *gin.Context) {
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
	if err := h.svc.RecordGroupPostView(c.Request.Context(), actorID, groupID, postID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) EchoGroupPost(c *gin.Context) {
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
		EchoType string `json:"echo_type"`
	}
	_ = c.ShouldBindJSON(&req)
	if err := h.svc.EchoGroupPost(c.Request.Context(), actorID, groupID, postID, req.EchoType); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, gin.H{"status": "echoed"}, nil)
}

func (h *Handler) UnechoGroupPost(c *gin.Context) {
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
	if err := h.svc.UnechoGroupPost(c.Request.Context(), actorID, groupID, postID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "unechoed"}, nil)
}

// GetMyGroupsFeed returns the aggregated feed across every group the caller
// has joined — newest first, paginated with limit/offset.
func (h *Handler) GetMyGroupsFeed(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	limit, offset := parsePagination(c)
	posts, err := h.svc.GetMyGroupsFeed(c.Request.Context(), actorID, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, posts, nil)
}
