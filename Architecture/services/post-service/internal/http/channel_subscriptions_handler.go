package http

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Tube channel subscriptions (2026-09-12). Founder decisions: Subscribe is
// follow + notify behind one button; unsubscribing removes both; every
// subscriber is notified by default and the per-channel bell opts out.
//
// Wire codes the Tube client keys on:
//
//	400 INVALID_NOTIFY_ON       notify_on is not 'all' / 'none'
//	400 CANNOT_SUBSCRIBE_SELF   the caller owns the channel
//	404 NOT_FOUND               no channel for the ref
//	404 NOT_SUBSCRIBED          bell change on a channel the caller does not subscribe to
//	502 GRAPH_UNAVAILABLE       graph-service refused the follow / unfollow; nothing was written

type subscribeRequest struct {
	NotifyOn string `json:"notify_on"`
}

// bindOptionalJSON decodes a JSON body that may be entirely absent: the
// subscribe button sends nothing, the bell sends {notify_on}.
func bindOptionalJSON(c *gin.Context, dst any) bool {
	if c.Request.Body == nil || c.Request.ContentLength == 0 {
		return true
	}
	if err := c.ShouldBindJSON(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return true
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return false
	}
	return true
}

// writeSubscriptionError maps the subscription flows' typed errors. Returns
// false when the error is not one of them.
func writeSubscriptionError(c *gin.Context, err error) bool {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrInvalidNotifyOn):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_NOTIFY_ON", err.Error(), nil)
	case errors.Is(err, service.ErrCannotSubscribeSelf):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "CANNOT_SUBSCRIBE_SELF", err.Error(), nil)
	case errors.Is(err, service.ErrChannelNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "Channel not found", nil)
	case errors.Is(err, service.ErrNotSubscribed):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_SUBSCRIBED", err.Error(), nil)
	case errors.Is(err, service.ErrGraphUnavailable):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadGateway, "GRAPH_UNAVAILABLE", service.ErrGraphUnavailable.Error(), nil)
	case errors.Is(err, service.ErrInvalidSubscriptionCursor):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_CURSOR", err.Error(), nil)
	default:
		return false
	}
	return true
}

func writeSubscriptionFailure(c *gin.Context, err error) {
	if writeSubscriptionError(c, err) {
		return
	}
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
}

