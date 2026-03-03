package http

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/facebook-like/message-service/internal/service"
	pgstore "github.com/facebook-like/message-service/internal/store/postgres"
	"github.com/facebook-like/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	svc *service.Service
	db  *pgxpool.Pool
}

func New(svc *service.Service, db *pgxpool.Pool) *Handler {
	return &Handler{svc: svc, db: db}
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	v1 := r.Group("/v1/chat")
	{
		// Legacy direct message endpoints
		v1.POST("/messages/:receiverId", h.SendMessage)
		v1.GET("/messages/:receiverId", h.GetMessages)

		// Conversations
		v1.POST("/conversations/direct", h.CreateDirectConversation)
		v1.POST("/conversations/group", h.CreateGroupConversation)
		v1.GET("/conversations", h.ListConversations)
		v1.PATCH("/conversations/:conversationId", h.UpdateConversation)
		v1.DELETE("/conversations/:conversationId", h.LeaveConversation)

		// Messages within conversations
		v1.POST("/conversations/:conversationId/messages", h.SendMessageToConversation)
		v1.GET("/conversations/:conversationId/messages", h.GetMessagesByConversation)
		v1.PATCH("/conversations/:conversationId/messages/:messageId", h.EditMessage)
		v1.DELETE("/conversations/:conversationId/messages/:messageId", h.DeleteMessage)

		// Reactions (Scylla-backed toggle, conversation-scoped)
		v1.PUT("/conversations/:conversationId/messages/:messageId/reactions", h.ToggleReaction)

		// Reactions (Postgres-backed, message-scoped)
		v1.POST("/messages/:messageId/reactions", h.ReactToMessage)
		v1.DELETE("/messages/:messageId/reactions", h.UnreactToMessage)
		v1.GET("/messages/:messageId/reactions", h.GetMessageReactions)

		// Read receipts & typing
		v1.POST("/conversations/:conversationId/read", h.MarkRead)
		v1.POST("/conversations/:conversationId/typing", h.SetTyping)

		// Group members
		v1.POST("/conversations/:conversationId/members", h.AddMember)
		v1.DELETE("/conversations/:conversationId/members/:userId", h.RemoveMember)

		// Pinned messages
		v1.POST("/conversations/:id/pin/:msgId", h.PinMessage)
		v1.DELETE("/conversations/:id/pin", h.UnpinMessage)
		v1.GET("/conversations/:id/pin", h.GetPinnedMessage)
	}
}

// ---------------------------------------------------------------------------
// Request types
// ---------------------------------------------------------------------------

type SendMessageRequest struct {
	Text            string `json:"text"`
	MessageType     string `json:"message_type"`
	MediaID         string `json:"media_id"`
	ReplyToID       string `json:"reply_to_id"`
	ForwardedFromID string `json:"forwarded_from_id"`
}

type EditMessageRequest struct {
	Text      string `json:"text" binding:"required,min=1,max=2000"`
	Timestamp string `json:"timestamp" binding:"required"`
}

type DeleteMessageRequest struct {
	Timestamp string `json:"timestamp" binding:"required"`
}

type ToggleReactionRequest struct {
	Emoji string `json:"emoji" binding:"required,min=1,max=8"`
}

type MarkReadRequest struct {
	MessageID string `json:"message_id" binding:"required"`
}

type UpdateConversationRequest struct {
	Name    *string `json:"name"`
	IconURL *string `json:"icon_url"`
}

type AddMemberRequest struct {
	UserID string `json:"user_id" binding:"required"`
}

type CreateDirectConversationRequest struct {
	OtherUserID string `json:"other_user_id" binding:"required"`
}

type CreateGroupConversationRequest struct {
	Title     string   `json:"title" binding:"required"`
	MemberIDs []string `json:"member_ids" binding:"required"`
}

// ---------------------------------------------------------------------------
// Legacy direct message handlers
// ---------------------------------------------------------------------------

func (h *Handler) SendMessage(c *gin.Context) {
	senderID, ok := getUserID(c)
	if !ok {
		return
	}

	receiverIDStr := c.Param("receiverId")
	receiverID, err := uuid.Parse(receiverIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid receiver ID format", nil, nil)
		return
	}

	if senderID == receiverID {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Cannot send message to yourself", nil, nil)
		return
	}

	var req SendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}
	if req.Text == "" && req.MediaID == "" {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Text or media_id is required", nil, nil)
		return
	}

	msg, err := h.svc.SendMessage(c.Request.Context(), senderID, receiverID, service.SendMessageInput{
		Text:            req.Text,
		MessageType:     req.MessageType,
		MediaID:         req.MediaID,
		ReplyToID:       req.ReplyToID,
		ForwardedFromID: req.ForwardedFromID,
	})
	if err != nil {
		slog.Error("failed to send message", "error", err, "sender", senderID, "receiver", receiverID)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to deliver message", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, msg, nil)
}

