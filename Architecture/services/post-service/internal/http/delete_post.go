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
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil, nil)
		return
	}

	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid post ID", nil, nil)
		return
	}

	if err := h.svc.DeletePost(c.Request.Context(), postID, userID); err != nil {
		switch {
		case errors.Is(err, service.ErrPostForbidden):
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "Cannot delete another user's post", nil, nil)
			return
		case errors.Is(err, service.ErrPostNotFound):
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Post not found", nil, nil)
			return
		default:
			api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
			return
		}
	}

	c.Status(http.StatusNoContent)
}
