package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RegisterMyUploadsRoutes registers My Uploads HTTP endpoints.
func (h *Handler) RegisterMyUploadsRoutes(r *gin.Engine) {
	uploads := r.Group("/v1/uploads")
	{
		uploads.GET("/videos", h.GetMyVideos)
		uploads.GET("/flicks", h.GetMyFlicks)
		uploads.GET("/posts", h.GetMyTextPosts)
		uploads.GET("/counts", h.GetUploadCounts)
		// Creator Hub bulk edit and bulk delete (2026-09-27 / 09-28,
		// post_edit.go). Static "bulk" / "bulk-delete" are POSTs; the only
		// :postId route here is a DELETE, so neither is ever read as an id.
		uploads.POST("/bulk", h.BulkUpdateUploads)
		uploads.POST("/bulk-delete", h.BulkDeleteUploads)
		uploads.DELETE("/:postId", h.DeleteUpload)
	}
}

// includeScheduled is the Creator Hub's ?include_scheduled=true switch on
// the videos / flicks lists: scheduled posts appear with scheduled_at set
// and the "scheduled" flag. Off by default, which is what every existing
// client sees.
func includeScheduled(c *gin.Context) bool {
	v := strings.ToLower(strings.TrimSpace(c.Query("include_scheduled")))
	return v == "true" || v == "1"
}

func (h *Handler) GetMyVideos(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}

	limit, cursor := parseUploadPagination(c)

	uploads, nextCursor, err := h.svc.GetMyVideosOpts(c.Request.Context(), userID, limit, cursor, includeScheduled(c))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch videos", nil)
		return
	}
	if uploads == nil {
		api.JSON(c.Writer, http.StatusOK, []interface{}{}, cursorMeta(nextCursor))
		return
	}
	api.JSON(c.Writer, http.StatusOK, uploads, cursorMeta(nextCursor))
}

func (h *Handler) GetMyFlicks(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}

	limit, cursor := parseUploadPagination(c)

	uploads, nextCursor, err := h.svc.GetMyFlicksOpts(c.Request.Context(), userID, limit, cursor, includeScheduled(c))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch flicks", nil)
		return
	}
	if uploads == nil {
		api.JSON(c.Writer, http.StatusOK, []interface{}{}, cursorMeta(nextCursor))
		return
	}
	api.JSON(c.Writer, http.StatusOK, uploads, cursorMeta(nextCursor))
}

func (h *Handler) GetMyTextPosts(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}

	limit, cursor := parseUploadPagination(c)

	posts, nextCursor, err := h.svc.GetMyPosts(c.Request.Context(), userID, limit, cursor)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch posts", nil)
		return
	}
	if posts == nil {
		api.JSON(c.Writer, http.StatusOK, []interface{}{}, cursorMeta(nextCursor))
		return
	}
	api.JSON(c.Writer, http.StatusOK, posts, cursorMeta(nextCursor))
}

func (h *Handler) GetUploadCounts(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}

	videos, flicks, posts, err := h.svc.GetUploadCounts(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get counts", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{
		"videos": videos,
		"flicks": flicks,
		"posts":  posts,
	}, nil)
}

func (h *Handler) DeleteUpload(c *gin.Context) {
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

	err = h.svc.DeleteUploadCascade(c.Request.Context(), postID, userID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrPostForbidden):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Cannot delete another user's upload", nil)
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

func parseUploadPagination(c *gin.Context) (int, string) {
	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 && l <= 50 {
		limit = l
	}
	cursor := c.DefaultQuery("cursor", "")
	return limit, cursor
}

func cursorMeta(nextCursor string) *api.Meta {
	if nextCursor == "" {
		return nil
	}
	return &api.Meta{NextCursor: nextCursor}
}
