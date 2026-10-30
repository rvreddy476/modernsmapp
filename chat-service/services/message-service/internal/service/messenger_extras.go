package service

import (
	"context"
	"errors"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	sharedEvents "github.com/atpost/chat-shared/events"
	"github.com/google/uuid"
)

// MessengerExtrasStore extends ConversationStore with the new messenger methods.
type MessengerExtrasStore interface {
	// Conversation settings
	UpsertConversationSettings(ctx context.Context, settings *postgres.ConversationSettings) error
	GetConversationSettings(ctx context.Context, convID, userID uuid.UUID) (*postgres.ConversationSettings, error)
	GetSettingsForConversations(ctx context.Context, userID uuid.UUID, convIDs []uuid.UUID) (map[uuid.UUID]postgres.ConversationSettings, error)
	ListConversationsByLabel(ctx context.Context, userID uuid.UUID, label string, limit, offset int) ([]postgres.ConversationSettings, error)
	// Folders
	CreateChatFolder(ctx context.Context, f *postgres.ChatFolder) (*postgres.ChatFolder, error)
	GetChatFolder(ctx context.Context, id uuid.UUID) (*postgres.ChatFolder, error)
	ListChatFolders(ctx context.Context, userID uuid.UUID) ([]postgres.ChatFolder, error)
	DeleteChatFolder(ctx context.Context, id uuid.UUID) error
	AddConversationToFolder(ctx context.Context, folderID, conversationID uuid.UUID) error
	RemoveConversationFromFolder(ctx context.Context, folderID, conversationID uuid.UUID) error
	GetFolderConversations(ctx context.Context, folderID uuid.UUID) ([]uuid.UUID, error)
	// Pins
	PinMessage(ctx context.Context, convID, messageID, pinnedBy uuid.UUID) (*postgres.ConversationPin, error)
	UnpinMessage(ctx context.Context, convID, pinID uuid.UUID) (*postgres.ConversationPin, error)
	GetConversationPins(ctx context.Context, convID uuid.UUID) ([]postgres.ConversationPin, error)
	// Message requests
	GetMessageRequestSettings(ctx context.Context, userID uuid.UUID) (*postgres.MessageRequestSettings, error)
	UpsertMessageRequestSettings(ctx context.Context, s *postgres.MessageRequestSettings) error
	AcceptMessageRequest(ctx context.Context, convID uuid.UUID) error
	DeclineMessageRequest(ctx context.Context, convID uuid.UUID) error
	ListMessageRequests(ctx context.Context, userID uuid.UUID, limit, offset int) ([]postgres.Conversation, error)
	// Starred messages
	StarMessage(ctx context.Context, userID, convID, messageID uuid.UUID, preview *string) (*postgres.StarredMessage, error)
	UnstarMessage(ctx context.Context, userID, messageID uuid.UUID) error
	GetStarredMessages(ctx context.Context, userID uuid.UUID, limit, offset int) ([]postgres.StarredMessage, error)
	// Backups
	CreateChatBackup(ctx context.Context, b *postgres.ChatBackup) (*postgres.ChatBackup, error)
	UpdateChatBackup(ctx context.Context, id uuid.UUID, status string, sizeBytes, messageCount *int64, blobURL *string) error
	GetLatestChatBackup(ctx context.Context, userID uuid.UUID) (*postgres.ChatBackup, error)
	// Scheduled messages
	CreateScheduledMessage(ctx context.Context, m *postgres.ScheduledMessage) (*postgres.ScheduledMessage, error)
	CancelScheduledMessage(ctx context.Context, id, senderID uuid.UUID) error
	GetPendingScheduledMessages(ctx context.Context, before time.Time, limit int) ([]postgres.ScheduledMessage, error)
	MarkScheduledMessageSent(ctx context.Context, id uuid.UUID) error
	RecordScheduledMessageFailure(ctx context.Context, id uuid.UUID, failure string, maxAttempts int) error
	ListScheduledMessages(ctx context.Context, conversationID, senderID uuid.UUID) ([]postgres.ScheduledMessage, error)
	// Translations
	UpsertMessageTranslation(ctx context.Context, t *postgres.MessageTranslation) error
	GetMessageTranslation(ctx context.Context, messageID uuid.UUID, targetLang string) (*postgres.MessageTranslation, error)
	// Threads
	GetOrCreateThread(ctx context.Context, convID, parentMessageID uuid.UUID) (*postgres.MessageThread, error)
	IncrementThreadReplyCount(ctx context.Context, threadID uuid.UUID, lastReplyPreview string) error
	GetThread(ctx context.Context, convID, parentMessageID uuid.UUID) (*postgres.MessageThread, error)
	ListConversationThreads(ctx context.Context, convID uuid.UUID) ([]postgres.MessageThread, error)
}

