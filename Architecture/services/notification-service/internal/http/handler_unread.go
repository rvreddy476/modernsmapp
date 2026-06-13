package http

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// BulkUnread handles POST /v1/unread/bulk — returns unread counts for multiple contexts
// using a single Redis pipeline.
//
// MS1: per-user rate limit (60 req/min). The 200-ID cap is per-request,
// but without a per-user gate a single client can hammer the endpoint
// (60 req/sec × 200 IDs = 12k Redis pipeline ops/sec from one user).
func (h *Handler) BulkUnread(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	if _, err := uuid.Parse(userIDStr); err != nil {
		// MS8: log parse failures (without echoing the bad value to
		// avoid log poisoning) so forensics can spot scanners spraying
		// the endpoint with junk IDs.
		slog.Warn("bulk-unread: invalid X-User-Id", "ip", c.ClientIP())
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	if !h.allowBulkUnread(c.Request.Context(), userIDStr) {
		c.Header("Retry-After", "60")
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED",
			"bulk-unread request rate exceeded", nil)
		return
	}

	var req service.BulkUnreadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}

	// Validate total requested IDs to prevent abuse
	total := len(req.GroupIDs) + len(req.ChannelIDs) + len(req.CommunityIDs) + len(req.ConversationIDs)
	if total == 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "At least one ID list must be provided", nil)
		return
	}
	if total > 200 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Maximum 200 total IDs allowed", nil)
		return
	}

	resp, err := h.svc.GetBulkUnread(c.Request.Context(), userIDStr, &req)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch unread counts", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

// ReadMarker handles POST /v1/read-marker — sets a read marker and resets unread counters.
func (h *Handler) ReadMarker(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	if _, err := uuid.Parse(userIDStr); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req service.ReadMarkerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}

	if err := h.svc.SetReadMarker(c.Request.Context(), userIDStr, &req); err != nil {
		if err.Error() == "invalid context_type: must be group, channel, community_space, or chat" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to set read marker", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"}, nil)
}

// allowBulkUnread enforces 60 req/min/user on the bulk-unread surface.
// Redis sliding window (same pattern as feed MF5 + autocomplete HS3).
// Fail-CLOSED on Redis error because this endpoint isn't security-
// critical but does drive significant Redis pipeline load on every
// call; dropping a few requests during a Redis outage is correct
// behavior here.
func (h *Handler) allowBulkUnread(ctx context.Context, userID string) bool {
	if h.rdb == nil {
		return true
	}
	tctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	key := fmt.Sprintf("notif_bulk_unread_rl:%s", userID)
	pipe := h.rdb.Pipeline()
	incr := pipe.Incr(tctx, key)
	pipe.Expire(tctx, key, time.Minute)
	if _, err := pipe.Exec(tctx); err != nil {
		slog.Warn("bulk-unread rate limit: redis error", "err", err)
		return false
	}
	return incr.Val() <= 60
}
