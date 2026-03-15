package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/chat-message-service/internal/service"
	"github.com/atpost/chat-message-service/internal/store/scylla"
	"github.com/atpost/chat-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type ChatService interface {
	CreateDirectConversation(ctx context.Context, userID, otherID uuid.UUID, idempotencyKey string) (*service.ConversationResponse, error)
	CreateGroupConversation(ctx context.Context, userID uuid.UUID, title string, memberIDs []uuid.UUID, idempotencyKey string) (*service.ConversationResponse, error)
	GetConversation(ctx context.Context, userID, conversationID uuid.UUID) (*service.ConversationResponse, error)
	ListConversations(ctx context.Context, userID uuid.UUID, limit int, cursor *service.ConversationCursor) ([]service.ConversationResponse, *service.ConversationCursor, error)
	AddMember(ctx context.Context, userID, conversationID, targetUserID uuid.UUID) error
	RemoveMember(ctx context.Context, userID, conversationID, targetUserID uuid.UUID) error
	UpdateTitle(ctx context.Context, userID, conversationID uuid.UUID, title string) error
	SendMessage(ctx context.Context, userID, conversationID uuid.UUID, msgType, text string, mediaID *uuid.UUID, idempotencyKey string) (*service.MessageResponse, error)
	GetMessages(ctx context.Context, userID, conversationID uuid.UUID, cursor *scylla.MessageCursor, limit int) ([]service.MessageResponse, *scylla.MessageCursor, error)
	DeleteMessage(ctx context.Context, userID, conversationID, messageID uuid.UUID, bucket string, ts time.Time) error
	ToggleReaction(ctx context.Context, userID, conversationID, messageID uuid.UUID, bucket string, ts time.Time, emoji string) (*service.ToggleReactionResponse, error)
	SetTyping(ctx context.Context, userID, conversationID uuid.UUID) error
	MarkRead(ctx context.Context, userID, conversationID uuid.UUID, messageID string) error
	GetPresence(ctx context.Context, userIDs []uuid.UUID) (map[string]bool, error)
}

type Handler struct {
	svc ChatService
	log *slog.Logger
}

func New(svc ChatService, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log}
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	v1 := r.Group("/v1/chat")
	{
		v1.GET("/health", h.Health)
		// Conversations
		v1.POST("/conversations/direct", h.CreateDirectConversation)
		v1.POST("/conversations/group", h.CreateGroupConversation)
		v1.GET("/conversations", h.ListConversations)
		v1.GET("/conversations/:id", h.GetConversation)
		v1.POST("/conversations/:id/members", h.AddMember)
		v1.DELETE("/conversations/:id/members/:userId", h.RemoveMember)
		v1.PUT("/conversations/:id", h.UpdateConversation)
		// Messages
		v1.POST("/conversations/:id/messages", h.SendMessage)
		v1.GET("/conversations/:id/messages", h.GetMessages)
		v1.DELETE("/conversations/:id/messages/:messageId", h.DeleteMessage)
		v1.PUT("/conversations/:id/messages/:messageId/reactions", h.ToggleReaction)
		// Typing & Read receipts
		v1.POST("/conversations/:id/typing", h.SetTyping)
		v1.POST("/conversations/:id/read", h.MarkRead)
		// Presence
		v1.POST("/presence", h.GetPresence)
	}
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// --- Conversations ---

type CreateDirectConversationRequest struct {
	OtherUserID string `json:"other_user_id" binding:"required"`
}

func (h *Handler) CreateDirectConversation(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	var req CreateDirectConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}
	otherID, err := uuid.Parse(req.OtherUserID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid other_user_id format", nil, nil)
		return
	}
	idempotencyKey := c.GetHeader("Idempotency-Key")

	conv, err := h.svc.CreateDirectConversation(c.Request.Context(), userID, otherID, idempotencyKey)
	if err != nil {
		if handled := writeIdempotencyError(c, err); handled {
			return
		}
		h.log.Error("failed to create direct conversation", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, conv, nil)
}

type CreateGroupConversationRequest struct {
	Title     string   `json:"title" binding:"required,min=1,max=100"`
	MemberIDs []string `json:"member_ids" binding:"required,min=1"`
}

func (h *Handler) CreateGroupConversation(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	var req CreateGroupConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	memberIDs := make([]uuid.UUID, 0, len(req.MemberIDs))
	for _, idStr := range req.MemberIDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid member_id: "+idStr, nil, nil)
			return
		}
		memberIDs = append(memberIDs, id)
	}
	idempotencyKey := c.GetHeader("Idempotency-Key")

	conv, err := h.svc.CreateGroupConversation(c.Request.Context(), userID, req.Title, memberIDs, idempotencyKey)
	if err != nil {
		if handled := writeIdempotencyError(c, err); handled {
			return
		}
		h.log.Error("failed to create group conversation", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, conv, nil)
}