// pgStore returns the underlying pgStore cast to MessengerExtrasStore.
// All methods below assume the convStore also implements MessengerExtrasStore.
func (s *Service) extrasStore() MessengerExtrasStore {
	es, ok := s.convStore.(MessengerExtrasStore)
	if !ok {
		panic("convStore does not implement MessengerExtrasStore")
	}
	return es
}

// --- Conversation Settings ---

func (s *Service) GetSettings(ctx context.Context, userID, convID uuid.UUID) (*postgres.ConversationSettings, error) {
	ok, err := s.convStore.CheckMembership(ctx, convID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotMember
	}
	cs, err := s.extrasStore().GetConversationSettings(ctx, convID, userID)
	if err != nil {
		return nil, err
	}
	// Disappearing messages: the timer is conversation-wide, so the
	// settings carry the conversation's value (0 = off), never the stale
	// per-member column.
	timer, err := s.GetDisappearingTimer(ctx, convID)
	if err != nil {
		return nil, err
	}
	ms := timer.DisappearAfterMs
	cs.DisappearAfterMs = &ms
	cs.DisappearUpdatedBy, cs.DisappearUpdatedAt = timer.UpdatedBy, timer.UpdatedAt
	return cs, nil
}

func (s *Service) UpdateSettings(ctx context.Context, settings *postgres.ConversationSettings) error {
	ok, err := s.convStore.CheckMembership(ctx, settings.ConversationID, settings.UserID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotMember
	}
	return s.extrasStore().UpsertConversationSettings(ctx, settings)
}

func (s *Service) ListByLabel(ctx context.Context, userID uuid.UUID, label string, limit, offset int) ([]postgres.ConversationSettings, error) {
	return s.extrasStore().ListConversationsByLabel(ctx, userID, label, limit, offset)
}

// --- Chat Folders ---

func (s *Service) CreateFolder(ctx context.Context, userID uuid.UUID, name, icon string, sortOrder int) (*postgres.ChatFolder, error) {
	if name == "" {
		return nil, invalidInput("folder name is required")
	}
	f := &postgres.ChatFolder{
		UserID:    userID,
		Name:      name,
		Icon:      icon,
		SortOrder: sortOrder,
	}
	return s.extrasStore().CreateChatFolder(ctx, f)
}

func (s *Service) GetFolder(ctx context.Context, userID, folderID uuid.UUID) (*postgres.ChatFolder, error) {
	f, err := s.extrasStore().GetChatFolder(ctx, folderID)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, notFound("folder not found")
	}
	if f.UserID != userID {
		return nil, notFound("folder not found")
	}
	return f, nil
}

func (s *Service) ListFolders(ctx context.Context, userID uuid.UUID) ([]postgres.ChatFolder, error) {
	return s.extrasStore().ListChatFolders(ctx, userID)
}

func (s *Service) DeleteFolder(ctx context.Context, userID, folderID uuid.UUID) error {
	f, err := s.extrasStore().GetChatFolder(ctx, folderID)
	if err != nil {
		return err
	}
	if f == nil || f.UserID != userID {
		return notFound("folder not found")
	}
	return s.extrasStore().DeleteChatFolder(ctx, folderID)
}

func (s *Service) AddToFolder(ctx context.Context, userID, folderID, conversationID uuid.UUID) error {
	f, err := s.extrasStore().GetChatFolder(ctx, folderID)
	if err != nil {
		return err
	}
	if f == nil || f.UserID != userID {
		return notFound("folder not found")
	}
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotMember
	}
	return s.extrasStore().AddConversationToFolder(ctx, folderID, conversationID)
}

