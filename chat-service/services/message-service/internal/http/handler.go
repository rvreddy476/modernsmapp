// The chat HTTP handler: the ChatService interface it calls, the Handler
// with its constructor and internal-key wiring, route registration, the
// health check, and the shared request helpers (caller id, conversation id)
// and idempotency/messaging error writers. Each route family's handlers
// live in their own file in this package.

package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/atpost/chat-message-service/internal/service"
	store "github.com/atpost/chat-message-service/internal/store/postgres"
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
	SendMessage(ctx context.Context, userID, conversationID uuid.UUID, msgType, text string, mediaID, postID *uuid.UUID, replyTo *service.ReplyRef, idempotencyKey string) (*service.MessageResponse, error)
	GetMessages(ctx context.Context, userID, conversationID uuid.UUID, cursor *scylla.MessageCursor, limit int) ([]service.MessageResponse, *scylla.MessageCursor, error)
	DeleteMessage(ctx context.Context, userID, conversationID, messageID uuid.UUID, bucket string, ts time.Time) error
	EditMessage(ctx context.Context, userID, conversationID, messageID uuid.UUID, bucket string, ts time.Time, newText string) (*scylla.Message, error)
	ToggleReaction(ctx context.Context, userID, conversationID, messageID uuid.UUID, bucket string, ts time.Time, emoji string) (*service.ToggleReactionResponse, error)
	SetTyping(ctx context.Context, userID, conversationID uuid.UUID) error
	MarkRead(ctx context.Context, userID, conversationID uuid.UUID, messageID string) error
	GetPresence(ctx context.Context, requesterID uuid.UUID, userIDs []uuid.UUID) (map[string]bool, error)
	GetConversationPresence(ctx context.Context, userID, convID uuid.UUID) (*service.ConversationPresence, error)
	// P0-3 dating-match internal entry points.
	CreateDatingMatchConversation(ctx context.Context, userA, userB, matchID uuid.UUID, opts service.DatingMatchOptions) (*service.ConversationResponse, error)
	SetDatingReceiptsUntil(ctx context.Context, matchID, userID uuid.UUID, until *time.Time) error
	SendDatingOpeningAnswer(ctx context.Context, matchID, senderID uuid.UUID, text, idempotencyKey string) (*service.MessageResponse, error)
	CloseDatingMatchConversation(ctx context.Context, matchID uuid.UUID) error
	HasOpenDatingMatch(ctx context.Context, userA, userB uuid.UUID) (bool, error)
	DatingTurnsOwed(ctx context.Context, userID uuid.UUID) (int, error)
	ManagedAddGroupMember(ctx context.Context, conversationID, userID uuid.UUID) error
	ManagedRemoveGroupMember(ctx context.Context, conversationID, userID uuid.UUID) error
	ViewerMayAccessChatMedia(ctx context.Context, viewerID, mediaID uuid.UUID) (bool, error)

	// Conversation settings
	GetSettings(ctx context.Context, userID, convID uuid.UUID) (*store.ConversationSettings, error)
	UpdateSettings(ctx context.Context, settings *store.ConversationSettings) error
	// Folders
	CreateFolder(ctx context.Context, userID uuid.UUID, name, icon string, sortOrder int) (*store.ChatFolder, error)
	ListFolders(ctx context.Context, userID uuid.UUID) ([]store.ChatFolder, error)
	DeleteFolder(ctx context.Context, userID, folderID uuid.UUID) error
	AddToFolder(ctx context.Context, userID, folderID, conversationID uuid.UUID) error
	RemoveFromFolder(ctx context.Context, userID, folderID, conversationID uuid.UUID) error
	// Pins
	PinMessageSvc(ctx context.Context, userID, convID, messageID uuid.UUID) (*store.ConversationPin, error)
	UnpinMessageSvc(ctx context.Context, userID, convID, pinID uuid.UUID) error
	GetPins(ctx context.Context, userID, convID uuid.UUID) ([]store.ConversationPin, error)
	// Message requests
	AcceptRequest(ctx context.Context, userID, convID uuid.UUID) error
	DeclineRequest(ctx context.Context, userID, convID uuid.UUID) error
	BlockRequest(ctx context.Context, userID, convID uuid.UUID) error
	ReportRequest(ctx context.Context, userID, convID uuid.UUID) error
	ListRequests(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.Conversation, error)

	// Group governance (production chat pass, directive §3.4)
	CreateGroupWithPolicy(ctx context.Context, creatorID uuid.UUID, title string, memberIDs []uuid.UUID, idempotencyKey string) (*service.CreateGroupResult, error)
	AddMemberWithPolicy(ctx context.Context, actorID, conversationID, targetID uuid.UUID) (*service.AddOutcome, error)
	RemoveMemberGoverned(ctx context.Context, actorID, conversationID, targetID uuid.UUID) error
	LeaveConversation(ctx context.Context, userID, conversationID uuid.UUID) error
	TransferOwnershipGoverned(ctx context.Context, actorID, conversationID, newOwnerID uuid.UUID) error
	SetMemberRoleGoverned(ctx context.Context, actorID, conversationID, targetID uuid.UUID, role string) error
	UpdateGroupInfoGoverned(ctx context.Context, actorID, conversationID uuid.UUID, title *string, avatarMediaID *uuid.UUID, description *string) error
	ListMyInvitations(ctx context.Context, userID uuid.UUID) ([]store.GroupInvitation, error)
	// Invite links (chat-app pass 2026-09-05)
	CreateInviteLink(ctx context.Context, actorID, conversationID uuid.UUID, ttl time.Duration, maxUses *int) (*service.InviteLinkResponse, error)
	GetInviteLink(ctx context.Context, actorID, conversationID uuid.UUID) (*service.InviteLinkResponse, error)
	RevokeInviteLink(ctx context.Context, actorID, conversationID uuid.UUID) error
	PreviewInvite(ctx context.Context, viewerID uuid.UUID, code string) (*service.InvitePreview, error)
	JoinByInvite(ctx context.Context, userID uuid.UUID, code string) (*service.ConversationResponse, error)
	AcceptGroupInvitation(ctx context.Context, userID, invitationID uuid.UUID) error
	DeclineGroupInvitation(ctx context.Context, userID, invitationID uuid.UUID) error

	// Realtime entitlements (directive §5.3)
	IssueSubscriptionEntitlement(ctx context.Context, userID, conversationID uuid.UUID) (*service.SubscriptionEntitlement, error)
	// Starred messages
	StarMessageSvc(ctx context.Context, userID, convID, messageID uuid.UUID, preview *string) (*store.StarredMessage, error)
	UnstarMessageSvc(ctx context.Context, userID, messageID uuid.UUID) error
	GetStarredMessages(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.StarredMessage, error)
	// Backups
	CreateBackup(ctx context.Context, userID uuid.UUID, keyHint *string) (*store.ChatBackup, error)
	GetLatestBackup(ctx context.Context, userID uuid.UUID) (*store.ChatBackup, error)
	// Scheduled messages
	CreateScheduledMessage(ctx context.Context, convID, senderID uuid.UUID, msgType, content string, mediaID *uuid.UUID, sendAt time.Time) (*store.ScheduledMessage, error)
	CancelScheduledMessageSvc(ctx context.Context, userID, msgID uuid.UUID) error
	ListScheduledMessagesSvc(ctx context.Context, userID, convID uuid.UUID) ([]store.ScheduledMessage, error)
	// Translation
	GetTranslation(ctx context.Context, userID, messageID uuid.UUID, targetLang string) (*store.MessageTranslation, error)
	// Threads
	GetOrCreateThreadSvc(ctx context.Context, userID, convID, parentMessageID uuid.UUID) (*store.MessageThread, error)
	ListThreadsSvc(ctx context.Context, userID, convID uuid.UUID) ([]store.MessageThread, error)
}

