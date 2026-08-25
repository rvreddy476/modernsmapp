package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Module 1 P0-3 — internal subscriber fan-out contract.
//
// These endpoints live under /internal so the gateway blocks them for
// non-admin callers and RequireInternalKey gates service-to-service
// access. Subscriber identities are NEVER served through a public route.

// GetChannelByOwner — GET /internal/channels/by-owner/:userId
// Resolves an author to their canonical channel so post-service can
// stamp channel_id onto PostCreated for subscriber fan-out.
func (h *Handler) GetChannelByOwner(c *gin.Context) {
	ownerID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid user ID", nil)
		return
	}
	channelID, err := h.svc.GetChannelByOwner(c.Request.Context(), ownerID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if channelID == nil {
		// No channel: the caller treats this as "no subscriber fan-out".
		api.JSON(c.Writer, http.StatusOK, map[string]string{"channel_id": ""}, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"channel_id": channelID.String()}, nil)
}

// ListSubscribedOwners — GET /internal/users/:userId/subscribed-owner-ids
// Returns the creator user IDs whose channels the viewer subscribes to.
// feed-service uses this to build the PostTube Subscriptions feed from
// channel subscriptions instead of the follow graph.
func (h *Handler) ListSubscribedOwners(c *gin.Context) {
	viewerID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid user ID", nil)
		return
	}
	after := uuid.Nil
	if raw := c.Query("after"); raw != "" {
		after, err = uuid.Parse(raw)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid after cursor", nil)
			return
		}
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "1000"))

	ids, err := h.svc.ListSubscribedChannelOwnersAfter(c.Request.Context(), viewerID, after, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	// P2-2: keyset pagination — callers loop until has_more is false, so
	// a viewer with many subscriptions is never silently truncated.
	nextAfter := ""
	if len(ids) > 0 {
		nextAfter = ids[len(ids)-1].String()
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{
		"owner_ids":  out,
		"next_after": nextAfter,
		"has_more":   len(ids) == limit,
	}, nil)
}

// ListSubscriberIDs — GET /internal/channels/:channelId/subscriber-ids
// Keyset pagination: ?after=<uuid>&limit=<n>. Returns only subscribers
// whose notify_on opts into upload notifications. Response carries
// next_after when another page exists — callers loop until it is empty,
// so a large channel is never silently truncated.
func (h *Handler) ListSubscriberIDs(c *gin.Context) {
	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid channel ID", nil)
		return
	}
	after := uuid.Nil
	if raw := c.Query("after"); raw != "" {
		after, err = uuid.Parse(raw)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid after cursor", nil)
			return
		}
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "500"))

	ids, err := h.svc.ListSubscriberIDsAfter(c.Request.Context(), channelID, after, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	nextAfter := ""
	if len(ids) > 0 {
		nextAfter = ids[len(ids)-1].String()
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{
		"subscriber_ids": out,
		"next_after":     nextAfter,
		"has_more":       len(ids) == limit,
	}, nil)
}