func (s *Service) RemoveFromFolder(ctx context.Context, userID, folderID, conversationID uuid.UUID) error {
	f, err := s.extrasStore().GetChatFolder(ctx, folderID)
	if err != nil {
		return err
	}
	if f == nil || f.UserID != userID {
		return notFound("folder not found")
	}
	return s.extrasStore().RemoveConversationFromFolder(ctx, folderID, conversationID)
}

func (s *Service) GetFolderConversations(ctx context.Context, userID, folderID uuid.UUID) ([]uuid.UUID, error) {
	f, err := s.extrasStore().GetChatFolder(ctx, folderID)
	if err != nil {
		return nil, err
	}
	if f == nil || f.UserID != userID {
		return nil, notFound("folder not found")
	}
	return s.extrasStore().GetFolderConversations(ctx, folderID)
}

// --- Pins ---

// PinMessageSvc pins a message the caller can see (L5). The caller must be
// allowed to change pins (requirePinRights) and the message must belong to
// convID and be visible to them (visibleMessage: live, not expired, inside
// their history). The 3-pin cap is enforced under a row lock in the store.
func (s *Service) PinMessageSvc(ctx context.Context, userID, convID, messageID uuid.UUID) (*postgres.ConversationPin, error) {
	if err := s.requirePinRights(ctx, userID, convID); err != nil {
		return nil, err
	}
	if _, err := s.visibleMessage(ctx, userID, convID, messageID, nil); err != nil {
		return nil, err
	}
	pin, err := s.extrasStore().PinMessage(ctx, convID, messageID, userID)
	if err != nil {
		return nil, err
	}
	s.publishPinUpdate(ctx, convID, "pinned", pin, userID)
	return pin, nil
}

// requirePinRights is who may change a conversation's pins: an active
// member, never while the conversation is still a message request (the
// requester could otherwise pin over the recipient's screen before they
// accept), and in a group only the owner or an admin — the same rung of the
// role ladder that edits the group's info.
func (s *Service) requirePinRights(ctx context.Context, userID, convID uuid.UUID) error {
	ok, err := s.convStore.CheckMembership(ctx, convID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotMember
	}
	conv, err := s.convStore.GetConversation(ctx, convID)
	if err != nil {
		return err
	}
	if conv == nil {
		return ErrNotMember
	}
	if conv.IsRequest {
		return ErrAwaitingRequestAcceptance
	}
	if conv.Type == "direct" {
		return nil
	}
	role, err := s.convStore.GetMemberRole(ctx, convID, userID)
	if err != nil {
		return err
	}
	if role != "owner" && role != "admin" {
		return ErrNotPermittedRole
	}
	return nil
}

// ErrPinNotFound is a pin id that does not exist in the conversation named
// by the route (including a real pin of ANOTHER conversation).
var ErrPinNotFound = errors.New("pin not found")

// UnpinMessageSvc removes a pin. The caller must be an active member of
// convID and the pin must belong to convID; otherwise ErrNotMember /
// ErrPinNotFound (both 404), and nothing is deleted. Pin rights
// (requirePinRights) apply as on pin. Before this check any
// signed-in user holding a pin id could unpin in any chat.
func (s *Service) UnpinMessageSvc(ctx context.Context, userID, convID, pinID uuid.UUID) error {
	if err := s.requirePinRights(ctx, userID, convID); err != nil {
		return err
	}
	pin, err := s.extrasStore().UnpinMessage(ctx, convID, pinID)
	if err != nil {
		return err
	}
	if pin == nil {
		return ErrPinNotFound
	}
	s.publishPinUpdate(ctx, convID, "unpinned", pin, userID)
	return nil
}

