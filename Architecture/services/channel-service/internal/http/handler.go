package http

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/channel-service/internal/service"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	svc         *service.Service
	internalKey string
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

// WithInternalKey sets the internal service key used to authenticate
// service-to-service requests via the X-Internal-Service-Key header.
func (h *Handler) WithInternalKey(key string) *Handler {
	h.internalKey = key
	return h
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	// Apply internal service key enforcement to all /v1 routes.
	// Health and metrics endpoints registered outside this group remain public.
	if h.internalKey != "" {
		r.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}

	v1 := r.Group("/v1/broadcast-channels")
	{
		v1.POST("", h.CreateChannel)
		v1.GET("/my", h.GetMyChannels)
		v1.GET("/discover", h.DiscoverChannels)
		v1.GET("/:channelId", h.GetChannel)
		v1.PUT("/:channelId", h.UpdateChannel)
		v1.DELETE("/:channelId", h.DeleteChannel)
		v1.POST("/:channelId/subscribe", h.Subscribe)
		v1.DELETE("/:channelId/subscribe", h.Unsubscribe)
		v1.PUT("/:channelId/subscribe/mute", h.MuteChannel)
		v1.GET("/:channelId/subscribers", h.ListSubscribers)
		v1.POST("/:channelId/updates", h.CreateUpdate)
		v1.GET("/:channelId/updates", h.ListUpdates)
		v1.PUT("/:channelId/updates/:updateId", h.EditUpdate)
		v1.DELETE("/:channelId/updates/:updateId", h.DeleteUpdate)

		// Engagement
		v1.POST("/:channelId/updates/:updateId/spark", h.SparkUpdate)
		v1.DELETE("/:channelId/updates/:updateId/spark", h.UnsparkUpdate)
		v1.POST("/:channelId/updates/:updateId/stash", h.StashUpdate)
		v1.DELETE("/:channelId/updates/:updateId/stash", h.UnstashUpdate)
		v1.POST("/:channelId/updates/:updateId/echo", h.EchoUpdate)
		v1.DELETE("/:channelId/updates/:updateId/echo", h.UnechoUpdate)
		v1.POST("/:channelId/updates/:updateId/view", h.RecordView)
		v1.GET("/:channelId/updates/:updateId/comments", h.ListComments)
		v1.GET("/:channelId/updates/:updateId/comments/delta", h.ListCommentsDelta)
		v1.POST("/:channelId/updates/:updateId/comments", h.AddComment)
		v1.DELETE("/:channelId/updates/:updateId/comments/:commentId", h.DeleteComment)
		v1.POST("/:channelId/updates/:updateId/comments/:commentId/pin", h.PinComment)
		v1.POST("/:channelId/updates/:updateId/vote", h.VoteOnPoll)
		v1.GET("/:channelId/updates/:updateId/results", h.GetPollResults)
		v1.POST("/:channelId/updates/:updateId/rsvp", h.RSVPEvent)
		v1.GET("/:channelId/updates/:updateId/attendees", h.ListAttendees)
	}

}

// --- Request structs ---

type CreateChannelRequest struct {
	Handle                 string     `json:"handle" binding:"required"`
	Name                   string     `json:"name" binding:"required"`
	Description            string     `json:"description"`
	AvatarMediaID          *uuid.UUID `json:"avatar_media_id"`
	BannerMediaID          *uuid.UUID `json:"banner_media_id"`
	ChannelType            string     `json:"channel_type"`
	Category               string     `json:"category"`
	Language               string     `json:"language"`
	CommentMode            string     `json:"comment_mode"`
	ReactionMode           string     `json:"reaction_mode"`
	ForwardAllowed         *bool      `json:"forward_allowed"`
	PaidAccess             bool       `json:"paid_access"`
	SubscriptionPriceCents int        `json:"subscription_price_cents"`
}

