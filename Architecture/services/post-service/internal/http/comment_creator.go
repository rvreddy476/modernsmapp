package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

/*
	Creator comment tools (2026-09-27).

	  GET    /v1/comments/inbox?status=unanswered|all&content=videos|flicks|posts|all
	                            &sort=newest|relevant&cursor=&limit=
	           -> {data: [{comment, post:{id,title,content_type,cover_media_id}, author_replied}],
	               meta: {next_cursor}}
	  POST   /v1/comments/:commentId/heart   -> {comment_id, hearted_by_author:true}
	  DELETE /v1/comments/:commentId/heart   -> {comment_id, hearted_by_author:false}
	  PUT    /v1/comments/:commentId/pin     -> {comment_id, post_id, pinned:true}
	  DELETE /v1/comments/:commentId/pin     -> {comment_id, post_id, pinned:false}

	Heart and pin are the POST author's alone: 403 FORBIDDEN for anyone
	else, 404 NOT_FOUND for a comment outside the public thread, 422
	CANNOT_PIN_REPLY for a pin on a reply. Neither existed before this pass.
*/

// ListCreatorInbox is GET /v1/comments/inbox.
func (h *Handler) ListCreatorInbox(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}
	if limit > 50 {
		limit = 50
	}
	q := service.CreatorInboxQuery{
		Status:  strings.ToLower(strings.TrimSpace(c.Query("status"))),
		Content: strings.ToLower(strings.TrimSpace(c.Query("content"))),
		Sort:    strings.ToLower(strings.TrimSpace(c.Query("sort"))),
		Cursor:  c.Query("cursor"),
		Limit:   limit,
	}
	// Validated here as well as in the service so a bad filter is a 400
	// before any store is consulted (and the handler test can pin it).
	if q.Status != "" && q.Status != postgres.InboxStatusUnanswered && q.Status != postgres.InboxStatusAll {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", service.ErrInvalidInboxStatus.Error(), nil)
		return
	}
	if _, err := service.InboxContentTypes(q.Content); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	if q.Sort != "" && q.Sort != postgres.InboxSortNewest && q.Sort != postgres.InboxSortRelevant {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", service.ErrInvalidInboxSort.Error(), nil)
		return
	}
	rows, next, err := h.svc.ListCreatorInbox(c.Request.Context(), userID, q)
	if err != nil {
		h.writeCreatorCommentError(c, err)
		return
	}
	// Author presentation for the comment rows, the same hydration the
	// thread gets.
	comments := make([]postgres.Comment, len(rows))
	for i := range rows {
		comments[i] = rows[i].Comment
	}
	h.svc.HydrateCommentAuthors(c.Request.Context(), &userID, comments)
	for i := range rows {
		rows[i].Comment = comments[i]
	}
	var meta *api.Meta
	if next != "" {
		meta = &api.Meta{NextCursor: next}
	}
	api.JSON(c.Writer, http.StatusOK, rows, meta)
}

// HeartComment is POST /v1/comments/:commentId/heart.
func (h *Handler) HeartComment(c *gin.Context) {
	h.setCommentHeart(c, true)
}

// UnheartComment is DELETE /v1/comments/:commentId/heart.
func (h *Handler) UnheartComment(c *gin.Context) {
	h.setCommentHeart(c, false)
}

func (h *Handler) setCommentHeart(c *gin.Context, on bool) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	commentID, err := uuid.Parse(c.Param("commentId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid comment ID", nil)
		return
	}
	res, err := h.svc.SetCommentHeart(c.Request.Context(), userID, commentID, on)
	if err != nil {
		h.writeCreatorCommentError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}

// PinComment is PUT /v1/comments/:commentId/pin.
func (h *Handler) PinComment(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	commentID, err := uuid.Parse(c.Param("commentId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid comment ID", nil)
		return
	}
	res, err := h.svc.PinComment(c.Request.Context(), userID, commentID)
	if err != nil {
		h.writeCreatorCommentError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}

// UnpinComment is DELETE /v1/comments/:commentId/pin.
func (h *Handler) UnpinComment(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	commentID, err := uuid.Parse(c.Param("commentId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid comment ID", nil)
		return
	}
	res, err := h.svc.UnpinComment(c.Request.Context(), userID, commentID)
	if err != nil {
		h.writeCreatorCommentError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}

// writeCreatorCommentError maps the creator-tool refusals.
func (h *Handler) writeCreatorCommentError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrNotPostAuthor):
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "FORBIDDEN", "Only the post's author can do this", nil)
	case errors.Is(err, service.ErrCannotPinReply):
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, "CANNOT_PIN_REPLY", err.Error(), nil)
	case errors.Is(err, service.ErrInvalidInboxStatus), errors.Is(err, service.ErrInvalidInboxContent), errors.Is(err, service.ErrInvalidInboxSort):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
	case errors.Is(err, service.ErrAuthoringStoreUnavailable):
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "STORE_UNAVAILABLE", err.Error(), nil)
	default:
		// COMMENT_NOT_FOUND and the post-visibility refusals are the
		// thread's own 404.
		h.writeCommentLookupError(c, err)
	}
}
