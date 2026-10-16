package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/atpost/feed-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// DeltaResponse represents the feed delta check result.
type DeltaResponse struct {
	NewCount     int    `json:"new_count"`
	NewestAnchor string `json:"newest_anchor,omitempty"`
	HasMore      bool   `json:"has_more"`
}

// deltaFeedTypes are the feeds the delta answers for: the viewer's own.
// group / group_channel / channel / community / community_space were
// dropped — see service.ComputeFeedDelta.
var deltaFeedTypes = map[string]bool{"home": true, "following": true, "flicks": true, "posttube": true}

// FeedDelta handles GET /v1/feed/delta — lightweight new-content check.
//
// Query params:
//   - feed_type: home|following|flicks|posttube
//   - anchor: RFC3339 timestamp of the newest item the client has shown
//     (newest_anchor from a previous response is also a valid anchor)
func (h *Handler) FeedDelta(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	feedType := c.Query("feed_type")
	anchor := c.Query("anchor")

	if feedType == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "feed_type is required", nil)
		return
	}
	if anchor == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "anchor is required", nil)
		return
	}
	if !deltaFeedTypes[feedType] {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST",
			"feed_type must be one of: home, following, flicks, posttube", nil)
		return
	}
	if _, err := service.ParseDeltaAnchor(anchor); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST",
			"anchor must be an RFC3339 timestamp", nil)
		return
	}

	// Short-lived Redis cache (10s TTL), per viewer, feed and anchor.
	cacheKey := fmt.Sprintf("feed_delta:%s:%s:%s", feedType, userID.String(), anchor)

	cached, err := h.svc.GetCachedDelta(c.Request.Context(), cacheKey)
	if err == nil && cached != nil {
		api.JSON(c.Writer, http.StatusOK, cached, nil)
		return
	}

	delta, err := h.svc.ComputeFeedDelta(c.Request.Context(), userID, feedType, anchor)
	if err != nil {
		if errors.Is(err, service.ErrInvalidDeltaAnchor) || errors.Is(err, service.ErrUnsupportedDeltaFeed) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
			return
		}
		slog.Error("feed delta computation failed", "feed_type", feedType, "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to compute feed delta", nil)
		return
	}

	h.svc.CacheDelta(context.Background(), cacheKey, delta, 10*time.Second)
	api.JSON(c.Writer, http.StatusOK, delta, nil)
}