// SubscribeToChannel handles POST /v1/channels/:ref/subscribe.
func (h *Handler) SubscribeToChannel(c *gin.Context) {
	userID, ok := channelCaller(c)
	if !ok {
		return
	}
	var req subscribeRequest
	if !bindOptionalJSON(c, &req) {
		return
	}
	// Validate before touching the service so a bad bell value is a 400
	// even when no store is wired (and never reaches graph-service).
	notifyOn, err := service.ValidateNotifyOn(req.NotifyOn)
	if err != nil {
		writeSubscriptionFailure(c, err)
		return
	}
	result, err := h.svc.Subscribe(c.Request.Context(), userID, c.Param("ref"), notifyOn)
	if err != nil {
		writeSubscriptionFailure(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, result, nil)
}

// UnsubscribeFromChannel handles DELETE /v1/channels/:ref/subscribe.
func (h *Handler) UnsubscribeFromChannel(c *gin.Context) {
	userID, ok := channelCaller(c)
	if !ok {
		return
	}
	result, err := h.svc.Unsubscribe(c.Request.Context(), userID, c.Param("ref"))
	if err != nil {
		writeSubscriptionFailure(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, result, nil)
}

// GetChannelSubscription handles GET /v1/channels/:ref/subscription.
func (h *Handler) GetChannelSubscription(c *gin.Context) {
	userID, ok := channelCaller(c)
	if !ok {
		return
	}
	view, err := h.svc.GetSubscription(c.Request.Context(), userID, c.Param("ref"))
	if err != nil {
		writeSubscriptionFailure(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, view, nil)
}

// UpdateChannelSubscription handles PATCH /v1/channels/:ref/subscription
// {notify_on}: the bell.
func (h *Handler) UpdateChannelSubscription(c *gin.Context) {
	userID, ok := channelCaller(c)
	if !ok {
		return
	}
	var req subscribeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	if strings.TrimSpace(req.NotifyOn) == "" {
		// The bell PATCH must say which way it is going; defaulting to
		// 'all' here would turn a broken client into a silent opt-in.
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_NOTIFY_ON", service.ErrInvalidNotifyOn.Error(), nil)
		return
	}
	notifyOn, err := service.ValidateNotifyOn(req.NotifyOn)
	if err != nil {
		writeSubscriptionFailure(c, err)
		return
	}
	view, err := h.svc.SetNotifyOn(c.Request.Context(), userID, c.Param("ref"), notifyOn)
	if err != nil {
		writeSubscriptionFailure(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, view, nil)
}

// ListMySubscriptions handles GET /v1/channels/subscriptions?limit&cursor.
func (h *Handler) ListMySubscriptions(c *gin.Context) {
	userID, ok := channelCaller(c)
	if !ok {
		return
	}
	limit := 0
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "limit must be a non-negative integer", nil)
			return
		}
		limit = n
	}
	items, next, err := h.svc.ListMySubscriptions(c.Request.Context(), userID, c.Query("cursor"), limit)
	if err != nil {
		writeSubscriptionFailure(c, err)
		return
	}
	if items == nil {
		items = []service.SubscriptionListItem{}
	}
	api.JSON(c.Writer, http.StatusOK, items, &api.Meta{NextCursor: next})
}

// Internal subscriber fan-out contract. Moved from user-service on
// 2026-09-12 with the JSON kept byte-for-byte, so notification-service and
// feed-service only change a base URL. The internal-key middleware applied
// to the whole engine (handler.go RegisterRoutes) gates these; the gateway
// never forwards /internal.
func (h *Handler) registerInternalSubscriptionRoutes(r *gin.Engine) {
	r.GET("/internal/channels/by-owner/:userId", h.GetChannelByOwnerInternal)
	r.GET("/internal/channels/:channelId/subscriber-ids", h.ListSubscriberIDsInternal)
	r.GET("/internal/users/:userId/subscribed-owner-ids", h.ListSubscribedOwnersInternal)
}

// parseAfterCursor reads ?after (a uuid keyset cursor, uuid.Nil when absent).
func parseAfterCursor(c *gin.Context) (uuid.UUID, bool) {
	raw := c.Query("after")
	if raw == "" {
		return uuid.Nil, true
	}
	after, err := uuid.Parse(raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid after cursor", nil)
		return uuid.Nil, false
	}
	return after, true
}

// GetChannelByOwnerInternal handles GET /internal/channels/by-owner/:userId
// -> {"channel_id": "<uuid>" | ""}.
func (h *Handler) GetChannelByOwnerInternal(c *gin.Context) {
	ownerID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid user ID", nil)
		return
	}
	channelID, err := h.svc.ChannelIDByOwner(c.Request.Context(), ownerID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if channelID == uuid.Nil {
		// No channel: the caller treats this as "no subscriber fan-out".
		api.JSON(c.Writer, http.StatusOK, map[string]string{"channel_id": ""}, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"channel_id": channelID.String()}, nil)
}

// ListSubscriberIDsInternal handles
// GET /internal/channels/:channelId/subscriber-ids?after&limit
// -> {"subscriber_ids": [...], "next_after": "<last id>|\"\"", "has_more": bool}.
// Only bell-on subscribers; callers loop until has_more is false.
func (h *Handler) ListSubscriberIDsInternal(c *gin.Context) {
	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid channel ID", nil)
		return
	}
	after, ok := parseAfterCursor(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "500"))
	ids, err := h.svc.ListSubscriberIDsAfter(c.Request.Context(), channelID, after, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	out, nextAfter := idPage(ids)
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{
		"subscriber_ids": out,
		"next_after":     nextAfter,
		"has_more":       len(ids) == limit,
	}, nil)
}

// ListSubscribedOwnersInternal handles
// GET /internal/users/:userId/subscribed-owner-ids?after&limit
// -> {"owner_ids": [...], "next_after": ..., "has_more": bool}.
func (h *Handler) ListSubscribedOwnersInternal(c *gin.Context) {
	viewerID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid user ID", nil)
		return
	}
	after, ok := parseAfterCursor(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "1000"))
	ids, err := h.svc.ListSubscribedOwnersAfter(c.Request.Context(), viewerID, after, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	out, nextAfter := idPage(ids)
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{
		"owner_ids":  out,
		"next_after": nextAfter,
		"has_more":   len(ids) == limit,
	}, nil)
}

func idPage(ids []uuid.UUID) ([]string, string) {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	nextAfter := ""
	if len(ids) > 0 {
		nextAfter = ids[len(ids)-1].String()
	}
	return out, nextAfter
}
