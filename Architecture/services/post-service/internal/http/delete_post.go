package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// DeletePost removes a post owned by the authenticated user.
func (h *Handler) DeletePost(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}

	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid post ID", nil)
		return
	}

	if err := h.svc.DeletePost(c.Request.Context(), postID, userID); err != nil {
		switch {
		case errors.Is(err, service.ErrPostForbidden):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Cannot delete another user's post", nil)
			return
		case errors.Is(err, service.ErrPostNotFound):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Post not found", nil)
			return
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
			return
		}
	}

	c.Status(http.StatusNoContent)
}

// RestorePost — POST /v1/posts/:postId/restore. Author only; the post must
// be soft-deleted and inside the purge window. Returns the restored post as
// the author sees it (200), 409 NOT_DELETED when the post is live or not
// the caller's, 410 RESTORE_WINDOW_EXPIRED when purge_at has passed.
func (h *Handler) RestorePost(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid post ID", nil)
		return
	}
	if err := h.svc.RestorePost(c.Request.Context(), postID, userID); err != nil {
		switch {
		case errors.Is(err, service.ErrPostNotDeleted):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "NOT_DELETED", "Post is not in Recently deleted", nil)
		case errors.Is(err, service.ErrRestoreWindowExpired):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusGone, "RESTORE_WINDOW_EXPIRED", "The restore window for this post has passed", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}
	p, err := h.svc.GetPost(c.Request.Context(), postID, &userID)
	if err != nil || p == nil {
		// Restored, but the read-back failed: 200 with the id is still the
		// truthful answer (the row is live again).
		api.JSON(c.Writer, http.StatusOK, map[string]string{"id": postID.String(), "status": "restored"}, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, p, nil)
}

// ListMyDeletedPosts — GET /v1/posts/me/deleted?limit=&cursor=. The caller's
// "Recently deleted" page, newest deletion first; every item carries
// deleted_at and purge_at.
func (h *Handler) ListMyDeletedPosts(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}
	posts, next, err := h.svc.ListMyDeletedPosts(c.Request.Context(), userID, limit, c.DefaultQuery("cursor", ""))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if posts == nil {
		posts = []service.PostDetail{}
	}
	var meta *api.Meta
	if next != "" {
		meta = &api.Meta{NextCursor: next}
	}
	api.JSON(c.Writer, http.StatusOK, posts, meta)
}