type Handler struct {
	svc ChatService
	log *slog.Logger
	// internalServiceKey gates internal-only endpoints (currently
	// only the dating-match conversation creator). Wired via
	// WithInternalServiceKey from cmd/server; empty means the gate
	// rejects everything as misconfigured.
	internalServiceKey string
}

func New(svc ChatService, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log}
}

// WithInternalServiceKey wires the shared internal-service-key used by
// chat-service's internal-only endpoints (currently P0-3
// dating-match). Idempotent — call once from cmd/server.
func (h *Handler) WithInternalServiceKey(key string) *Handler {
	h.internalServiceKey = key
	return h
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	internal := r.Group("/internal/v1/chat")
	internal.POST("/media-access", h.ChatMediaAccess)
	// Dating-match conversations: service-only, called by dating-service
	// once a mutual spark forms. It lives under /internal/v1/chat because
	// the api-gateway proxies /v1/chat (injecting the internal key on every
	// request, anonymous ones included) but has no route to /internal/v1/chat.
	internal.POST("/conversations/dating-match", h.CreateDatingMatchConversation)
	// Dating mechanic M5: dating-service posts a validated opening answer
	// (the waiting member's first message) past the first-move gate. Same
	// gates as the route above.
	internal.POST("/conversations/dating-match/opening-answer", h.DatingOpeningAnswer)
	// Dating mechanic M9: dating-service sets until when a member may see
	// read receipts in a match's conversation. Same gates.
	internal.POST("/conversations/dating-match/read-receipts", h.DatingReadReceipts)
	// Open-match probe: graph-service asks whether a pair holds an open
	// dating match, so a matched pair may place a live call without becoming
	// a graph connection. Same gates, same prefix, same reasoning as above.
	internal.POST("/dating-match/state", h.DatingMatchState)
	internal.POST("/dating-match/turns", h.DatingTurns)
	// DM consent model: graph-service decides calls under
	// accepted_chats_only by asking whether the pair chat by consent.
	internal.POST("/direct-chat/state", h.DirectChatState)
	managedGroups := internal.Group("/groups")
	{
		managedGroups.POST("/conversations", h.CreateManagedGroupConversation)
		managedGroups.POST("/conversations/:id/members", h.AddManagedGroupMember)
		managedGroups.DELETE("/conversations/:id/members/:userId", h.RemoveManagedGroupMember)
	}
	v1 := r.Group("/v1/chat")
	// Hidden chats: every /conversations/:id route of a conversation the
	// caller hid needs X-Chat-Open (hidden_chats_handler.go). Installed
	// before any route so no route can be registered outside it.
	v1.Use(h.hiddenChatGate())
	{
		v1.GET("/health", h.Health)
		// Conversations
		v1.POST("/conversations/direct", h.CreateDirectConversation)
		v1.POST("/conversations/group", h.CreateGroupConversation)
		// Dating-match creation is NOT registered here: see the internal
		// group above. A /v1/chat path is reachable through the gateway.
		v1.GET("/conversations", h.ListConversations)
		v1.GET("/conversations/:id", h.GetConversation)
		v1.POST("/conversations/:id/members", h.AddMember)
		v1.DELETE("/conversations/:id/members/:userId", h.RemoveMember)
		v1.PUT("/conversations/:id", h.UpdateConversation)
		// Messages
		v1.POST("/conversations/:id/messages", limitBody(MaxMessageBodyBytes), h.SendMessage)
		v1.GET("/conversations/:id/messages", h.GetMessages)
		v1.PATCH("/conversations/:id/messages/:messageId", limitBody(MaxMessageBodyBytes), h.EditMessage)
		v1.DELETE("/conversations/:id/messages/:messageId", h.DeleteMessage)
		v1.PUT("/conversations/:id/messages/:messageId/reactions", h.ToggleReaction)
		// Typing & Read receipts
		v1.POST("/conversations/:id/typing", h.SetTyping)
		v1.POST("/conversations/:id/read", h.MarkRead)
		// Delivered ticks: the device acks what it received (receipts_handler.go).
		v1.POST("/conversations/:id/delivered", h.MarkDelivered)
		// Presence
		v1.POST("/presence", h.GetPresence)
		// Per-conversation presence (M1): who's currently viewing this
		// chat right now, plus typing users. ws-gateway writes via
		// conversation.enter/heartbeat/leave + typing.start; this is
		// the read side.
		v1.GET("/conversations/:id/presence", h.GetConversationPresence)

		// Conversation settings
		v1.GET("/conversations/:id/settings", h.GetConversationSettings)
		v1.PUT("/conversations/:id/settings", h.UpdateConversationSettings)

		// Folders
		v1.POST("/folders", h.CreateChatFolder)
		v1.GET("/folders", h.ListChatFolders)
		v1.DELETE("/folders/:folderId", h.DeleteChatFolder)
		v1.POST("/folders/:folderId/conversations", h.AddConversationToFolder)
		v1.DELETE("/folders/:folderId/conversations/:conversationId", h.RemoveConversationFromFolder)

		// Pins
		v1.POST("/conversations/:id/pins", h.PinMessage)
		v1.DELETE("/conversations/:id/pins/:pinId", h.UnpinMessage)
		v1.GET("/conversations/:id/pins", h.GetConversationPins)

		// Message requests
		v1.GET("/requests", h.ListMessageRequests)
		v1.GET("/requests/count", h.CountMessageRequests)
		v1.POST("/conversations/:id/requests/accept", h.AcceptMessageRequest)
		v1.POST("/conversations/:id/requests/decline", h.DeclineMessageRequest)
		v1.POST("/conversations/:id/requests/block", h.BlockMessageRequest)
		v1.POST("/conversations/:id/requests/report", h.ReportMessageRequest)

		// Group governance (production chat pass)
		v1.POST("/conversations/:id/leave", h.LeaveConversation)
		v1.POST("/conversations/:id/transfer-owner", h.TransferOwnership)
		v1.PUT("/conversations/:id/members/:userId/role", h.SetMemberRole)
		v1.GET("/invitations", h.ListGroupInvitations)
		v1.POST("/invitations/:invitationId/accept", h.AcceptGroupInvitation)
		v1.POST("/invitations/:invitationId/decline", h.DeclineGroupInvitation)

		// Group invite links (chat-app pass 2026-09-05)
		v1.POST("/conversations/:id/invite-link", h.CreateInviteLink)
		v1.GET("/conversations/:id/invite-link", h.GetInviteLink)
		v1.DELETE("/conversations/:id/invite-link", h.RevokeInviteLink)
		v1.GET("/invites/:code", h.PreviewInvite)
		v1.POST("/invites/:code/join", h.JoinByInvite)

		// Realtime room entitlement (scoped-rooms foundation)
		v1.POST("/conversations/:id/subscription", h.IssueSubscription)

		// Starred messages
		v1.POST("/conversations/:id/messages/:messageId/star", h.StarMessage)
		v1.DELETE("/messages/:messageId/star", h.UnstarMessage)
		v1.GET("/starred", h.GetStarredMessages)

		// Backups
		v1.POST("/backup", h.CreateChatBackup)
		v1.GET("/backup/latest", h.GetLatestChatBackup)

		// Scheduled messages
		v1.POST("/conversations/:id/scheduled", limitBody(MaxMessageBodyBytes), h.CreateScheduledMessage)
		v1.DELETE("/scheduled/:msgId", h.CancelScheduledMessage)
		v1.GET("/conversations/:id/scheduled", h.ListScheduledMessages)

		// Translation
		v1.GET("/messages/:messageId/translation", h.GetMessageTranslation)

		// Threads
		v1.GET("/conversations/:id/threads/:parentMessageId", h.GetOrCreateThread)
		v1.GET("/conversations/:id/threads", h.ListConversationThreads)

		// Chat parity programme (CONTRACT §1-3, §5-7, 2026-10-18).
		h.registerParityRoutes(v1, internal)

		// Hidden chats (contract pinned 24 Oct 2026).
		h.registerHiddenChatRoutes(v1)
	}
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
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

// writeMessagingError maps DM-gating / message-request errors (spec §4, §9.5)
// to their HTTP statuses. Returns true when the error was handled.
func writeMessagingError(c *gin.Context, err error) bool {
	// DM consent model: deny reasons and the fail-closed request limiter
	// (dm_consent_handler.go).
	if writeConsentError(c, err) {
		return true
	}
	switch {
	case errors.Is(err, service.ErrMessagingNotAllowed):
		api.Error(c.Writer, http.StatusForbidden, "MESSAGING_NOT_ALLOWED", err.Error(), nil, nil)
		return true
	case errors.Is(err, service.ErrRequestFirstMessageInvalid):
		api.Error(c.Writer, http.StatusUnprocessableEntity, "INVALID_REQUEST_MESSAGE", err.Error(), nil, nil)
		return true
	case errors.Is(err, service.ErrAwaitingRequestAcceptance):
		api.Error(c.Writer, http.StatusConflict, "AWAITING_REQUEST_ACCEPTANCE", err.Error(), nil, nil)
		return true
	case errors.Is(err, service.ErrFirstMovePending):
		api.Error(c.Writer, http.StatusForbidden, "FIRST_MOVE_PENDING", err.Error(), nil, nil)
		return true
	case errors.Is(err, service.ErrRateLimited):
		api.Error(c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", err.Error(), nil, nil)
		return true
	case errors.Is(err, service.ErrInvalidMessageContent):
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return true
	default:
		return false
	}
}