func (h *Handler) GetConversation(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	conv, err := h.svc.GetConversation(c.Request.Context(), userID, convID)
	if err != nil {
		h.log.Warn("failed to get conversation", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, conv, nil)
}

func (h *Handler) ListConversations(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	limit := 20
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	var cursor *service.ConversationCursor
	if raw := c.Query("cursor"); raw != "" {
		cur, err := decodeConvCursor(raw)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_CURSOR", "Invalid cursor", nil, nil)
			return
		}
		cursor = cur
	}

	convs, next, err := h.svc.ListConversations(c.Request.Context(), userID, limit, cursor)
	if err != nil {
		h.log.Error("failed to list conversations", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list conversations", nil, nil)
		return
	}

	var meta *api.Meta
	if next != nil {
		if encoded, err := encodeConvCursor(*next); err == nil {
			meta = &api.Meta{NextCursor: encoded}
		}
	}

	api.JSON(c.Writer, http.StatusOK, convs, meta)
}

// --- Member Management ---

type AddMemberRequest struct {
	UserID string `json:"user_id" binding:"required"`
}

func (h *Handler) AddMember(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	var req AddMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}
	targetID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid user_id format", nil, nil)
		return
	}

	if err := h.svc.AddMember(c.Request.Context(), userID, convID, targetID); err != nil {
		h.log.Warn("failed to add member", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "member added"}, nil)
}

func (h *Handler) RemoveMember(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	targetIDStr := c.Param("userId")
	targetID, err := uuid.Parse(targetIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid user ID format", nil, nil)
		return
	}

	if err := h.svc.RemoveMember(c.Request.Context(), userID, convID, targetID); err != nil {
		h.log.Warn("failed to remove member", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "member removed"}, nil)
}

type UpdateConversationRequest struct {
	Title string `json:"title" binding:"required,min=1,max=100"`
}

func (h *Handler) UpdateConversation(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	var req UpdateConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	if err := h.svc.UpdateTitle(c.Request.Context(), userID, convID, req.Title); err != nil {
		h.log.Warn("failed to update conversation", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "title updated"}, nil)
}

// --- Messages ---

type SendMessageRequest struct {
	Type    string     `json:"type" binding:"required,oneof=text media"`
	Text    string     `json:"text" binding:"max=2000"`
	MediaID *uuid.UUID `json:"media_id"`
}

func (h *Handler) SendMessage(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	var req SendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	if req.Type == "text" && req.Text == "" {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Text is required for text messages", nil, nil)
		return
	}
	if req.Type == "media" && req.MediaID == nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "media_id is required for media messages", nil, nil)
		return
	}
	idempotencyKey := c.GetHeader("Idempotency-Key")

	msg, err := h.svc.SendMessage(c.Request.Context(), userID, convID, req.Type, req.Text, req.MediaID, idempotencyKey)
	if err != nil {
		if handled := writeIdempotencyError(c, err); handled {
			return
		}
		h.log.Error("failed to send message", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, msg, nil)
}

