package http

import (
	"errors"
	"net/http"

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
