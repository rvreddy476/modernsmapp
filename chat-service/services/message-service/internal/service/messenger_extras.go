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
	UnpinMessage(ctx context.Context, pinID uuid.UUID) error
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
		return nil, errors.New("not a conversation member")
	}
	return s.extrasStore().GetConversationSettings(ctx, convID, userID)
}

func (s *Service) UpdateSettings(ctx context.Context, settings *postgres.ConversationSettings) error {
	ok, err := s.convStore.CheckMembership(ctx, settings.ConversationID, settings.UserID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("not a conversation member")
	}
	return s.extrasStore().UpsertConversationSettings(ctx, settings)
}

func (s *Service) ListByLabel(ctx context.Context, userID uuid.UUID, label string, limit, offset int) ([]postgres.ConversationSettings, error) {
	return s.extrasStore().ListConversationsByLabel(ctx, userID, label, limit, offset)
}

// --- Chat Folders ---

func (s *Service) CreateFolder(ctx context.Context, userID uuid.UUID, name, icon string, sortOrder int) (*postgres.ChatFolder, error) {
	if name == "" {
		return nil, errors.New("folder name is required")
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
		return nil, errors.New("folder not found")
	}
	if f.UserID != userID {
		return nil, errors.New("folder not found")
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
		return errors.New("folder not found")
	}
	return s.extrasStore().DeleteChatFolder(ctx, folderID)
}

func (s *Service) AddToFolder(ctx context.Context, userID, folderID, conversationID uuid.UUID) error {
	f, err := s.extrasStore().GetChatFolder(ctx, folderID)
	if err != nil {
		return err
	}
	if f == nil || f.UserID != userID {
		return errors.New("folder not found")
	}
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("not a conversation member")
	}
	return s.extrasStore().AddConversationToFolder(ctx, folderID, conversationID)
}

func (s *Service) RemoveFromFolder(ctx context.Context, userID, folderID, conversationID uuid.UUID) error {
	f, err := s.extrasStore().GetChatFolder(ctx, folderID)
	if err != nil {
		return err
	}
	if f == nil || f.UserID != userID {
		return errors.New("folder not found")
	}
	return s.extrasStore().RemoveConversationFromFolder(ctx, folderID, conversationID)
}

func (s *Service) GetFolderConversations(ctx context.Context, userID, folderID uuid.UUID) ([]uuid.UUID, error) {
	f, err := s.extrasStore().GetChatFolder(ctx, folderID)
	if err != nil {
		return nil, err
	}
	if f == nil || f.UserID != userID {
		return nil, errors.New("folder not found")
	}
	return s.extrasStore().GetFolderConversations(ctx, folderID)
}

// --- Pins ---

func (s *Service) PinMessageSvc(ctx context.Context, userID, convID, messageID uuid.UUID) (*postgres.ConversationPin, error) {
	ok, err := s.convStore.CheckMembership(ctx, convID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("not a conversation member")
	}
	return s.extrasStore().PinMessage(ctx, convID, messageID, userID)
}

func (s *Service) UnpinMessageSvc(ctx context.Context, userID, pinID uuid.UUID) error {
	return s.extrasStore().UnpinMessage(ctx, pinID)
}

func (s *Service) GetPins(ctx context.Context, userID, convID uuid.UUID) ([]postgres.ConversationPin, error) {
	ok, err := s.convStore.CheckMembership(ctx, convID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("not a conversation member")
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
		return errors.New("not a conversation member")
	}

	mr, err := s.convStore.GetMessageRequestByConversation(ctx, convID)
	if err != nil {
		return err
	}
	if mr != nil && mr.ReceiverID != userID {
		return errors.New("only the recipient can accept this request")
	}

	if err := s.extrasStore().AcceptMessageRequest(ctx, convID); err != nil {
		return err
	}
	if mr != nil {
		_ = s.convStore.UpdateMessageRequestStatus(ctx, convID, "accepted")
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
		return errors.New("not a conversation member")
	}

	mr, err := s.convStore.GetMessageRequestByConversation(ctx, convID)
	if err != nil {
		return err
	}
	if mr != nil && mr.ReceiverID != userID {
		return errors.New("only the recipient can decline this request")
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

// --- Starred Messages ---

func (s *Service) StarMessageSvc(ctx context.Context, userID, convID, messageID uuid.UUID, preview *string) (*postgres.StarredMessage, error) {
	ok, err := s.convStore.CheckMembership(ctx, convID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("not a conversation member")
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
	ok, err := s.convStore.CheckMembership(ctx, convID, senderID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("not a conversation member")
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
	return s.extrasStore().CreateScheduledMessage(ctx, m)
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
		return nil, errors.New("not a conversation member")
	}
	return s.extrasStore().ListScheduledMessages(ctx, convID, userID)
}

// --- Translations ---

func (s *Service) GetTranslation(ctx context.Context, messageID uuid.UUID, targetLang string) (*postgres.MessageTranslation, error) {
	return s.extrasStore().GetMessageTranslation(ctx, messageID, targetLang)
}

func (s *Service) CreateTranslation(ctx context.Context, t *postgres.MessageTranslation) error {
	return s.extrasStore().UpsertMessageTranslation(ctx, t)
}

// --- Threads ---

func (s *Service) GetOrCreateThreadSvc(ctx context.Context, convID, parentMessageID uuid.UUID) (*postgres.MessageThread, error) {
	return s.extrasStore().GetOrCreateThread(ctx, convID, parentMessageID)
}

func (s *Service) GetThreadSvc(ctx context.Context, convID, parentMessageID uuid.UUID) (*postgres.MessageThread, error) {
	return s.extrasStore().GetThread(ctx, convID, parentMessageID)
}

func (s *Service) ListThreadsSvc(ctx context.Context, convID uuid.UUID) ([]postgres.MessageThread, error) {
	return s.extrasStore().ListConversationThreads(ctx, convID)
}