func (h *Handler) GetMessages(c *gin.Context) {
	senderID, ok := getUserID(c)
	if !ok {
		return
	}

	receiverIDStr := c.Param("receiverId")
	receiverID, err := uuid.Parse(receiverIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid receiver ID format", nil, nil)
		return
	}

	limitStr := c.Query("limit")
	limit := 30
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	msgs, err := h.svc.GetMessages(c.Request.Context(), senderID, receiverID, limit)
	if err != nil {
		slog.Error("failed to get messages", "error", err, "sender", senderID, "receiver", receiverID)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to retrieve conversation history", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, msgs, nil)
}

// ---------------------------------------------------------------------------
// Conversation handlers
// ---------------------------------------------------------------------------

func (h *Handler) CreateDirectConversation(c *gin.Context) {
	userID, ok := getUserID(c)
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
	if otherID == userID {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Cannot start conversation with yourself", nil, nil)
		return
	}

	conv, err := h.svc.CreateDirectConversation(c.Request.Context(), userID, otherID)
	if err != nil {
		slog.Error("failed to create direct conversation", "error", err, "user_id", userID, "other_user_id", otherID)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create conversation", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, conv, nil)
}

func (h *Handler) CreateGroupConversation(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var req CreateGroupConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	var memberIDs []uuid.UUID
	for _, idStr := range req.MemberIDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid member ID: "+idStr, nil, nil)
			return
		}
		memberIDs = append(memberIDs, id)
	}

	convID, err := h.svc.CreateGroupConversation(c.Request.Context(), userID, memberIDs, req.Title)
	if err != nil {
		slog.Error("failed to create group conversation", "error", err, "user_id", userID)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create group conversation", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"id": convID}, nil)
}

func (h *Handler) ListConversations(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	limit := 20
	if limitStr := c.Query("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	var cursor *service.ConversationCursor
	if raw := c.Query("cursor"); raw != "" {
		cur, err := decodeCursor(raw)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_CURSOR", "Invalid cursor", nil, nil)
			return
		}
		cursor = cur
	}

	convs, next, err := h.svc.ListConversations(c.Request.Context(), userID, limit, cursor)
	if err != nil {
		slog.Error("failed to list conversations", "error", err, "user_id", userID)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list conversations", nil, nil)
		return
	}

	var meta *api.Meta
	if next != nil {
		if encoded, err := encodeCursor(*next); err == nil {
			meta = &api.Meta{NextCursor: encoded}
		}
	}

	api.JSON(c.Writer, http.StatusOK, convs, meta)
}

func (h *Handler) UpdateConversation(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	convID, ok := parseConversationID(c)
	if !ok {
		return
	}

	var req UpdateConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	if err := h.svc.UpdateConversation(c.Request.Context(), userID, convID, req.Name, req.IconURL); err != nil {
		slog.Error("failed to update conversation", "error", err)
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"updated": true}, nil)
}

func (h *Handler) LeaveConversation(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	convID, ok := parseConversationID(c)
	if !ok {
		return
	}

	if err := h.svc.LeaveConversation(c.Request.Context(), userID, convID); err != nil {
		slog.Error("failed to leave conversation", "error", err)
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"left": true}, nil)
}

// ---------------------------------------------------------------------------
// Message handlers
// ---------------------------------------------------------------------------

func (h *Handler) SendMessageToConversation(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	convID, ok := parseConversationID(c)
	if !ok {
		return
	}

	var req SendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}
	if req.Text == "" && req.MediaID == "" {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Text or media_id is required", nil, nil)
		return
	}

	msg, err := h.svc.SendMessageToConversation(c.Request.Context(), userID, convID, service.SendMessageInput{
		Text:            req.Text,
		MessageType:     req.MessageType,
		MediaID:         req.MediaID,
		ReplyToID:       req.ReplyToID,
		ForwardedFromID: req.ForwardedFromID,
	})
	if err != nil {
		slog.Error("failed to send message to conversation", "error", err, "user_id", userID, "conversation_id", convID)
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, msg, nil)
}

