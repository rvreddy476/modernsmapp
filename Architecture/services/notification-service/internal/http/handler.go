package http

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Handler struct {
	svc         *service.Service
	rdb         *redis.Client
	internalKey string
}

func New(svc *service.Service, rdb *redis.Client) *Handler {
	return &Handler{svc: svc, rdb: rdb}
}

// WithInternalKey sets the internal service key used to authenticate
// service-to-service requests via the X-Internal-Service-Key header.
func (h *Handler) WithInternalKey(key string) *Handler {
	h.internalKey = key
	return h
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	// Apply internal service key enforcement to all /v1 routes.
	if h.internalKey != "" {
		r.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}

	v1 := r.Group("/v1/notifications")
	{
		v1.GET("", h.GetNotifications)
		v1.GET("/stream", h.StreamNotifications)
		v1.POST("/read", h.MarkRead)
		v1.GET("/unread-count", h.GetUnreadCount)
		v1.PATCH("/read-all", h.MarkAllRead)
		v1.DELETE("/:bucket/:ts", h.DeleteNotification)
		v1.GET("/preferences", h.GetPreferences)
		v1.PATCH("/preferences", h.UpdatePreferences)
		v1.POST("/devices", h.RegisterDevice)
		v1.DELETE("/devices/:id", h.UnregisterDevice)
		v1.GET("/digests", h.GetDigests)
		v1.POST("/bundle", h.BundleNotification)
		v1.GET("/preferences/detailed", h.GetNotifPreferences)
		v1.PUT("/preferences/detailed", h.UpdateNotifPreferences)
	}

	// Unread and read-marker APIs
	r.POST("/v1/unread/bulk", h.BulkUnread)
	r.POST("/v1/read-marker", h.ReadMarker)
}

func (h *Handler) GetNotifications(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	limit := 20
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 100 {
		limit = l
	}

	cursor := c.Query("cursor")

	page, err := h.svc.GetNotificationsPage(c.Request.Context(), userID, limit, cursor)
	if err != nil {
		log.Printf("Failed to get notifications: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch notifications", nil, nil)
		return
	}

	var meta *api.Meta
	if page.NextCursor != "" {
		meta = &api.Meta{NextCursor: page.NextCursor}
	}

	api.JSON(c.Writer, http.StatusOK, page.Items, meta)
}

type MarkReadRequest struct {
	Bucket int    `json:"bucket" binding:"required"`
	TS     string `json:"ts" binding:"required"`
}