// publishPinUpdate tells every member (and the room) about a pin change:
// {"type":"pin_update","payload":{conversation_id, action: "pinned"|
// "unpinned", pin_id, message_id, by, at}}. Best-effort, like other frames.
func (s *Service) publishPinUpdate(ctx context.Context, convID uuid.UUID, action string, pin *postgres.ConversationPin, by uuid.UUID) {
	if pin == nil {
		return
	}
	members, err := s.convStore.GetMembers(ctx, convID)
	if err != nil {
		return
	}
	s.publishToConversation(convID, members, "pin_update", map[string]interface{}{
		"conversation_id": convID,
		"action":          action,
		"pin_id":          pin.ID,
		"message_id":      pin.MessageID,
		"by":              by,
		"at":              s.clock().UTC(),
	})
}

func (s *Service) GetPins(ctx context.Context, userID, convID uuid.UUID) ([]postgres.ConversationPin, error) {
	ok, err := s.convStore.CheckMembership(ctx, convID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotMember
	}
	return s.extrasStore().GetConversationPins(ctx, convID)
}

// --- Message Requests ---

func (s *Service) GetRequestSettings(ctx context.Context, userID uuid.UUID) (*postgres.MessageRequestSettings, error) {
	return s.extrasStore().GetMessageRequestSettings(ctx, userID)
}

func (s *Service) UpdateRequestSettings(ctx context.Context, userID uuid.UUID, allowFrom string, autoFilter bool) error {
	ms := &postgres.MessageRequestSettings{
		UserID:               userID,
		AllowFrom:            allowFrom,
		AutoFilterLikelySpam: autoFilter,
	}
	return s.extrasStore().UpsertMessageRequestSettings(ctx, ms)
}

// AcceptRequest accepts a pending message request. Only the recipient may
// accept; on acceptance the conversation leaves the Requests folder and the
// request envelope is marked accepted (spec §3.3, §9.5).
func (s *Service) AcceptRequest(ctx context.Context, userID, convID uuid.UUID) error {
	ok, err := s.convStore.CheckMembership(ctx, convID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotMember
	}

	mr, err := s.convStore.GetMessageRequestByConversation(ctx, convID)
	if err != nil {
		return err
	}
	if mr != nil && mr.ReceiverID != userID {
		return forbidden("only the recipient can accept this request")
	}

	if err := s.extrasStore().AcceptMessageRequest(ctx, convID); err != nil {
		return err
	}
	if mr != nil {
		_ = s.convStore.UpdateMessageRequestStatus(ctx, convID, "accepted")
		// DM consent model (20 Oct 2026): accepting opens the chat and
		// nothing more — it no longer makes the pair graph connections.
		// Calls under accepted_chats_only ask chat for this accepted state
		// (POST /internal/v1/chat/direct-chat/state).
		_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.MessageRequestAccepted, sharedEvents.MessageRequestPayload{
			ConversationID: convID.String(),
			SenderID:       mr.SenderID.String(),
			ReceiverID:     mr.ReceiverID.String(),
			OccurredAt:     time.Now(),
		})
	}
	return nil
}

// DeclineRequest ignores a pending message request (the recipient's action).
func (s *Service) DeclineRequest(ctx context.Context, userID, convID uuid.UUID) error {
	ok, err := s.convStore.CheckMembership(ctx, convID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotMember
	}

	mr, err := s.convStore.GetMessageRequestByConversation(ctx, convID)
	if err != nil {
		return err
	}
	if mr != nil && mr.ReceiverID != userID {
		return forbidden("only the recipient can decline this request")
	}

	if err := s.extrasStore().DeclineMessageRequest(ctx, convID); err != nil {
		return err
	}
	if mr != nil {
		_ = s.convStore.UpdateMessageRequestStatus(ctx, convID, "ignored")
		_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.MessageRequestIgnored, sharedEvents.MessageRequestPayload{
			ConversationID: convID.String(),
			SenderID:       mr.SenderID.String(),
			ReceiverID:     mr.ReceiverID.String(),
			OccurredAt:     time.Now(),
		})
	}
	return nil
}

func (s *Service) ListRequests(ctx context.Context, userID uuid.UUID, limit, offset int) ([]postgres.Conversation, error) {
	return s.extrasStore().ListMessageRequests(ctx, userID, limit, offset)
}

