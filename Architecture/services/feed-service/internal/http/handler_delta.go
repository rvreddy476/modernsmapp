package http

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

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

// FeedDelta handles GET /v1/feed/delta — lightweight new-content check.
//
// Query params:
//   - feed_type: home|following|group|group_channel|channel|community|community_space|flicks|posttube
//   - anchor: last-seen post/item ID or timestamp (ISO 8601)
//   - group_id, channel_id, community_id, space_id: context IDs depending on feed_type
func (h *Handler) FeedDelta(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	feedType := c.Query("feed_type")
	anchor := c.Query("anchor")

	if feedType == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "feed_type is required", nil, nil)
		return
	}
	if anchor == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "anchor is required", nil, nil)
		return
	}

	validTypes := map[string]bool{
		"home": true, "following": true, "group": true, "group_channel": true,
		"channel": true, "community": true, "community_space": true,
		"flicks": true, "posttube": true,
	}
	if !validTypes[feedType] {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST",
			"feed_type must be one of: home, following, group, group_channel, channel, community, community_space, flicks, posttube", nil, nil)
		return
	}

	// Extract context IDs
	groupID := c.Query("group_id")
	channelID := c.Query("channel_id")
	communityID := c.Query("community_id")
	spaceID := c.Query("space_id")

	// Validate required context IDs per feed type
	switch feedType {
	case "group":
		if groupID == "" {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "group_id is required for group feed", nil, nil)
			return
		}
	case "group_channel":
		if groupID == "" || channelID == "" {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "group_id and channel_id are required for group_channel feed", nil, nil)
			return
		}
	case "channel":
		if channelID == "" {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "channel_id is required for channel feed", nil, nil)
			return
		}
	case "community":
		if communityID == "" {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "community_id is required for community feed", nil, nil)
			return
		}
	case "community_space":
		if spaceID == "" {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "space_id is required for community_space feed", nil, nil)
			return
		}
	}

	// Build a cache key for short-lived Redis caching (10s TTL)
	contextID := buildContextID(feedType, userID.String(), groupID, channelID, communityID, spaceID)
	cacheKey := fmt.Sprintf("feed_delta:%s:%s:%s", feedType, contextID, anchor)

	// Check Redis cache
	cached, err := h.svc.GetCachedDelta(c.Request.Context(), cacheKey)
	if err == nil && cached != nil {
		api.JSON(c.Writer, http.StatusOK, cached, nil)
		return
	}

	// Compute delta
	delta, err := h.svc.ComputeFeedDelta(c.Request.Context(), userID, feedType, anchor, groupID, channelID, communityID, spaceID)
	if err != nil {
		slog.Error("feed delta computation failed", "feed_type", feedType, "error", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to compute feed delta", nil, nil)
		return
	}

	// Cache the result for 10 seconds
	h.svc.CacheDelta(context.Background(), cacheKey, delta, 10*time.Second)

	api.JSON(c.Writer, http.StatusOK, delta, nil)
}

// buildContextID creates a unique context identifier for cache keying.
func buildContextID(feedType, userID, groupID, channelID, communityID, spaceID string) string {
	switch feedType {
	case "home", "following", "flicks", "posttube":
		return userID
	case "group":
		return groupID
	case "group_channel":
		return groupID + ":" + channelID
	case "channel":
		return channelID
	case "community":
		return communityID
	case "community_space":
		return spaceID
	default:
		return userID
	}
}
