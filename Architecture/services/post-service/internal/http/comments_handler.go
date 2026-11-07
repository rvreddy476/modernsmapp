// comments_handler.go holds the comment routes on a post: add, list (and
// around one comment), reply, like / dislike, delete and edit. Moved out of
// handler.go unchanged.

package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/post-service/internal/http/middleware"
	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type CommentRequest struct {
	Text string `json:"text" binding:"required"`
}

func (h *Handler) AddComment(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}

	// NoComments check is now inside the service (audit H2 — no
	// more handler-level GetPost just to read engagement flags).
	var req CommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "comment text cannot be blank", nil)
		return
	}

	// The client key and a fingerprint of the NORMALIZED text make the insert
	// durably idempotent in PostgreSQL. The Redis middleware in front of this
	// handler is a fast concurrency gate; it cannot be the authority, because
	// the comment commits before it records anything.
	//
	// The fingerprint covers the text rather than the raw body so that
	// insignificant encoding differences between two attempts at the same
	// intent do not read as a changed payload.
	clientKey := c.GetHeader("Idempotency-Key")
	fingerprint := middleware.CommentFingerprint(postID.String(), req.Text)

	comment, err := h.svc.CreateCommentPG(c.Request.Context(), postID, userID, req.Text, clientKey, fingerprint)
	if err != nil {
		switch {
		case writeMembersOnlyError(c, err):
		case errors.Is(err, postgres.ErrIdempotencyKeyReused):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED", "This Idempotency-Key was already used for a different comment.", nil)
		case err.Error() == "RATE_LIMITED":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", "Too many comments, please slow down", nil)
		case errors.Is(err, service.ErrCommentsDisabled):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "COMMENTS_DISABLED", "Comments are disabled on this post", nil)
		case errors.Is(err, service.ErrCommentsRestricted):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "COMMENTS_RESTRICTED", "Only friends can comment on this post", nil)
		case errors.Is(err, service.ErrCommentsFollowersOnly):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "COMMENTS_FOLLOWERS_ONLY", "Only followers can comment on this post", nil)
		case errors.Is(err, service.ErrPostNotFound), errors.Is(err, service.ErrPostNotVisible):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "POST_NOT_FOUND", "post not found", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusCreated, comment, nil)
}

func (h *Handler) ListComments(c *gin.Context) {
	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}

	cursor := c.DefaultQuery("cursor", "")
	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}

	var viewerID *uuid.UUID
	if v := c.GetHeader("X-User-Id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			viewerID = &id
		}
	}

	// ?sort=top|newest (2026-09-27): newest is the historic order and the
	// default; top ranks by reactions, replies, recency. Anything else is
	// a 400 rather than a silent fallback. The pinned comment leads both.
	sort := strings.ToLower(strings.TrimSpace(c.Query("sort")))
	switch sort {
	case "", postgres.CommentSortNewest, postgres.CommentSortTop:
	default:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "sort must be top or newest", nil)
		return
	}

	comments, nextCursor, err := h.svc.ListCommentsSortedPG(c.Request.Context(), postID, viewerID, cursor, limit, sort)
	if err != nil {
		// The post's read gate (2026-09-28): 404 for a post the viewer may
		// not see (private shares honoured), the age codes for an 18+ one.
		if writeReadGateError(c, err) {
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	if comments == nil {
		comments = []postgres.Comment{}
	}
	h.svc.HydrateCommentAuthors(c.Request.Context(), viewerID, comments)

	var meta *api.Meta
	if nextCursor != "" {
		meta = &api.Meta{NextCursor: nextCursor}
	}

	api.JSON(c.Writer, http.StatusOK, comments, meta)
}

func (h *Handler) ListCommentsAround(c *gin.Context) {
	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}

	commentIDStr := c.Param("commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid comment ID", nil)
		return
	}

	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}

	var viewerID *uuid.UUID
	if v := c.GetHeader("X-User-Id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			viewerID = &id
		}
	}

	comments, err := h.svc.GetCommentsAroundPG(c.Request.Context(), postID, commentID, viewerID, limit)
	if err != nil {
		if writeReadGateError(c, err) {
			return
		}
		if err.Error() == "COMMENT_NOT_FOUND" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Comment not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	if comments == nil {
		comments = []postgres.Comment{}
	}
	h.svc.HydrateCommentAuthors(c.Request.Context(), viewerID, comments)

	api.JSON(c.Writer, http.StatusOK, comments, nil)
}