type UpdateChannelRequest struct {
	Name                   *string    `json:"name"`
	Description            *string    `json:"description"`
	AvatarMediaID          *uuid.UUID `json:"avatar_media_id"`
	BannerMediaID          *uuid.UUID `json:"banner_media_id"`
	ChannelType            *string    `json:"channel_type"`
	Category               *string    `json:"category"`
	Language               *string    `json:"language"`
	CommentMode            *string    `json:"comment_mode"`
	ReactionMode           *string    `json:"reaction_mode"`
	ForwardAllowed         *bool      `json:"forward_allowed"`
	PaidAccess             *bool      `json:"paid_access"`
	SubscriptionPriceCents *int       `json:"subscription_price_cents"`
	PostScheduleEnabled    *bool      `json:"post_schedule_enabled"`
	SubscriberCountVisible *bool      `json:"subscriber_count_visible"`
	AllowPreviewPosts      *int       `json:"allow_preview_posts"`
}

type CreateUpdateRequest struct {
	UpdateType  string          `json:"update_type"`
	Title       *string         `json:"title"`
	Body        string          `json:"body"`
	MediaIDs    []uuid.UUID     `json:"media_ids"`
	Metadata    json.RawMessage `json:"metadata"`
	ScheduledAt *time.Time      `json:"scheduled_at"`
}

type MuteRequest struct {
	MutedUntil *time.Time `json:"muted_until"`
}

// --- Helpers ---

func getUserID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return uuid.Nil, false
	}
	return id, true
}

func parsePagination(c *gin.Context) (int, int) {
	limit := 20
	offset := 0
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	if o, err := strconv.Atoi(c.DefaultQuery("offset", "0")); err == nil && o >= 0 {
		offset = o
	}
	return limit, offset
}

func handleServiceError(c *gin.Context, err error) {
	msg := err.Error()
	switch {
	case contains(msg, "forbidden"), contains(msg, "only admins"), contains(msg, "only the channel"), contains(msg, "only editors"):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", msg, nil)
	case contains(msg, "not found"):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", msg, nil)
	case contains(msg, "not a member"):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", msg, nil)
	case contains(msg, "rate_limited"):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", msg, nil)
	case contains(msg, "already"):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "CONFLICT", msg, nil)
	case contains(msg, "invalid"), contains(msg, "must be between"), contains(msg, "is required"):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, "VALIDATION_ERROR", msg, nil)
	default:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", msg, nil)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- Handlers ---

func (h *Handler) CreateChannel(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	var req CreateChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	params := service.CreateChannelParams{
		Handle:                 req.Handle,
		Name:                   req.Name,
		Description:            req.Description,
		AvatarMediaID:          req.AvatarMediaID,
		BannerMediaID:          req.BannerMediaID,
		ChannelType:            req.ChannelType,
		Category:               req.Category,
		Language:               req.Language,
		CommentMode:            req.CommentMode,
		ReactionMode:           req.ReactionMode,
		ForwardAllowed:         req.ForwardAllowed,
		PaidAccess:             req.PaidAccess,
		SubscriptionPriceCents: req.SubscriptionPriceCents,
	}

	ch, err := h.svc.CreateChannel(c.Request.Context(), actorID, params)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, ch, nil)
}

func (h *Handler) GetChannel(c *gin.Context) {
	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil)
		return
	}

	var viewerID *uuid.UUID
	if uid, err := uuid.Parse(c.GetHeader("X-User-Id")); err == nil {
		viewerID = &uid
	}

	ch, err := h.svc.GetChannel(c.Request.Context(), channelID, viewerID)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, ch, nil)
}