// BlockRequest is the recipient's strongest refusal (directive §3.3): the
// request is marked blocked, BOTH memberships are severed so neither side can
// read or send, and the event stream carries the block so the graph-side
// symmetric block (which the client also files through the social block API)
// can reconcile. Idempotent — re-blocking an already-blocked request succeeds.
func (s *Service) BlockRequest(ctx context.Context, userID, convID uuid.UUID) error {
	mr, err := s.convStore.GetMessageRequestByConversation(ctx, convID)
	if err != nil {
		return err
	}
	if mr == nil {
		return notFound("no message request for this conversation")
	}
	if mr.ReceiverID != userID {
		return forbidden("only the recipient can block this request")
	}
	if mr.Status == "blocked" {
		return nil // idempotent — status flips only after sever+revocation succeeded
	}
	// Sever both memberships THROUGH the revocation protocol, BEFORE the
	// status flip (final-verification P0-4): the legacy order swallowed
	// sever failures and the idempotent early-return above then acknowledged
	// a block whose memberships still carried live room subscriptions and
	// tokens. Errors propagate; the client retries the idempotent block.
	if err := s.severMemberWithRevocation(ctx, convID, mr.SenderID); err != nil {
		return err
	}
	if err := s.severMemberWithRevocation(ctx, convID, mr.ReceiverID); err != nil {
		return err
	}
	if err := s.convStore.UpdateMessageRequestStatus(ctx, convID, "blocked"); err != nil {
		return err
	}
	_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.MessageRequestBlocked, sharedEvents.MessageRequestPayload{
		ConversationID: convID.String(),
		SenderID:       mr.SenderID.String(),
		ReceiverID:     mr.ReceiverID.String(),
		OccurredAt:     time.Now(),
	})
	return nil
}

// ReportRequest marks the request reported (and severed like a block) and
// emits the moderation event. The report itself carries only ids — the
// preview is already stored server-side for the moderation pipeline.
func (s *Service) ReportRequest(ctx context.Context, userID, convID uuid.UUID) error {
	mr, err := s.convStore.GetMessageRequestByConversation(ctx, convID)
	if err != nil {
		return err
	}
	if mr == nil {
		return notFound("no message request for this conversation")
	}
	if mr.ReceiverID != userID {
		return forbidden("only the recipient can report this request")
	}
	if mr.Status == "reported" {
		return nil // idempotent — status flips only after sever+revocation succeeded
	}
	// Same order and rationale as BlockRequest: sever through the revocation
	// protocol first, propagate failures, flip the status only afterwards.
	if err := s.severMemberWithRevocation(ctx, convID, mr.SenderID); err != nil {
		return err
	}
	if err := s.severMemberWithRevocation(ctx, convID, mr.ReceiverID); err != nil {
		return err
	}
	if err := s.convStore.UpdateMessageRequestStatus(ctx, convID, "reported"); err != nil {
		return err
	}
	_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.MessageRequestReported, sharedEvents.MessageRequestPayload{
		ConversationID: convID.String(),
		SenderID:       mr.SenderID.String(),
		ReceiverID:     mr.ReceiverID.String(),
		Preview:        mr.Preview,
		OccurredAt:     time.Now(),
	})
	return nil
}

// --- Starred Messages ---

func (s *Service) StarMessageSvc(ctx context.Context, userID, convID, messageID uuid.UUID, preview *string) (*postgres.StarredMessage, error) {
	ok, err := s.convStore.CheckMembership(ctx, convID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotMember
	}
	return s.extrasStore().StarMessage(ctx, userID, convID, messageID, preview)
}

func (s *Service) UnstarMessageSvc(ctx context.Context, userID, messageID uuid.UUID) error {
	return s.extrasStore().UnstarMessage(ctx, userID, messageID)
}

func (s *Service) GetStarredMessages(ctx context.Context, userID uuid.UUID, limit, offset int) ([]postgres.StarredMessage, error) {
	return s.extrasStore().GetStarredMessages(ctx, userID, limit, offset)
}

// --- Backups ---