func (h *Handler) GetMessagesByConversation(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	convID, ok := parseConversationID(c)
	if !ok {
		return
	}

	limit := 30
	if limitStr := c.Query("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	msgs, err := h.svc.GetMessagesByConversation(c.Request.Context(), userID, convID, limit)
	if err != nil {
		slog.Error("failed to get conversation messages", "error", err, "user_id", userID, "conversation_id", convID)
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, msgs, nil)
}

func (h *Handler) EditMessage(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	convID, ok := parseConversationID(c)
	if !ok {
		return
	}
	msgID := c.Param("messageId")
	if msgID == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Missing message ID", nil, nil)
		return
	}

	var req EditMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	ts, err := time.Parse(time.RFC3339Nano, req.Timestamp)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid timestamp format", nil, nil)
		return
	}

	if err := h.svc.EditMessage(c.Request.Context(), userID, convID, msgID, ts, req.Text); err != nil {
		slog.Error("failed to edit message", "error", err)
		status := http.StatusBadRequest
		if err.Error() == "can only edit your own messages" {
			status = http.StatusForbidden
		}
		api.Error(c.Writer, status, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"edited": true}, nil)
}

func (h *Handler) DeleteMessage(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	convID, ok := parseConversationID(c)
	if !ok {
		return
	}
	msgID := c.Param("messageId")
	if msgID == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Missing message ID", nil, nil)
		return
	}

	var req DeleteMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	ts, err := time.Parse(time.RFC3339Nano, req.Timestamp)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid timestamp format", nil, nil)
		return
	}

	if err := h.svc.DeleteMessage(c.Request.Context(), userID, convID, msgID, ts); err != nil {
		slog.Error("failed to delete message", "error", err)
		status := http.StatusBadRequest
		if err.Error() == "can only delete your own messages" {
			status = http.StatusForbidden
		}
		api.Error(c.Writer, status, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"deleted": true}, nil)
}

// ---------------------------------------------------------------------------
// Reactions
// ---------------------------------------------------------------------------

func (h *Handler) ToggleReaction(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	convID, ok := parseConversationID(c)
	if !ok {
		return
	}
	msgID := c.Param("messageId")
	if msgID == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Missing message ID", nil, nil)
		return
	}

	var req ToggleReactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	added, err := h.svc.ToggleReaction(c.Request.Context(), userID, convID, msgID, req.Emoji)
	if err != nil {
		slog.Error("failed to toggle reaction", "error", err)
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"added": added}, nil)
}

// ---------------------------------------------------------------------------
// Read receipts & typing
// ---------------------------------------------------------------------------

func (h *Handler) MarkRead(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	convID, ok := parseConversationID(c)
	if !ok {
		return
	}

	var req MarkReadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	if err := h.svc.MarkRead(c.Request.Context(), userID, convID, req.MessageID); err != nil {
		slog.Error("failed to mark read", "error", err)
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"read": true}, nil)
}

func (h *Handler) SetTyping(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	convID, ok := parseConversationID(c)
	if !ok {
		return
	}

	if err := h.svc.SetTyping(c.Request.Context(), userID, convID); err != nil {
		slog.Error("failed to set typing", "error", err)
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"typing": true}, nil)
}

// ---------------------------------------------------------------------------
// Group member management
// ---------------------------------------------------------------------------

func (h *Handler) AddMember(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	convID, ok := parseConversationID(c)
	if !ok {
		return
	}
	_ = userID // caller must be a member (verified in service)

	var req AddMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}
	targetID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid user_id", nil, nil)
		return
	}

	if err := h.svc.AddGroupConversationMember(c.Request.Context(), convID, targetID); err != nil {
		slog.Error("failed to add member", "error", err)
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"added": true}, nil)
}

func (h *Handler) RemoveMember(c *gin.Context) {
	_, ok := getUserID(c)
	if !ok {
		return
	}
	convID, ok := parseConversationID(c)
	if !ok {
		return
	}
	targetIDStr := c.Param("userId")
	targetID, err := uuid.Parse(targetIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid user ID", nil, nil)
		return
	}

	if err := h.svc.RemoveGroupConversationMember(c.Request.Context(), convID, targetID); err != nil {
		slog.Error("failed to remove member", "error", err)
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"removed": true}, nil)
}

// ---------------------------------------------------------------------------
// Pinned messages
// ---------------------------------------------------------------------------

