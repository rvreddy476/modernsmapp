package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RegisterCrosspostRoutes registers cross-post HTTP endpoints.
func (h *Handler) RegisterCrosspostRoutes(r *gin.Engine) {
	crossposts := r.Group("/v1/posts/:postId/crossposts")
	{
		crossposts.POST("", h.CreateCrosspost)
		crossposts.GET("", h.ListCrossposts)
		crossposts.DELETE("/:crosspostId", h.RemoveCrosspost)
	}

	// User-level crosspost listing
	r.GET("/v1/crossposts/mine", h.ListMyCrossposts)
}

func (h *Handler) CreateCrosspost(c *gin.Context) {
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

	var req struct {
		TargetModule string `json:"target_module" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "target_module is required", nil)
		return
	}

	link, err := h.svc.CreateCrosspost(c.Request.Context(), postID, userID, req.TargetModule)
	if err != nil {
		switch err.Error() {
		case "RATE_LIMITED":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", "Too many crossposts. Limit: 5 per hour.", nil)
		case "FORBIDDEN":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Cannot crosspost another user's content", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusCreated, link, nil)
}

func (h *Handler) ListCrossposts(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid post ID", nil)
		return
	}

	links, err := h.svc.ListCrossposts(c.Request.Context(), postID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list crossposts", nil)
		return
	}
	if links == nil {
		api.JSON(c.Writer, http.StatusOK, []interface{}{}, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, links, nil)
}

func (h *Handler) RemoveCrosspost(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	crosspostID, err := uuid.Parse(c.Param("crosspostId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid crosspost ID", nil)
		return
	}

	err = h.svc.RemoveCrosspost(c.Request.Context(), crosspostID, userID)
	if err != nil {
		switch err.Error() {
		case "FORBIDDEN":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Cannot remove another user's crosspost", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		}
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *Handler) ListMyCrossposts(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}

	limit := 20
	offset := 0
	if l := c.Query("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 100 {
			limit = v
		}
	}
	if o := c.Query("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil && v >= 0 {
			offset = v
		}
	}

	links, total, err := h.svc.ListCrosspostsByUser(c.Request.Context(), userID, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list crossposts", nil)
		return
	}
	if links == nil {
		api.JSON(c.Writer, http.StatusOK, gin.H{"items": []interface{}{}, "total": total}, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": links, "total": total}, nil)
}