func (s *Service) CreateBackup(ctx context.Context, userID uuid.UUID, keyHint *string) (*postgres.ChatBackup, error) {
	b := &postgres.ChatBackup{
		UserID:  userID,
		KeyHint: keyHint,
	}
	return s.extrasStore().CreateChatBackup(ctx, b)
}

func (s *Service) UpdateBackup(ctx context.Context, id uuid.UUID, status string, sizeBytes, messageCount *int64, blobURL *string) error {
	return s.extrasStore().UpdateChatBackup(ctx, id, status, sizeBytes, messageCount, blobURL)
}

func (s *Service) GetLatestBackup(ctx context.Context, userID uuid.UUID) (*postgres.ChatBackup, error) {
	return s.extrasStore().GetLatestChatBackup(ctx, userID)
}

// --- Scheduled Messages ---

func (s *Service) CreateScheduledMessage(ctx context.Context, convID, senderID uuid.UUID, msgType, content string, mediaID *uuid.UUID, sendAt time.Time) (*postgres.ScheduledMessage, error) {
	// H4: the send rules (2000-rune cap, known type) apply when the message
	// is scheduled, not only when the worker sends it hours later — an
	// oversized row would otherwise sit in the queue and fail every attempt.
	if err := validateMessageContent(msgType, content, mediaID, nil); err != nil {
		return nil, err
	}
	ok, err := s.convStore.CheckMembership(ctx, convID, senderID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotMember
	}
	var contentPtr *string
	if content != "" {
		contentPtr = &content
	}
	m := &postgres.ScheduledMessage{
		ConversationID: convID,
		SenderID:       senderID,
		Type:           msgType,
		Content:        contentPtr,
		MediaID:        mediaID,
		SendAt:         sendAt,
	}
	created, err := s.extrasStore().CreateScheduledMessage(ctx, m)
	if errors.Is(err, postgres.ErrScheduledMessageLimit) {
		// M2: per-user cap on pending scheduled messages (400).
		return nil, invalidInput(err.Error())
	}
	return created, err
}

func (s *Service) CancelScheduledMessageSvc(ctx context.Context, userID, msgID uuid.UUID) error {
	return s.extrasStore().CancelScheduledMessage(ctx, msgID, userID)
}

func (s *Service) ListScheduledMessagesSvc(ctx context.Context, userID, convID uuid.UUID) ([]postgres.ScheduledMessage, error) {
	ok, err := s.convStore.CheckMembership(ctx, convID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotMember
	}
	return s.extrasStore().ListScheduledMessages(ctx, convID, userID)
}

// --- Translations ---

func (s *Service) GetTranslation(ctx context.Context, userID, messageID uuid.UUID, targetLang string) (*postgres.MessageTranslation, error) {
	translation, err := s.extrasStore().GetMessageTranslation(ctx, messageID, targetLang)
	if err != nil || translation == nil {
		return translation, err
	}
	member, err := s.convStore.CheckMembership(ctx, translation.ConversationID, userID)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, nil
	}
	return translation, nil
}

func (s *Service) CreateTranslation(ctx context.Context, t *postgres.MessageTranslation) error {
	return s.extrasStore().UpsertMessageTranslation(ctx, t)
}

// --- Threads ---

func (s *Service) GetOrCreateThreadSvc(ctx context.Context, userID, convID, parentMessageID uuid.UUID) (*postgres.MessageThread, error) {
	member, err := s.convStore.CheckMembership(ctx, convID, userID)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, nil
	}
	return s.extrasStore().GetOrCreateThread(ctx, convID, parentMessageID)
}

func (s *Service) GetThreadSvc(ctx context.Context, userID, convID, parentMessageID uuid.UUID) (*postgres.MessageThread, error) {
	member, err := s.convStore.CheckMembership(ctx, convID, userID)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, nil
	}
	return s.extrasStore().GetThread(ctx, convID, parentMessageID)
}

func (s *Service) ListThreadsSvc(ctx context.Context, userID, convID uuid.UUID) ([]postgres.MessageThread, error) {
	member, err := s.convStore.CheckMembership(ctx, convID, userID)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, nil
	}
	return s.extrasStore().ListConversationThreads(ctx, convID)
}