func (h *Handler) PinMessage(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	convIDStr := c.Param("id")
	convID, err := uuid.Parse(convIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid conversation ID format", nil, nil)
		return
	}

	msgID := c.Param("msgId")
	if msgID == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Missing message ID", nil, nil)
		return
	}

	if err := h.svc.PinMessage(c.Request.Context(), userID, convID, msgID); err != nil {
		slog.Error("failed to pin message", "error", err, "user_id", userID, "conversation_id", convID, "message_id", msgID)
		status := http.StatusBadRequest
		if err.Error() == "not a conversation member" {
			status = http.StatusForbidden
		}
		api.Error(c.Writer, status, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"pinned": true}, nil)
}

func (h *Handler) UnpinMessage(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	convIDStr := c.Param("id")
	convID, err := uuid.Parse(convIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid conversation ID format", nil, nil)
		return
	}

	if err := h.svc.UnpinMessage(c.Request.Context(), userID, convID); err != nil {
		slog.Error("failed to unpin message", "error", err, "user_id", userID, "conversation_id", convID)
		status := http.StatusBadRequest
		if err.Error() == "not a conversation member" {
			status = http.StatusForbidden
		}
		api.Error(c.Writer, status, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"unpinned": true}, nil)
}

func (h *Handler) GetPinnedMessage(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	convIDStr := c.Param("id")
	convID, err := uuid.Parse(convIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid conversation ID format", nil, nil)
		return
	}

	pm, err := h.svc.GetPinnedMessage(c.Request.Context(), userID, convID)
	if err != nil {
		slog.Error("failed to get pinned message", "error", err, "user_id", userID, "conversation_id", convID)
		status := http.StatusBadRequest
		if err.Error() == "not a conversation member" {
			status = http.StatusForbidden
		}
		api.Error(c.Writer, status, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if pm == nil {
		api.JSON(c.Writer, http.StatusOK, gin.H{"pinned_message": nil}, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"pinned_message": pm}, nil)
}

// ---------------------------------------------------------------------------
// Postgres-backed message reactions
// ---------------------------------------------------------------------------

// ReactToMessage POST /v1/chat/messages/:messageId/reactions
func (h *Handler) ReactToMessage(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	messageID := c.Param("messageId")

	var body struct {
		ReactionType string `json:"reaction_type" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", "reaction_type required", nil, nil)
		return
	}

	reaction, err := pgstore.AddReaction(c.Request.Context(), h.db, messageID, userID, body.ReactionType)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "REACT_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, reaction, nil)
}

// UnreactToMessage DELETE /v1/chat/messages/:messageId/reactions
func (h *Handler) UnreactToMessage(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	messageID := c.Param("messageId")
	if err := pgstore.RemoveReaction(c.Request.Context(), h.db, messageID, userID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "UNREACT_FAILED", err.Error(), nil, nil)
		return
	}
	c.Writer.WriteHeader(http.StatusNoContent)
}

// GetMessageReactions GET /v1/chat/messages/:messageId/reactions
func (h *Handler) GetMessageReactions(c *gin.Context) {
	messageID := c.Param("messageId")
	summaries, err := pgstore.GetReactions(c.Request.Context(), h.db, messageID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "FETCH_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, summaries, nil)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func getUserID(c *gin.Context) (uuid.UUID, bool) {
	senderIDStr := c.GetHeader("X-User-Id")
	if senderIDStr == "" {
		slog.Warn("missing X-User-Id header")
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user authentication", nil, nil)
		return uuid.Nil, false
	}

	senderID, err := uuid.Parse(senderIDStr)
	if err != nil {
		slog.Warn("invalid X-User-Id header", "value", senderIDStr)
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user authentication", nil, nil)
		return uuid.Nil, false
	}
	return senderID, true
}

func parseConversationID(c *gin.Context) (uuid.UUID, bool) {
	convIDStr := c.Param("conversationId")
	convID, err := uuid.Parse(convIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid conversation ID format", nil, nil)
		return uuid.Nil, false
	}
	return convID, true
}

type cursorPayload struct {
	Ts time.Time `json:"ts"`
	ID string    `json:"id"`
}

func encodeCursor(cur service.ConversationCursor) (string, error) {
	payload := cursorPayload{Ts: cur.UpdatedAt, ID: cur.ID.String()}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeCursor(raw string) (*service.ConversationCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	var payload cursorPayload
	if err := json.Unmarshal(b, &payload); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(payload.ID)
	if err != nil {
		return nil, err
	}
	return &service.ConversationCursor{UpdatedAt: payload.Ts, ID: id}, nil
}