func (h *Handler) GetMessages(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	limit := 30
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	var cursor *scylla.MessageCursor
	if raw := c.Query("cursor"); raw != "" {
		cur, err := decodeMsgCursor(raw)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_CURSOR", "Invalid cursor", nil, nil)
			return
		}
		cursor = cur
	}

	msgs, nextCursor, err := h.svc.GetMessages(c.Request.Context(), userID, convID, cursor, limit)
	if err != nil {
		h.log.Error("failed to get messages", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	var meta *api.Meta
	if nextCursor != nil {
		if encoded, err := encodeMsgCursor(*nextCursor); err == nil {
			meta = &api.Meta{NextCursor: encoded}
		}
	}

	api.JSON(c.Writer, http.StatusOK, msgs, meta)
}

type DeleteMessageRequest struct {
	Bucket string    `json:"bucket" binding:"required"`
	Ts     time.Time `json:"ts" binding:"required"`
}

func (h *Handler) DeleteMessage(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	msgIDStr := c.Param("messageId")
	msgID, err := uuid.Parse(msgIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid message ID format", nil, nil)
		return
	}

	var req DeleteMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	if err := h.svc.DeleteMessage(c.Request.Context(), userID, convID, msgID, req.Bucket, req.Ts); err != nil {
		h.log.Error("failed to delete message", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "message deleted"}, nil)
}

// --- Reactions ---

type ToggleReactionRequest struct {
	Emoji  string    `json:"emoji" binding:"required"`
	Bucket string    `json:"bucket" binding:"required"`
	Ts     time.Time `json:"ts" binding:"required"`
}

func (h *Handler) ToggleReaction(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	msgIDStr := c.Param("messageId")
	msgID, err := uuid.Parse(msgIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid message ID format", nil, nil)
		return
	}

	var req ToggleReactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	result, err := h.svc.ToggleReaction(c.Request.Context(), userID, convID, msgID, req.Bucket, req.Ts, req.Emoji)
	if err != nil {
		h.log.Warn("failed to toggle reaction", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

// --- Typing & Read Receipts ---

func (h *Handler) SetTyping(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	if err := h.svc.SetTyping(c.Request.Context(), userID, convID); err != nil {
		h.log.Warn("failed to set typing", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"}, nil)
}

type MarkReadRequest struct {
	MessageID string `json:"message_id" binding:"required"`
}

func (h *Handler) MarkRead(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	var req MarkReadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	if err := h.svc.MarkRead(c.Request.Context(), userID, convID, req.MessageID); err != nil {
		h.log.Warn("failed to mark read", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"}, nil)
}

// --- Helpers ---

func getUserID(c *gin.Context, log *slog.Logger) (uuid.UUID, bool) {
	rawUserID, ok := c.Get(userIDKey)
	if !ok {
		log.Warn("missing authenticated user context", "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user authentication", nil, nil)
		return uuid.Nil, false
	}

	idStr, ok := rawUserID.(string)
	if !ok || idStr == "" {
		log.Warn("invalid authenticated user context", "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user authentication", nil, nil)
		return uuid.Nil, false
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		log.Warn("invalid authenticated user id", "value", idStr, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user authentication", nil, nil)
		return uuid.Nil, false
	}
	return id, true
}

func parseConvID(c *gin.Context) (uuid.UUID, bool) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid conversation ID format", nil, nil)
		return uuid.Nil, false
	}
	return id, true
}

// --- Cursor encoding ---

type convCursorPayload struct {
	Ts time.Time `json:"ts"`
	ID string    `json:"id"`
}

func encodeConvCursor(cur service.ConversationCursor) (string, error) {
	payload := convCursorPayload{Ts: cur.UpdatedAt, ID: cur.ID.String()}
	raw, _ := json.Marshal(payload)
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeConvCursor(raw string) (*service.ConversationCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	var payload convCursorPayload
	if err := json.Unmarshal(b, &payload); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(payload.ID)
	if err != nil {
		return nil, err
	}
	return &service.ConversationCursor{UpdatedAt: payload.Ts, ID: id}, nil
}

type msgCursorPayload struct {
	Bucket string    `json:"b"`
	Ts     time.Time `json:"t"`
	MsgID  string    `json:"m"`
}

func encodeMsgCursor(cur scylla.MessageCursor) (string, error) {
	payload := msgCursorPayload{Bucket: cur.Bucket, Ts: cur.Ts, MsgID: cur.MsgID.String()}
	raw, _ := json.Marshal(payload)
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeMsgCursor(raw string) (*scylla.MessageCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	var payload msgCursorPayload
	if err := json.Unmarshal(b, &payload); err != nil {
		return nil, err
	}
	msgID, err := uuid.Parse(payload.MsgID)
	if err != nil {
		return nil, err
	}
	return &scylla.MessageCursor{Bucket: payload.Bucket, Ts: payload.Ts, MsgID: msgID}, nil
}

// --- Presence ---

type GetPresenceRequest struct {
	UserIDs []string `json:"user_ids" binding:"required"`
}

func (h *Handler) GetPresence(c *gin.Context) {
	var req GetPresenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	if len(req.UserIDs) > 200 {
		api.Error(c.Writer, http.StatusBadRequest, "TOO_MANY_IDS", "Maximum 200 user IDs per request", nil, nil)
		return
	}

	userIDs := make([]uuid.UUID, 0, len(req.UserIDs))
	for _, raw := range req.UserIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			continue
		}
		userIDs = append(userIDs, id)
	}

	presence, err := h.svc.GetPresence(c.Request.Context(), userIDs)
	if err != nil {
		h.log.Error("failed to get presence", "err", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL", "Failed to check presence", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, presence, nil)
}

func writeIdempotencyError(c *gin.Context, err error) bool {
	switch {
	case errors.Is(err, service.ErrIdempotencyKeyRequired):
		api.Error(c.Writer, http.StatusBadRequest, "MISSING_IDEMPOTENCY_KEY", err.Error(), nil, nil)
		return true
	case errors.Is(err, service.ErrIdempotencyConflict):
		api.Error(c.Writer, http.StatusConflict, "IDEMPOTENCY_KEY_CONFLICT", err.Error(), nil, nil)
		return true
	case errors.Is(err, service.ErrIdempotencyInProgress):
		api.Error(c.Writer, http.StatusConflict, "IDEMPOTENCY_IN_PROGRESS", err.Error(), nil, nil)
		return true
	default:
		return false
	}
}