func (h *Handler) UpdateChannel(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil)
		return
	}

	var req UpdateChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	params := service.UpdateChannelParams{
		Name:                   req.Name,
		Description:            req.Description,
		AvatarMediaID:          req.AvatarMediaID,
		BannerMediaID:          req.BannerMediaID,
		ChannelType:            req.ChannelType,
		Category:               req.Category,
		Language:               req.Language,
		CommentMode:            req.CommentMode,
		ReactionMode:           req.ReactionMode,
		ForwardAllowed:         req.ForwardAllowed,
		PaidAccess:             req.PaidAccess,
		SubscriptionPriceCents: req.SubscriptionPriceCents,
		PostScheduleEnabled:    req.PostScheduleEnabled,
		SubscriberCountVisible: req.SubscriberCountVisible,
		AllowPreviewPosts:      req.AllowPreviewPosts,
	}

	ch, err := h.svc.UpdateChannel(c.Request.Context(), channelID, actorID, params)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, ch, nil)
}

func (h *Handler) DeleteChannel(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil)
		return
	}

	if err := h.svc.DeleteChannel(c.Request.Context(), channelID, actorID); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

func (h *Handler) Subscribe(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil)
		return
	}

	if err := h.svc.Subscribe(c.Request.Context(), channelID, actorID); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "subscribed"}, nil)
}

func (h *Handler) Unsubscribe(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil)
		return
	}

	if err := h.svc.Unsubscribe(c.Request.Context(), channelID, actorID); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unsubscribed"}, nil)
}

func (h *Handler) MuteChannel(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil)
		return
	}

	var req MuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	if err := h.svc.MuteChannel(c.Request.Context(), channelID, actorID, req.MutedUntil); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "muted"}, nil)
}

func (h *Handler) ListSubscribers(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil)
		return
	}

	limit, offset := parsePagination(c)
	members, err := h.svc.ListSubscribers(c.Request.Context(), channelID, actorID, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, members, nil)
}

func (h *Handler) CreateUpdate(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil)
		return
	}

	var req CreateUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	params := service.CreateUpdateParams{
		UpdateType:  req.UpdateType,
		Title:       req.Title,
		Body:        req.Body,
		MediaIDs:    req.MediaIDs,
		Metadata:    req.Metadata,
		ScheduledAt: req.ScheduledAt,
	}

	update, err := h.svc.CreateUpdate(c.Request.Context(), channelID, actorID, params)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, update, nil)
}

func (h *Handler) ListUpdates(c *gin.Context) {
	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil)
		return
	}

	var viewerID *uuid.UUID
	if uid, err := uuid.Parse(c.GetHeader("X-User-Id")); err == nil {
		viewerID = &uid
	}

	limit, offset := parsePagination(c)
	updates, err := h.svc.ListUpdates(c.Request.Context(), channelID, viewerID, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, updates, nil)
}

func (h *Handler) EditUpdate(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil)
		return
	}

	updateID, err := uuid.Parse(c.Param("updateId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid update ID", nil)
		return
	}

	var req CreateUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	params := service.CreateUpdateParams{
		UpdateType:  req.UpdateType,
		Title:       req.Title,
		Body:        req.Body,
		MediaIDs:    req.MediaIDs,
		Metadata:    req.Metadata,
		ScheduledAt: req.ScheduledAt,
	}

	update, err := h.svc.EditUpdate(c.Request.Context(), channelID, updateID, actorID, params)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, update, nil)
}

func (h *Handler) DeleteUpdate(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil)
		return
	}

	updateID, err := uuid.Parse(c.Param("updateId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid update ID", nil)
		return
	}

	if err := h.svc.DeleteUpdate(c.Request.Context(), channelID, updateID, actorID); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

func (h *Handler) GetMyChannels(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	limit, offset := parsePagination(c)
	channels, err := h.svc.GetMyChannels(c.Request.Context(), actorID, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, channels, nil)
}

func (h *Handler) DiscoverChannels(c *gin.Context) {
	limit, offset := parsePagination(c)

	var viewerID *uuid.UUID
	if uid, err := uuid.Parse(c.GetHeader("X-User-Id")); err == nil {
		viewerID = &uid
	}

	channels, err := h.svc.DiscoverChannels(c.Request.Context(), viewerID, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, channels, nil)
}

func (h *Handler) Health(c *gin.Context) {
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"}, nil)
}
