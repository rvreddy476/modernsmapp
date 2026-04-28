package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RegisterReelDiscoveryRoutes adds discovery, slug, and moderation endpoints.
func (h *Handler) RegisterReelDiscoveryRoutes(r *gin.Engine) {
	// Trending hashtags
	r.GET("/v1/reels/hashtags/trending", h.GetTrendingHashtags)

	// Slug redirect
	r.GET("/v1/reels/slug/:slug", h.LookupSlugRedirect)

	// Moderation (admin)
	r.GET("/v1/reels/moderation/flagged", h.GetFlaggedReels)
	r.GET("/v1/reels/:reelId/moderation", h.GetReelModerationReviews)
}

func (h *Handler) GetTrendingHashtags(c *gin.Context) {
	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	sinceDays := 7
	if d, err := strconv.Atoi(c.DefaultQuery("days", "7")); err == nil && d > 0 && d <= 90 {
		sinceDays = d
	}

	tags, err := h.svc.GetTrendingHashtags(c.Request.Context(), limit, sinceDays)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, tags, nil)
}

func (h *Handler) LookupSlugRedirect(c *gin.Context) {
	slug := c.Param("slug")
	if slug == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_SLUG", "Slug is required", nil)
		return
	}

	reelID, newSlug, err := h.svc.LookupSlugRedirect(c.Request.Context(), slug)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Slug not found", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{
		"reel_id":  reelID,
		"new_slug": newSlug,
	}, nil)
}

func (h *Handler) GetFlaggedReels(c *gin.Context) {
	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	offset := 0
	if o, err := strconv.Atoi(c.DefaultQuery("offset", "0")); err == nil && o >= 0 {
		offset = o
	}

	reviews, err := h.svc.GetFlaggedReels(c.Request.Context(), limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, reviews, nil)
}

func (h *Handler) GetReelModerationReviews(c *gin.Context) {
	reelID, err := uuid.Parse(c.Param("reelId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid reel ID", nil)
		return
	}

	reviews, err := h.svc.GetReelModerationReviews(c.Request.Context(), reelID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, reviews, nil)
}