func (h *Handler) MarkRead(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req MarkReadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.MarkRead(c.Request.Context(), userID, req.Bucket, req.TS); err != nil {
		log.Printf("Failed to mark read: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to mark as read", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"}, nil)
}

func (h *Handler) GetUnreadCount(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	count, err := h.svc.GetUnreadCount(c.Request.Context(), userID)
	if err != nil {
		log.Printf("Failed to get unread count: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get unread count", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]int64{"count": count}, nil)
}

func (h *Handler) MarkAllRead(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	if err := h.svc.MarkAllRead(c.Request.Context(), userID); err != nil {
		log.Printf("Failed to mark all read: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to mark all as read", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"}, nil)
}

type DeleteNotificationRequest struct {
	Bucket string `uri:"bucket" binding:"required"`
	TS     string `uri:"ts" binding:"required"`
}

func (h *Handler) DeleteNotification(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	bucketStr := c.Param("bucket")
	bucket, err := strconv.Atoi(bucketStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid bucket", nil, nil)
		return
	}

	ts := c.Param("ts")
	if ts == "" {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Missing ts", nil, nil)
		return
	}

	if err := h.svc.DeleteNotification(c.Request.Context(), userID, bucket, ts); err != nil {
		log.Printf("Failed to delete notification: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete notification", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

func (h *Handler) GetPreferences(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	prefs, err := h.svc.GetPreferences(c.Request.Context(), userID)
	if err != nil {
		log.Printf("Failed to get preferences: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get preferences", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, prefs, nil)
}

type UpdatePreferencesRequest struct {
	EmailEnabled    *bool            `json:"email_enabled"`
	PushEnabled     *bool            `json:"push_enabled"`
	SMSEnabled      *bool            `json:"sms_enabled"`
	QuietHoursStart *string          `json:"quiet_hours_start"`
	QuietHoursEnd   *string          `json:"quiet_hours_end"`
	MutedTypes      *json.RawMessage `json:"muted_types"`
}

func (h *Handler) UpdatePreferences(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req UpdatePreferencesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	// Fetch current, merge updates
	current, err := h.svc.GetPreferences(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	if req.EmailEnabled != nil {
		current.EmailEnabled = *req.EmailEnabled
	}
	if req.PushEnabled != nil {
		current.PushEnabled = *req.PushEnabled
	}
	if req.SMSEnabled != nil {
		current.SMSEnabled = *req.SMSEnabled
	}
	if req.QuietHoursStart != nil {
		current.QuietHoursStart = req.QuietHoursStart
	}
	if req.QuietHoursEnd != nil {
		current.QuietHoursEnd = req.QuietHoursEnd
	}
	if req.MutedTypes != nil {
		current.MutedTypes = *req.MutedTypes
	}
	current.UserID = userID

	if err := h.svc.UpdatePreferences(c.Request.Context(), current); err != nil {
		log.Printf("Failed to update preferences: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update preferences", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, current, nil)
}

type RegisterDeviceRequest struct {
	Platform  string `json:"platform" binding:"required,oneof=ios android web"`
	PushToken string `json:"push_token" binding:"required"`
}

func (h *Handler) RegisterDevice(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req RegisterDeviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	device, err := h.svc.RegisterDevice(c.Request.Context(), userID, req.Platform, req.PushToken)
	if err != nil {
		log.Printf("Failed to register device: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to register device", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, device, nil)
}

func (h *Handler) UnregisterDevice(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	deviceIDStr := c.Param("id")
	deviceID, err := uuid.Parse(deviceIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid device ID", nil, nil)
		return
	}

	if err := h.svc.UnregisterDevice(c.Request.Context(), deviceID, userID); err != nil {
		if err.Error() == "DEVICE_NOT_FOUND" {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Device not found", nil, nil)
			return
		}
		log.Printf("Failed to unregister device: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to unregister device", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "removed"}, nil)
}

// GetDigests handles GET /v1/notifications/digests
// Returns weekly/monthly notification digests for the authenticated user.
func (h *Handler) GetDigests(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	digests, err := h.svc.GetDigests(c.Request.Context(), userID)
	if err != nil {
		log.Printf("GetDigests error: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get digests", nil, nil)
		return
	}
	if digests == nil {
		api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": []any{}}, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": digests}, nil)
}

// BundleNotificationRequest is the request body for POST /v1/notifications/bundle.
type BundleNotificationRequest struct {
	UserID     string  `json:"user_id" binding:"required"`
	ActorID    string  `json:"actor_id" binding:"required"`
	BundleType string  `json:"bundle_type" binding:"required"`
	RefID      *string `json:"ref_id"`
}

// BundleNotification handles POST /v1/notifications/bundle (internal only).
// It groups a notification event into a bundle to reduce noise.
func (h *Handler) BundleNotification(c *gin.Context) {
	var req BundleNotificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid user_id", nil, nil)
		return
	}
	actorID, err := uuid.Parse(req.ActorID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid actor_id", nil, nil)
		return
	}

	var refID *uuid.UUID
	if req.RefID != nil && *req.RefID != "" {
		parsed, parseErr := uuid.Parse(*req.RefID)
		if parseErr != nil {
			api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid ref_id", nil, nil)
			return
		}
		refID = &parsed
	}

	if err := h.svc.BundleNotification(c.Request.Context(), userID, actorID, req.BundleType, refID); err != nil {
		log.Printf("BundleNotification error: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to bundle notification", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"}, nil)
}

// GetNotifPreferences handles GET /v1/notifications/preferences (granular)
func (h *Handler) GetNotifPreferences(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user ID", nil, nil)
		return
	}

	prefs, err := h.svc.GetNotifPreferences(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get preferences", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, prefs, nil)
}

// UpdateNotifPreferencesRequest is the request body for PUT /v1/notifications/preferences (granular).
type UpdateNotifPreferencesRequest struct {
	PushEnabled         *bool   `json:"push_enabled"`
	EmailEnabled        *bool   `json:"email_enabled"`
	QuietHoursEnabled   *bool   `json:"quiet_hours_enabled"`
	QuietHoursStart     *string `json:"quiet_hours_start"`
	QuietHoursEnd       *string `json:"quiet_hours_end"`
	QuietHoursTZ        *string `json:"quiet_hours_tz"`
	PushLikes           *bool   `json:"push_likes"`
	PushSuperLikes      *bool   `json:"push_super_likes"`
	PushComments        *bool   `json:"push_comments"`
	PushReplies         *bool   `json:"push_replies"`
	PushMentions        *bool   `json:"push_mentions"`
	PushFollows         *bool   `json:"push_follows"`
	PushFriendRequests  *bool   `json:"push_friend_requests"`
	PushGroupPosts      *bool   `json:"push_group_posts"`
	PushGroupMentions   *bool   `json:"push_group_mentions"`
	PushChannelUpdates  *bool   `json:"push_channel_updates"`
	PushChannelUrgent   *bool   `json:"push_channel_urgent"`
	PushCommunityPosts  *bool   `json:"push_community_posts"`
	PushCommunityMentions *bool `json:"push_community_mentions"`
	PushEventReminders  *bool   `json:"push_event_reminders"`
	PushSystem          *bool   `json:"push_system"`
	EmailDigest         *string `json:"email_digest"`
}

// UpdateNotifPreferences handles PUT /v1/notifications/preferences (granular)
func (h *Handler) UpdateNotifPreferences(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user ID", nil, nil)
		return
	}

	var req UpdateNotifPreferencesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	// Fetch current, merge updates
	current, err := h.svc.GetNotifPreferences(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	if req.PushEnabled != nil {
		current.PushEnabled = *req.PushEnabled
	}
	if req.EmailEnabled != nil {
		current.EmailEnabled = *req.EmailEnabled
	}
	if req.QuietHoursEnabled != nil {
		current.QuietHoursEnabled = *req.QuietHoursEnabled
	}
	if req.QuietHoursStart != nil {
		current.QuietHoursStart = req.QuietHoursStart
	}
	if req.QuietHoursEnd != nil {
		current.QuietHoursEnd = req.QuietHoursEnd
	}
	if req.QuietHoursTZ != nil {
		current.QuietHoursTZ = req.QuietHoursTZ
	}
	if req.PushLikes != nil {
		current.PushLikes = *req.PushLikes
	}
	if req.PushSuperLikes != nil {
		current.PushSuperLikes = *req.PushSuperLikes
	}
	if req.PushComments != nil {
		current.PushComments = *req.PushComments
	}
	if req.PushReplies != nil {
		current.PushReplies = *req.PushReplies
	}
	if req.PushMentions != nil {
		current.PushMentions = *req.PushMentions
	}
	if req.PushFollows != nil {
		current.PushFollows = *req.PushFollows
	}
	if req.PushFriendRequests != nil {
		current.PushFriendRequests = *req.PushFriendRequests
	}
	if req.PushGroupPosts != nil {
		current.PushGroupPosts = *req.PushGroupPosts
	}
	if req.PushGroupMentions != nil {
		current.PushGroupMentions = *req.PushGroupMentions
	}
	if req.PushChannelUpdates != nil {
		current.PushChannelUpdates = *req.PushChannelUpdates
	}
	if req.PushChannelUrgent != nil {
		current.PushChannelUrgent = *req.PushChannelUrgent
	}
	if req.PushCommunityPosts != nil {
		current.PushCommunityPosts = *req.PushCommunityPosts
	}
	if req.PushCommunityMentions != nil {
		current.PushCommunityMentions = *req.PushCommunityMentions
	}
	if req.PushEventReminders != nil {
		current.PushEventReminders = *req.PushEventReminders
	}
	if req.PushSystem != nil {
		current.PushSystem = *req.PushSystem
	}
	if req.EmailDigest != nil {
		current.EmailDigest = *req.EmailDigest
	}
	current.UserID = userID

	if err := h.svc.UpdateNotifPreferences(c.Request.Context(), current); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update preferences", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, current, nil)
}

// StreamNotifications uses SSE to push real-time notifications from Redis pub/sub
func (h *Handler) StreamNotifications(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	if _, err := uuid.Parse(userIDStr); err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	// SSE headers
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeaderNow()

	// Subscribe to the user's notification channel
	channel := fmt.Sprintf("notify:%s", userIDStr)
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	sub := h.rdb.Subscribe(ctx, channel)
	defer sub.Close()

	ch := sub.Channel()

	// Send initial heartbeat so client knows connection is alive
	fmt.Fprintf(c.Writer, "event: connected\ndata: {\"status\":\"ok\"}\n\n")
	c.Writer.Flush()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(c.Writer, "event: notification\ndata: %s\n\n", msg.Payload)
			c.Writer.Flush()
		}
	}
}
