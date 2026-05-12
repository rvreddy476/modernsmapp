package http

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/notification-service/internal/store/scylla"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/gocql/gocql"
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	limit := 20
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 100 {
		limit = l
	}

	cursor := c.Query("cursor")
	category := c.Query("category")

	page, err := h.svc.GetNotificationsPage(c.Request.Context(), userID, limit, cursor, category)
	if err != nil {
		log.Printf("Failed to get notifications: %v", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch notifications", nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req MarkReadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}

	if err := h.svc.MarkRead(c.Request.Context(), userID, req.Bucket, req.TS); err != nil {
		log.Printf("Failed to mark read: %v", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to mark as read", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"}, nil)
}

func (h *Handler) GetUnreadCount(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	count, err := h.svc.GetUnreadCount(c.Request.Context(), userID)
	if err != nil {
		log.Printf("Failed to get unread count: %v", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get unread count", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]int64{"count": count}, nil)
}

func (h *Handler) MarkAllRead(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	if err := h.svc.MarkAllRead(c.Request.Context(), userID); err != nil {
		log.Printf("Failed to mark all read: %v", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to mark all as read", nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	bucketStr := c.Param("bucket")
	bucket, err := strconv.Atoi(bucketStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid bucket", nil)
		return
	}

	ts := c.Param("ts")
	if ts == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Missing ts", nil)
		return
	}

	if err := h.svc.DeleteNotification(c.Request.Context(), userID, bucket, ts); err != nil {
		log.Printf("Failed to delete notification: %v", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete notification", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

func (h *Handler) GetPreferences(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	prefs, err := h.svc.GetPreferences(c.Request.Context(), userID)
	if err != nil {
		log.Printf("Failed to get preferences: %v", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get preferences", nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req UpdatePreferencesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}

	// Fetch current, merge updates
	current, err := h.svc.GetPreferences(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update preferences", nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req RegisterDeviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}

	device, err := h.svc.RegisterDevice(c.Request.Context(), userID, req.Platform, req.PushToken)
	if err != nil {
		log.Printf("Failed to register device: %v", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to register device", nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, device, nil)
}

func (h *Handler) UnregisterDevice(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	deviceIDStr := c.Param("id")
	deviceID, err := uuid.Parse(deviceIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid device ID", nil)
		return
	}

	if err := h.svc.UnregisterDevice(c.Request.Context(), deviceID, userID); err != nil {
		if err.Error() == "DEVICE_NOT_FOUND" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Device not found", nil)
			return
		}
		log.Printf("Failed to unregister device: %v", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to unregister device", nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	digests, err := h.svc.GetDigests(c.Request.Context(), userID)
	if err != nil {
		log.Printf("GetDigests error: %v", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get digests", nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}

	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid user_id", nil)
		return
	}
	actorID, err := uuid.Parse(req.ActorID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid actor_id", nil)
		return
	}

	var refID *uuid.UUID
	if req.RefID != nil && *req.RefID != "" {
		parsed, parseErr := uuid.Parse(*req.RefID)
		if parseErr != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid ref_id", nil)
			return
		}
		refID = &parsed
	}

	if err := h.svc.BundleNotification(c.Request.Context(), userID, actorID, req.BundleType, refID); err != nil {
		log.Printf("BundleNotification error: %v", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to bundle notification", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"}, nil)
}

// GetNotifPreferences handles GET /v1/notifications/preferences (granular)
func (h *Handler) GetNotifPreferences(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user ID", nil)
		return
	}

	prefs, err := h.svc.GetNotifPreferences(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get preferences", nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user ID", nil)
		return
	}

	var req UpdateNotifPreferencesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}

	// Fetch current, merge updates
	current, err := h.svc.GetNotifPreferences(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update preferences", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, current, nil)
}

// streamHeartbeat is how often the SSE handler emits a `:` comment
// line to keep Cloudflare-proxied connections alive (the proxy's
// idle timeout is ~100 s; this stays well below).
const streamHeartbeat = 25 * time.Second

// StreamNotifications hosts GET /v1/notifications/stream as an SSE
// channel. Two phases per connection:
//
//  1. Replay (if Last-Event-ID header is present): fetch notifications
//     newer than the cursor from Scylla, emit them as
//     `event: notification` frames in chronological order — capped at
//     500 per README §13 — and set an `id:` field on each. The client
//     persists the latest id locally.
//  2. Live: subscribe to Redis pub/sub `notify:<userID>` and forward
//     each push as the same `event: notification` shape, with `id:`
//     reconstructed from the payload's (bucket, ts) so a future
//     reconnect can pick up exactly where this one left off.
//
// The replay query uses the existing Scylla composite cursor
// (bucket, ts) so we don't need a separate event-id table. The
// payload structure produced by service/processor.go carries both
// values; payloads predating that change just won't get an `id:` and
// will fall back to client-side timestamp ordering.
func (h *Handler) StreamNotifications(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeaderNow()

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	channel := fmt.Sprintf("notify:%s", userIDStr)
	sub := h.rdb.Subscribe(ctx, channel)
	defer sub.Close()
	ch := sub.Channel()

	fmt.Fprintf(c.Writer, "event: connected\ndata: {\"status\":\"ok\"}\n\n")
	c.Writer.Flush()

	// Phase 1: Last-Event-ID replay. SSE clients (browser EventSource
	// + our mobile client) automatically send this header on
	// reconnect; the spec also allows reading it from a query param
	// for clients that can't set headers.
	lastID := c.GetHeader("Last-Event-ID")
	if lastID == "" {
		lastID = c.Query("last_event_id")
	}
	if lastID != "" {
		bucket, ts, ok := parseEventID(lastID)
		if ok {
			missed, err := h.svc.GetNotificationsAfter(ctx, userID, bucket, ts, 500)
			if err != nil {
				log.Printf("[notify-stream] replay query failed user=%s err=%v", userIDStr, err)
			} else {
				for _, n := range missed {
					body, eerr := encodeReplayPayload(n)
					if eerr != nil {
						continue
					}
					fmt.Fprintf(
						c.Writer,
						"id: %d:%s\nevent: notification\ndata: %s\n\n",
						n.Bucket, n.TS.String(), body,
					)
				}
				c.Writer.Flush()
			}
		}
	}

	// Phase 2: live pub/sub.
	heartbeat := time.NewTicker(streamHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(c.Writer, ": keepalive\n\n")
			c.Writer.Flush()
		case msg, ok := <-ch:
			if !ok {
				return
			}
			id := extractEventID(msg.Payload)
			if id != "" {
				fmt.Fprintf(c.Writer, "id: %s\n", id)
			}
			fmt.Fprintf(c.Writer, "event: notification\ndata: %s\n\n", msg.Payload)
			c.Writer.Flush()
		}
	}
}

// parseEventID decodes "<bucket>:<ts-uuid>" into its parts. Returns
// ok=false for any malformed input — caller skips replay in that
// case and goes straight to live.
func parseEventID(raw string) (int, gocql.UUID, bool) {
	sep := strings.IndexByte(raw, ':')
	if sep <= 0 || sep == len(raw)-1 {
		return 0, gocql.UUID{}, false
	}
	bucket, err := strconv.Atoi(raw[:sep])
	if err != nil {
		return 0, gocql.UUID{}, false
	}
	ts, err := gocql.ParseUUID(raw[sep+1:])
	if err != nil {
		return 0, gocql.UUID{}, false
	}
	return bucket, ts, true
}

// extractEventID pulls "<bucket>:<ts>" out of the Redis envelope.
// payload shape is `{"type":"notification","payload":{"bucket":X,
// "ts":"<uuid>",...}}` (see service/processor.go). Returns empty when
// either field is missing — caller omits the `id:` SSE line, which is
// spec-legal.
func extractEventID(raw string) string {
	var env struct {
		Payload struct {
			Bucket int    `json:"bucket"`
			TS     string `json:"ts"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return ""
	}
	if env.Payload.Bucket == 0 || env.Payload.TS == "" {
		return ""
	}
	return fmt.Sprintf("%d:%s", env.Payload.Bucket, env.Payload.TS)
}

// encodeReplayPayload renders a stored Notification into the same
// JSON envelope shape that live pub/sub messages use, so clients can
// reuse one parser for replay + live.
func encodeReplayPayload(n scylla.Notification) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"type": "notification",
		"payload": map[string]interface{}{
			"notification_id": n.NotificationID.String(),
			"event_type":      n.Type,
			"actor_id":        n.ActorUserID.String(),
			"target_id":       n.EntityID.String(),
			"target_type":     n.EntityType,
			"deep_link":       n.DeepLink,
			"created_at":      n.CreatedAt,
			"bucket":          n.Bucket,
			"ts":              n.TS.String(),
			// title/body are NOT in the Scylla row — the client should
			// re-render from the notification center API if needed.
			// Most clients only use replay to update unread state +
			// keep the badge in sync, not to re-show toasts.
			"replay": true,
		},
	})
}
