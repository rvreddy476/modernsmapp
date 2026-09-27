package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

/*
	Replies for everyone and emoji reactions on comments (2026-09-27).

	  GET    /v1/comments/:commentId/replies?limit=20&cursor=
	           → {data: [Comment], meta: {next_cursor}}, oldest first
	  PUT    /v1/comments/:commentId/reaction {emoji}
	           → {data: {emoji, reaction_count, reactions, viewer_reaction}}
	  DELETE /v1/comments/:commentId/reaction
	           → 200 with the same summary, emoji and viewer_reaction null
	             (chosen over 204 so the client can repaint from one shape)

	A comment that is not visible to the viewer (held, hidden, deleted, or on
	a post the viewer may not see) is a 404 on every route. INVALID_EMOJI is
	a 400 for an empty or over-long emoji.
*/

// commentReactionRequest is the PUT body.
type commentReactionRequest struct {
	Emoji string `json:"emoji"`
}

// ListReplies is the paged, oldest-first reply list. Anonymous viewers are
// allowed (the gateway decides whether a token is required); a viewer id
// drives moderation visibility and viewer_reaction.
func (h *Handler) ListReplies(c *gin.Context) {
	commentID, err := uuid.Parse(c.Param("commentId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid comment ID", nil)
		return
	}
	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}
	if limit > 100 {
		limit = 100
	}
	var viewerID *uuid.UUID
	if v := c.GetHeader("X-User-Id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			viewerID = &id
		}
	}

	replies, nextCursor, err := h.svc.ListReplies(c.Request.Context(), commentID, viewerID, c.DefaultQuery("cursor", ""), limit)
	if err != nil {
		h.writeCommentLookupError(c, err)
		return
	}
	if replies == nil {
		replies = []postgres.Comment{}
	}
	h.svc.HydrateCommentAuthors(c.Request.Context(), viewerID, replies)

	var meta *api.Meta
	if nextCursor != "" {
		meta = &api.Meta{NextCursor: nextCursor}
	}
	api.JSON(c.Writer, http.StatusOK, replies, meta)
}

// SetCommentReaction upserts the viewer's single reaction.
func (h *Handler) SetCommentReaction(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	commentID, err := uuid.Parse(c.Param("commentId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid comment ID", nil)
		return
	}
	var req commentReactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	// Validated here as well as in the service so a malformed body never
	// reaches the store (and the handler test can pin the 400 without one).
	emoji, err := service.NormalizeCommentEmoji(req.Emoji)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_EMOJI", "emoji must be 1 to 16 characters", nil)
		return
	}

	summary, err := h.svc.SetCommentReaction(c.Request.Context(), commentID, userID, emoji)
	if err != nil {
		h.writeCommentLookupError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, summary, nil)
}

// RemoveCommentReaction deletes the viewer's reaction and answers the
// summary with emoji / viewer_reaction null.
func (h *Handler) RemoveCommentReaction(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	commentID, err := uuid.Parse(c.Param("commentId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid comment ID", nil)
		return
	}
	summary, err := h.svc.RemoveCommentReaction(c.Request.Context(), commentID, userID)
	if err != nil {
		h.writeCommentLookupError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, summary, nil)
}

// writeCommentLookupError maps the shared refusals of the comment routes.
func (h *Handler) writeCommentLookupError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrInvalidEmoji):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_EMOJI", "emoji must be 1 to 16 characters", nil)
	case err.Error() == "RATE_LIMITED":
		api.ErrorWithContext(ctx, c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", "Too many reactions, please slow down", nil)
	case err.Error() == "COMMENT_NOT_FOUND", errors.Is(err, service.ErrPostNotFound), errors.Is(err, service.ErrPostNotVisible):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "Comment not found", nil)
	default:
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
	}
}
