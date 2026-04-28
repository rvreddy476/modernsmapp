package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/atpost/user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type SubscribeRequest struct {
	NotifyOn string `json:"notify_on"`
}

// SubscribeToChannel handles POST /v1/channels/:channelId/subscribe
func (h *Handler) SubscribeToChannel(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	channelID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil)
		return
	}

	var req SubscribeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	if req.NotifyOn == "" {
		req.NotifyOn = "all"
	}

	if err := h.svc.SubscribeToChannel(c.Request.Context(), channelID, userID, req.NotifyOn); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "subscribed"}, nil)
}

// UnsubscribeFromChannel handles DELETE /v1/channels/:channelId/subscribe
func (h *Handler) UnsubscribeFromChannel(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	channelID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil)
		return
	}

	if err := h.svc.UnsubscribeFromChannel(c.Request.Context(), channelID, userID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unsubscribed"}, nil)
}

// GetChannelSubscriptionStatus handles GET /v1/channels/:channelId/subscription
func (h *Handler) GetChannelSubscriptionStatus(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	channelID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil)
		return
	}

	sub, err := h.svc.GetChannelSubscriptionStatus(c.Request.Context(), channelID, userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if sub == nil {
		api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"subscribed": false}, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"subscribed": true, "subscription": sub}, nil)
}

// ListChannelSubscribers handles GET /v1/channels/:channelId/subscribers
func (h *Handler) ListChannelSubscribers(c *gin.Context) {
	channelID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil)
		return
	}

	limit := 20
	if s := c.Query("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			limit = n
		}
	}
	offset := 0
	if s := c.Query("offset"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			offset = n
		}
	}

	subs, err := h.svc.ListChannelSubscribers(c.Request.Context(), channelID, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if subs == nil {
		subs = []store.ChannelSubscription{}
	}
	api.JSON(c.Writer, http.StatusOK, subs, nil)
}

// ListUserChannelSubscriptions handles GET /v1/users/:userId/subscriptions
func (h *Handler) ListUserChannelSubscriptions(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	limit := 20
	if s := c.Query("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			limit = n
		}
	}
	offset := 0
	if s := c.Query("offset"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			offset = n
		}
	}

	subs, err := h.svc.ListUserChannelSubscriptions(c.Request.Context(), userID, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if subs == nil {
		subs = []store.ChannelSubscription{}
	}
	api.JSON(c.Writer, http.StatusOK, subs, nil)
}
