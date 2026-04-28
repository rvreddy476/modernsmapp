package http

import (
	"net/http"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// BulkUnread handles POST /v1/unread/bulk — returns unread counts for multiple contexts
// using a single Redis pipeline.
func (h *Handler) BulkUnread(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	if _, err := uuid.Parse(userIDStr); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
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
