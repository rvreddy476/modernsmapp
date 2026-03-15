package http

import (
	"net/http"
	"strconv"
	"time"

	store "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// --- Conversation Settings ---

func (h *Handler) GetConversationSettings(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	settings, err := h.svc.GetSettings(c.Request.Context(), userID, convID)
	if err != nil {
		h.log.Warn("failed to get conversation settings", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, settings, nil)
}

type UpdateConversationSettingsRequest struct {
	Label               *string    `json:"label"`
	IsMuted             *bool      `json:"is_muted"`
	MuteUntil           *time.Time `json:"mute_until"`
	DisappearAfterMs    *int64     `json:"disappear_after_ms"`
	ReadReceiptsEnabled *bool      `json:"read_receipts_enabled"`
	Theme               *string    `json:"theme"`
	IsPinned            *bool      `json:"is_pinned"`
}

func (h *Handler) UpdateConversationSettings(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	var req UpdateConversationSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	// Fetch current settings first, then merge
	current, err := h.svc.GetSettings(c.Request.Context(), userID, convID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if req.Label != nil {
		current.Label = req.Label
	}
	if req.IsMuted != nil {
		current.IsMuted = *req.IsMuted
	}
	if req.MuteUntil != nil {
		current.MuteUntil = req.MuteUntil
	}
	if req.DisappearAfterMs != nil {
		current.DisappearAfterMs = req.DisappearAfterMs
	}
	if req.ReadReceiptsEnabled != nil {
		current.ReadReceiptsEnabled = *req.ReadReceiptsEnabled
	}
	if req.Theme != nil {
		current.Theme = *req.Theme
	}
	if req.IsPinned != nil {
		current.IsPinned = *req.IsPinned
		if *req.IsPinned {
			now := time.Now()
			current.PinnedAt = &now
		} else {
			current.PinnedAt = nil
		}
	}

	if err := h.svc.UpdateSettings(c.Request.Context(), current); err != nil {
		h.log.Warn("failed to update conversation settings", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, current, nil)
}

// --- Chat Folders ---

type CreateChatFolderRequest struct {
	Name      string `json:"name" binding:"required,max=50"`
	Icon      string `json:"icon"`
	SortOrder int    `json:"sort_order"`
}

func (h *Handler) CreateChatFolder(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	var req CreateChatFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	icon := req.Icon
	if icon == "" {
		icon = "folder"
	}

	folder, err := h.svc.CreateFolder(c.Request.Context(), userID, req.Name, icon, req.SortOrder)
	if err != nil {
		h.log.Error("failed to create chat folder", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, folder, nil)
}

func (h *Handler) ListChatFolders(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	folders, err := h.svc.ListFolders(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to list chat folders", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list folders", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, folders, nil)
}

func (h *Handler) DeleteChatFolder(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	folderIDStr := c.Param("folderId")
	folderID, err := uuid.Parse(folderIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid folder ID format", nil, nil)
		return
	}

	if err := h.svc.DeleteFolder(c.Request.Context(), userID, folderID); err != nil {
		h.log.Warn("failed to delete chat folder", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "folder deleted"}, nil)
}

type AddConversationToFolderRequest struct {
	ConversationID string `json:"conversation_id" binding:"required"`
}

func (h *Handler) AddConversationToFolder(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	folderIDStr := c.Param("folderId")
	folderID, err := uuid.Parse(folderIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid folder ID format", nil, nil)
		return
	}

	var req AddConversationToFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	convID, err := uuid.Parse(req.ConversationID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid conversation_id format", nil, nil)
		return
	}

	if err := h.svc.AddToFolder(c.Request.Context(), userID, folderID, convID); err != nil {
		h.log.Warn("failed to add conversation to folder", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "conversation added to folder"}, nil)
}

func (h *Handler) RemoveConversationFromFolder(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	folderIDStr := c.Param("folderId")
	folderID, err := uuid.Parse(folderIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid folder ID format", nil, nil)
		return
	}

	convIDStr := c.Param("conversationId")
	convID, err := uuid.Parse(convIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid conversation ID format", nil, nil)
		return
	}

	if err := h.svc.RemoveFromFolder(c.Request.Context(), userID, folderID, convID); err != nil {
		h.log.Warn("failed to remove conversation from folder", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "conversation removed from folder"}, nil)
}

// --- Pins ---

type PinMessageRequest struct {
	MessageID string `json:"message_id" binding:"required"`
}

func (h *Handler) PinMessage(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	var req PinMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	messageID, err := uuid.Parse(req.MessageID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid message_id format", nil, nil)
		return
	}

	pin, err := h.svc.PinMessageSvc(c.Request.Context(), userID, convID, messageID)
	if err != nil {
		h.log.Warn("failed to pin message", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, pin, nil)
}

func (h *Handler) UnpinMessage(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	pinIDStr := c.Param("pinId")
	pinID, err := uuid.Parse(pinIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid pin ID format", nil, nil)
		return
	}

	if err := h.svc.UnpinMessageSvc(c.Request.Context(), userID, pinID); err != nil {
		h.log.Warn("failed to unpin message", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "message unpinned"}, nil)
}

func (h *Handler) GetConversationPins(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	pins, err := h.svc.GetPins(c.Request.Context(), userID, convID)
	if err != nil {
		h.log.Warn("failed to get conversation pins", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, pins, nil)
}

// --- Message Requests ---

func (h *Handler) ListMessageRequests(c *gin.Context) {
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
	offset := 0
	if o := c.Query("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			offset = n
		}
	}

	requests, err := h.svc.ListRequests(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.log.Error("failed to list message requests", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list requests", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, requests, nil)
}

func (h *Handler) AcceptMessageRequest(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	if err := h.svc.AcceptRequest(c.Request.Context(), userID, convID); err != nil {
		h.log.Warn("failed to accept message request", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "request accepted"}, nil)
}

func (h *Handler) DeclineMessageRequest(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	if err := h.svc.DeclineRequest(c.Request.Context(), userID, convID); err != nil {
		h.log.Warn("failed to decline message request", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "request declined"}, nil)
}

// --- Starred Messages ---

type StarMessageRequest struct {
	MessagePreview *string `json:"message_preview"`
}

func (h *Handler) StarMessage(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	messageIDStr := c.Param("messageId")
	messageID, err := uuid.Parse(messageIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid message ID format", nil, nil)
		return
	}

	var req StarMessageRequest
	_ = c.ShouldBindJSON(&req) // optional body

	starred, err := h.svc.StarMessageSvc(c.Request.Context(), userID, convID, messageID, req.MessagePreview)
	if err != nil {
		h.log.Warn("failed to star message", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, starred, nil)
}

func (h *Handler) UnstarMessage(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	messageIDStr := c.Param("messageId")
	messageID, err := uuid.Parse(messageIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid message ID format", nil, nil)
		return
	}

	if err := h.svc.UnstarMessageSvc(c.Request.Context(), userID, messageID); err != nil {
		h.log.Warn("failed to unstar message", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "message unstarred"}, nil)
}

func (h *Handler) GetStarredMessages(c *gin.Context) {
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
	offset := 0
	if o := c.Query("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			offset = n
		}
	}

	starred, err := h.svc.GetStarredMessages(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.log.Error("failed to get starred messages", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get starred messages", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, starred, nil)
}

// --- Backups ---

type CreateChatBackupRequest struct {
	KeyHint *string `json:"key_hint"`
}

func (h *Handler) CreateChatBackup(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	var req CreateChatBackupRequest
	_ = c.ShouldBindJSON(&req) // optional body

	backup, err := h.svc.CreateBackup(c.Request.Context(), userID, req.KeyHint)
	if err != nil {
		h.log.Error("failed to create chat backup", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create backup", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, backup, nil)
}

func (h *Handler) GetLatestChatBackup(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	backup, err := h.svc.GetLatestBackup(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to get latest chat backup", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get backup", nil, nil)
		return
	}
	if backup == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "No backup found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, backup, nil)
}

// --- Scheduled Messages ---

type CreateScheduledMessageRequest struct {
	Type    string     `json:"type" binding:"required,oneof=text media"`
	Content string     `json:"content"`
	MediaID *uuid.UUID `json:"media_id"`
	SendAt  time.Time  `json:"send_at" binding:"required"`
}

func (h *Handler) CreateScheduledMessage(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	var req CreateScheduledMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	if req.SendAt.Before(time.Now()) {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "send_at must be in the future", nil, nil)
		return
	}

	msg, err := h.svc.CreateScheduledMessage(c.Request.Context(), convID, userID, req.Type, req.Content, req.MediaID, req.SendAt)
	if err != nil {
		h.log.Warn("failed to create scheduled message", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, msg, nil)
}

func (h *Handler) CancelScheduledMessage(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	msgIDStr := c.Param("msgId")
	msgID, err := uuid.Parse(msgIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid message ID format", nil, nil)
		return
	}

	if err := h.svc.CancelScheduledMessageSvc(c.Request.Context(), userID, msgID); err != nil {
		h.log.Warn("failed to cancel scheduled message", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "scheduled message cancelled"}, nil)
}

func (h *Handler) ListScheduledMessages(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	msgs, err := h.svc.ListScheduledMessagesSvc(c.Request.Context(), userID, convID)
	if err != nil {
		h.log.Warn("failed to list scheduled messages", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, msgs, nil)
}

// --- Translation ---

func (h *Handler) GetMessageTranslation(c *gin.Context) {
	_, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	messageIDStr := c.Param("messageId")
	messageID, err := uuid.Parse(messageIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid message ID format", nil, nil)
		return
	}

	lang := c.Query("lang")
	if lang == "" {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "lang query parameter is required", nil, nil)
		return
	}

	translation, err := h.svc.GetTranslation(c.Request.Context(), messageID, lang)
	if err != nil {
		h.log.Error("failed to get message translation", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get translation", nil, nil)
		return
	}
	if translation == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Translation not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, translation, nil)
}

// --- Threads ---

func (h *Handler) GetOrCreateThread(c *gin.Context) {
	_, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	parentMsgIDStr := c.Param("parentMessageId")
	parentMsgID, err := uuid.Parse(parentMsgIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid parent message ID format", nil, nil)
		return
	}

	thread, err := h.svc.GetOrCreateThreadSvc(c.Request.Context(), convID, parentMsgID)
	if err != nil {
		h.log.Error("failed to get or create thread", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get thread", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, thread, nil)
}

func (h *Handler) ListConversationThreads(c *gin.Context) {
	_, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}

	threads, err := h.svc.ListThreadsSvc(c.Request.Context(), convID)
	if err != nil {
		h.log.Error("failed to list conversation threads", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list threads", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, threads, nil)
}

// ensure store import is used (types referenced in interface)
var _ *store.ConversationSettings