func (h *Handler) CreateReply(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	commentIDStr := c.Param("commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid comment ID", nil)
		return
	}

	var req CommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "reply text cannot be blank", nil)
		return
	}

	// Replies are open to every viewer who may comment on the post, so the
	// refusals are AddComment's (2026-09-27). REPLY_OWNER_ONLY, REPLY_EXISTS
	// and CANNOT_REPLY_TO_REPLY no longer occur.
	reply, err := h.svc.CreateReply(c.Request.Context(), commentID, userID, req.Text)
	if err != nil {
		switch {
		case writeMembersOnlyError(c, err):
		case err.Error() == "RATE_LIMITED":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", "Too many replies, please slow down", nil)
		case err.Error() == "COMMENT_NOT_FOUND":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Comment not found", nil)
		case errors.Is(err, service.ErrCommentsDisabled):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "COMMENTS_DISABLED", "Comments are disabled on this post", nil)
		case errors.Is(err, service.ErrCommentsRestricted):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "COMMENTS_RESTRICTED", "Only friends can comment on this post", nil)
		case errors.Is(err, service.ErrCommentsFollowersOnly):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "COMMENTS_FOLLOWERS_ONLY", "Only followers can comment on this post", nil)
		case errors.Is(err, service.ErrPostNotFound), errors.Is(err, service.ErrPostNotVisible):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Comment not found", nil)
		case strings.HasPrefix(err.Error(), "INVALID_REQUEST"):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusCreated, reply, nil)
}

func (h *Handler) ToggleCommentLike(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	commentIDStr := c.Param("commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid comment ID", nil)
		return
	}

	result, err := h.svc.ToggleCommentLike(c.Request.Context(), commentID, userID)
	if err != nil {
		if writeMembersOnlyError(c, err) {
			return
		}
		switch err.Error() {
		case "RATE_LIMITED":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", "Too many comment like toggles, please slow down", nil)
		case "COMMENT_NOT_FOUND":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Comment not found", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

func (h *Handler) ToggleCommentDislike(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	commentIDStr := c.Param("commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid comment ID", nil)
		return
	}

	result, err := h.svc.ToggleCommentDislike(c.Request.Context(), commentID, userID)
	if err != nil {
		// The post's read gate runs first in the service (2026-09-29): a
		// comment under a post the caller may not open is the like toggle's
		// 404; an 18+ post answers the detail's age codes.
		if writeReadGateError(c, err) {
			return
		}
		switch err.Error() {
		case "RATE_LIMITED":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", "Too many comment dislike toggles, please slow down", nil)
		case "COMMENT_NOT_FOUND":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Comment not found", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

func (h *Handler) DeleteComment(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	commentIDStr := c.Param("commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid comment ID", nil)
		return
	}

	err = h.svc.SoftDeleteComment(c.Request.Context(), commentID, userID)
	if err != nil {
		switch err.Error() {
		case "COMMENT_NOT_FOUND":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Comment not found", nil)
		case "NOT_COMMENT_AUTHOR":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "You can only delete your own comments", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

type EditCommentRequest struct {
	Body string `json:"body" binding:"required"`
}

func (h *Handler) EditComment(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	commentIDStr := c.Param("commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid comment ID", nil)
		return
	}

	var req EditCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	err = h.svc.EditComment(c.Request.Context(), commentID, userID, req.Body)
	if err != nil {
		switch err.Error() {
		case "COMMENT_NOT_FOUND":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Comment not found", nil)
		case "NOT_COMMENT_AUTHOR":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "You can only edit your own comments", nil)
		case "EDIT_WINDOW_EXPIRED":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "EDIT_WINDOW_EXPIRED", "Comments can only be edited within 15 minutes of creation", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}
