package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RegisterReelFeedRoutes adds reel feed endpoints.
func (h *Handler) RegisterReelFeedRoutes(r *gin.Engine) {
	r.GET("/v1/reels/feed", h.GetReelFeed)
	r.POST("/v1/reels/feed/refresh", h.RefreshReelFeed)
}

func (h *Handler) GetReelFeed(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}
	cursor := c.DefaultQuery("cursor", "")

	items, nextCursor, err := h.svc.GetReelFeed(c.Request.Context(), userID, limit, cursor)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	var meta *api.Meta
	if nextCursor != "" {
		meta = &api.Meta{NextCursor: nextCursor}
	}

	api.JSON(c.Writer, http.StatusOK, items, meta)
}

func (h *Handler) RefreshReelFeed(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	if err := h.svc.ResetReelSeenState(c.Request.Context(), userID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "refreshed"}, nil)
}
