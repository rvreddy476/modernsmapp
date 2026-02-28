package http

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/facebook-like/notification-service/internal/service"
	"github.com/facebook-like/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Handler struct {
	svc *service.Service
	rdb *redis.Client
}

func New(svc *service.Service, rdb *redis.Client) *Handler {
	return &Handler{svc: svc, rdb: rdb}
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	v1 := r.Group("/v1/notifications")
	{
		v1.GET("", h.GetNotifications)
		v1.GET("/stream", h.StreamNotifications)
		v1.POST("/read", h.MarkRead)
	}
}

func (h *Handler) GetNotifications(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	limitStr := c.Query("limit")
	limit := 20
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	// Cursor logic would go here (parsing bucket/ts from query param)
	// For MVP v1, we just return the latest from the hardcoded/current bucket

	notifs, err := h.svc.GetNotifications(c.Request.Context(), userID, limit)
	if err != nil {
		log.Printf("Failed to get notifications: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch notifications", nil, nil)
		return
	}

	// Transform to response if needed, or return directly
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{
		"items":       notifs,
		"next_cursor": nil, // TODO: Implement cursor pagination
	}, nil)
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
