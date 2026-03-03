package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/message-service/internal/kafka"
	"github.com/atpost/message-service/internal/store/postgres"
	"github.com/atpost/message-service/internal/store/scylla"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	scyllaStore *scylla.MessageStore
	convStore   *postgres.ConversationStore
	rdb         *redis.Client
	kafka       *kafka.Producer
}

func New(scyllaS *scylla.MessageStore, conv *postgres.ConversationStore, rdb *redis.Client, kp *kafka.Producer) *Service {
	return &Service{
		scyllaStore: scyllaS,
		convStore:   conv,
		rdb:         rdb,
		kafka:       kp,
	}
}

// SendMessageInput is the input for sending a message.
type SendMessageInput struct {
	Text            string `json:"text"`
	MessageType     string `json:"message_type"`
	MediaID         string `json:"media_id,omitempty"`
	ReplyToID       string `json:"reply_to_id,omitempty"`
	ForwardedFromID string `json:"forwarded_from_id,omitempty"`
}

type Conversation struct {
	ID                 uuid.UUID   `json:"id"`
	Type               string      `json:"type"`
	Name               *string     `json:"name,omitempty"`
	IconURL            *string     `json:"icon_url,omitempty"`
	Members            []uuid.UUID `json:"members"`
	LastMessageAt      *time.Time  `json:"last_message_at,omitempty"`
	LastMessagePreview *string     `json:"last_message_preview,omitempty"`
	CreatedAt          time.Time   `json:"created_at"`
	UpdatedAt          time.Time   `json:"updated_at"`
}

type ConversationCursor struct {
	UpdatedAt time.Time
	ID        uuid.UUID
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// resolveChatID resolves the ScyllaDB chat_id for a given conversation and validates membership.
func (s *Service) resolveChatID(ctx context.Context, userID, conversationID uuid.UUID) (string, *postgres.Conversation, error) {
	conv, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil {
		return "", nil, err
	}
	if conv == nil {
		return "", nil, errors.New("conversation not found")
	}
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return "", nil, err
	}
	if !ok {
		return "", nil, errors.New("not a conversation member")
	}

	if conv.Type == "group" {
		return conversationID.String(), conv, nil
	}

	members, err := s.convStore.GetMembers(ctx, conversationID)
	if err != nil {
		return "", nil, err
	}
	var other uuid.UUID
	for _, m := range members {
		if m != userID {
			other = m
			break
		}
	}
	return scylla.GenerateChatID(userID.String(), other.String()), conv, nil
}

// broadcastToConversation publishes a JSON payload to Redis PubSub for all conversation members.
func (s *Service) broadcastToConversation(ctx context.Context, conversationID uuid.UUID, excludeUserID *uuid.UUID, payload map[string]interface{}) {
	members, err := s.convStore.GetMembers(ctx, conversationID)
	if err != nil {
		slog.Error("broadcastToConversation: failed to get members", "error", err, "conversation_id", conversationID)
		return
	}
	data, _ := json.Marshal(payload)
	for _, m := range members {
		if excludeUserID != nil && m == *excludeUserID {
			continue
		}
		ch := fmt.Sprintf("chat:%s", m.String())
		s.rdb.Publish(ctx, ch, data)
	}
}

// messagePreview computes a short preview for the conversation list.
func messagePreview(input SendMessageInput) string {
	switch input.MessageType {
	case "image":
		return "[Image]"
	case "video":
		return "[Video]"
	case "audio":
		return "[Audio]"
	case "file":
		return "[File]"
	default:
		text := input.Text
		if len(text) > 100 {
			text = text[:100]
		}
		return text
	}
}

// ---------------------------------------------------------------------------
// Send Messages
// ---------------------------------------------------------------------------

// SendMessage implements the ultra-scale chat flow:
// 1. Deliver to recipient WebSocket immediately (Redis PubSub)
// 2. Non-blocking writes to ScyllaDB, Redis Cache, and Kafka
func (s *Service) SendMessage(ctx context.Context, senderID, receiverID uuid.UUID, input SendMessageInput) (*scylla.Message, error) {
	if input.MessageType == "" {
		input.MessageType = "text"
	}

	// Ensure conversation exists + update timestamp for list ordering.
	var convID uuid.UUID
	if s.convStore != nil {
		cid, err := s.convStore.CreateDirectConversation(ctx, senderID, receiverID)
		if err == nil {
			convID = cid
			_ = s.convStore.TouchConversationWithPreview(ctx, cid, time.Now(), messagePreview(input))
		}
	}

	chatID := scylla.GenerateChatID(senderID.String(), receiverID.String())
	msgID := uuid.New().String()
	timestamp := time.Now()

	msg := &scylla.Message{
		ChatID:          chatID,
		Timestamp:       timestamp,
		MsgID:           msgID,
		SenderID:        senderID.String(),
		ReceiverID:      receiverID.String(),
		Text:            input.Text,
		MessageType:     input.MessageType,
		MediaID:         input.MediaID,
		ReplyToID:       input.ReplyToID,
		ForwardedFromID: input.ForwardedFromID,
	}

	l := slog.With("chat_id", chatID, "sender_id", senderID, "receiver_id", receiverID, "msg_id", msgID)

	// PERSISTENCE (Primary Path)
	if err := s.scyllaStore.CreateMessage(ctx, msg); err != nil {
		l.Error("failed to save to scylladb", "error", err)
		return nil, err
	}

	// REAL-TIME DELIVERY (Primary Path)
	go func() {
		pubCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		channel := fmt.Sprintf("chat:%s", receiverID.String())
		payload, _ := json.Marshal(map[string]interface{}{
			"type":              "new_message",
			"payload":          msg,
			"conversation_id":  convID.String(),
		})
		if err := s.rdb.Publish(pubCtx, channel, payload).Err(); err != nil {
			l.Error("failed to publish to redis pubsub", "error", err)
		}
	}()

	// Secondary: Redis List cache
	go func() {
		cacheCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		key := fmt.Sprintf("chat_history:%s", chatID)
		payload, _ := json.Marshal(msg)
		pipe := s.rdb.Pipeline()
		pipe.LPush(cacheCtx, key, payload)
		pipe.LTrim(cacheCtx, key, 0, 99)
		if _, err := pipe.Exec(cacheCtx); err != nil {
			l.Error("failed to update redis cache", "error", err)
		}
	}()

	// Secondary: Kafka
	go func() {
		kafkaCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.kafka.PublishMessage(kafkaCtx, msg); err != nil {
			l.Error("failed to publish to kafka", "error", err)
		}
	}()

	return msg, nil
}

// GetMessages implements hot-path caching
func (s *Service) GetMessages(ctx context.Context, senderID, receiverID uuid.UUID, limit int) ([]scylla.Message, error) {
	chatID := scylla.GenerateChatID(senderID.String(), receiverID.String())
	key := fmt.Sprintf("chat_history:%s", chatID)
	l := slog.With("chat_id", chatID, "sender_id", senderID, "receiver_id", receiverID)

	// 1. Try Redis Hot-Path
	cached, err := s.rdb.LRange(ctx, key, 0, int64(limit-1)).Result()
	if err == nil && len(cached) > 0 {
		var messages []scylla.Message
		for _, raw := range cached {
			var m scylla.Message
			if err := json.Unmarshal([]byte(raw), &m); err == nil {
				messages = append(messages, m)
			}
		}
		return messages, nil
	}
	if err != nil && err != redis.Nil {
		l.Warn("redis cache lookup failed", "error", err)
	}

	// 2. Hydrate from ScyllaDB
	messages, err := s.scyllaStore.GetMessages(ctx, chatID, limit)
	if err != nil {
		l.Error("scylladb lookup failed", "error", err)
		return nil, fmt.Errorf("failed to retrieve messages from permanent storage: %w", err)
	}

	// 3. Populate Redis Cache (Async)
	if len(messages) > 0 {
		go func() {
			cacheCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			s.rdb.Del(cacheCtx, key)
			for i := len(messages) - 1; i >= 0; i-- {
				payload, _ := json.Marshal(messages[i])
				s.rdb.LPush(cacheCtx, key, payload)
			}
			s.rdb.LTrim(cacheCtx, key, 0, 99)
		}()
	}

	return messages, nil
}

func (s *Service) CreateDirectConversation(ctx context.Context, userID, otherID uuid.UUID) (*Conversation, error) {
	if userID == otherID {
		return nil, errors.New("cannot create conversation with self")
	}
	if s.convStore == nil {
		return nil, errors.New("conversation store not configured")
	}
	convID, err := s.convStore.CreateDirectConversation(ctx, userID, otherID)
	if err != nil {
		return nil, err
	}
	conv, err := s.convStore.GetConversation(ctx, convID)
	if err != nil {
		return nil, err
	}
	if conv == nil {
		return nil, errors.New("conversation not found")
	}
	members, err := s.convStore.GetMembers(ctx, convID)
	if err != nil {
		return nil, err
	}
	return &Conversation{
		ID:        conv.ID,
		Type:      conv.Type,
		Name:      conv.Name,
		Members:   members,
		CreatedAt: conv.CreatedAt,
		UpdatedAt: conv.UpdatedAt,
	}, nil
}

func (s *Service) ListConversations(ctx context.Context, userID uuid.UUID, limit int, cursor *ConversationCursor) ([]Conversation, *ConversationCursor, error) {
	if s.convStore == nil {
		return nil, nil, errors.New("conversation store not configured")
	}
	var cursorUpdatedAt *time.Time
	var cursorID *uuid.UUID
	if cursor != nil {
		cursorUpdatedAt = &cursor.UpdatedAt
		cursorID = &cursor.ID
	}
	convs, err := s.convStore.ListConversationsByUser(ctx, userID, limit, cursorUpdatedAt, cursorID)
	if err != nil {
		return nil, nil, err
	}

	out := make([]Conversation, 0, len(convs))
	for _, c := range convs {
		members, err := s.convStore.GetMembers(ctx, c.ID)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, Conversation{
			ID:                 c.ID,
			Type:               c.Type,
			Name:               c.Name,
			IconURL:            c.IconURL,
			Members:            members,
			LastMessageAt:      c.LastMessageAt,
			LastMessagePreview: c.LastMessagePreview,
			CreatedAt:          c.CreatedAt,
			UpdatedAt:          c.UpdatedAt,
		})
	}

	var next *ConversationCursor
	if len(convs) == limit {
		last := convs[len(convs)-1]
		next = &ConversationCursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
	return out, next, nil
}

func (s *Service) SendMessageToConversation(ctx context.Context, senderID, conversationID uuid.UUID, input SendMessageInput) (*scylla.Message, error) {
	if s.convStore == nil {
		return nil, errors.New("conversation store not configured")
	}
	if input.MessageType == "" {
		input.MessageType = "text"
	}

	conv, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	if conv == nil {
		return nil, errors.New("conversation not found")
	}
	ok, err := s.convStore.CheckMembership(ctx, conversationID, senderID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("not a conversation member")
	}
	if conv.Type == "group" {
		return s.sendGroupMessage(ctx, senderID, conversationID, input)
	}

	members, err := s.convStore.GetMembers(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	if len(members) != 2 {
		return nil, errors.New("invalid direct conversation members")
	}
	var receiverID uuid.UUID
	if members[0] == senderID {
		receiverID = members[1]
	} else {
		receiverID = members[0]
	}

	msg, err := s.SendMessage(ctx, senderID, receiverID, input)
	if err != nil {
		return nil, err
	}
	_ = s.convStore.TouchConversationWithPreview(ctx, conversationID, time.Now(), messagePreview(input))
	return msg, nil
}

func (s *Service) GetMessagesByConversation(ctx context.Context, userID, conversationID uuid.UUID, limit int) ([]scylla.Message, error) {
	if s.convStore == nil {
		return nil, errors.New("conversation store not configured")
	}
	conv, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	if conv == nil {
		return nil, errors.New("conversation not found")
	}
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("not a conversation member")
	}
	if conv.Type == "group" {
		return s.getGroupMessages(ctx, conversationID, limit)
	}
	members, err := s.convStore.GetMembers(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	if len(members) != 2 {
		return nil, errors.New("invalid direct conversation members")
	}
	var other uuid.UUID
	if members[0] == userID {
		other = members[1]
	} else {
		other = members[0]
	}
	return s.GetMessages(ctx, userID, other, limit)
}

// sendGroupMessage saves a message to ScyllaDB and broadcasts to all group members via Redis PubSub.
func (s *Service) sendGroupMessage(ctx context.Context, senderID, conversationID uuid.UUID, input SendMessageInput) (*scylla.Message, error) {
	members, err := s.convStore.GetMembers(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("get group members: %w", err)
	}

	msgID := uuid.New().String()
	timestamp := time.Now()

	msg := &scylla.Message{
		ChatID:          conversationID.String(),
		Timestamp:       timestamp,
		MsgID:           msgID,
		SenderID:        senderID.String(),
		ReceiverID:      "",
		Text:            input.Text,
		MessageType:     input.MessageType,
		MediaID:         input.MediaID,
		ReplyToID:       input.ReplyToID,
		ForwardedFromID: input.ForwardedFromID,
	}

	l := slog.With("chat_id", conversationID.String(), "sender_id", senderID, "msg_id", msgID)

	if err := s.scyllaStore.CreateMessage(ctx, msg); err != nil {
		l.Error("failed to save group message to scylladb", "error", err)
		return nil, fmt.Errorf("save group message: %w", err)
	}

	_ = s.convStore.TouchConversationWithPreview(ctx, conversationID, timestamp, messagePreview(input))

	// Broadcast to all members except sender
	go func() {
		pubCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		payload, _ := json.Marshal(map[string]interface{}{
			"type":              "message",
			"msg_id":            msg.MsgID,
			"conversation_id":   conversationID.String(),
			"sender_id":         senderID.String(),
			"text":              input.Text,
			"message_type":      input.MessageType,
			"media_id":          input.MediaID,
			"reply_to_id":       input.ReplyToID,
			"forwarded_from_id": input.ForwardedFromID,
			"created_at":        msg.Timestamp.Format(time.RFC3339Nano),
		})
		for _, memberID := range members {
			if memberID == senderID {
				continue
			}
			channel := fmt.Sprintf("chat:%s", memberID.String())
			if err := s.rdb.Publish(pubCtx, channel, payload).Err(); err != nil {
				l.Error("failed to publish group message", "error", err, "member_id", memberID)
			}
		}
	}()

	// Cache in Redis list
	go func() {
		cacheCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cacheKey := fmt.Sprintf("chat_history:%s", conversationID.String())
		msgJSON, _ := json.Marshal(msg)
		pipe := s.rdb.Pipeline()
		pipe.LPush(cacheCtx, cacheKey, msgJSON)
		pipe.LTrim(cacheCtx, cacheKey, 0, 99)
		if _, err := pipe.Exec(cacheCtx); err != nil {
			l.Error("failed to update redis cache", "error", err)
		}
	}()

	// Kafka
	go func() {
		kafkaCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.kafka.PublishMessage(kafkaCtx, msg); err != nil {
			l.Error("failed to publish to kafka", "error", err)
		}
	}()

	return msg, nil
}

func (s *Service) getGroupMessages(ctx context.Context, conversationID uuid.UUID, limit int) ([]scylla.Message, error) {
	return s.scyllaStore.GetMessages(ctx, conversationID.String(), limit)
}

// ---------------------------------------------------------------------------
// Edit / Delete / React
// ---------------------------------------------------------------------------

// EditMessage edits the text of an existing message.
func (s *Service) EditMessage(ctx context.Context, userID, conversationID uuid.UUID, msgID string, ts time.Time, newText string) error {
	chatID, _, err := s.resolveChatID(ctx, userID, conversationID)
	if err != nil {
		return err
	}

	msg, err := s.scyllaStore.GetMessage(ctx, chatID, msgID, ts)
	if err != nil {
		return fmt.Errorf("message not found: %w", err)
	}
	if msg.SenderID != userID.String() {
		return errors.New("can only edit your own messages")
	}
	if msg.IsDeleted {
		return errors.New("cannot edit a deleted message")
	}

	if err := s.scyllaStore.UpdateMessageText(ctx, chatID, msgID, ts, newText); err != nil {
		return fmt.Errorf("failed to edit message: %w", err)
	}

	// Invalidate Redis cache
	cacheKey := fmt.Sprintf("chat_history:%s", chatID)
	s.rdb.Del(ctx, cacheKey)

	// Broadcast to conversation
	go func() {
		bctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		s.broadcastToConversation(bctx, conversationID, nil, map[string]interface{}{
			"type":            "message_edited",
			"conversation_id": conversationID.String(),
			"msg_id":          msgID,
			"new_text":        newText,
			"edited_at":       time.Now().Format(time.RFC3339Nano),
		})
	}()

	return nil
}

// DeleteMessage soft-deletes a message.
func (s *Service) DeleteMessage(ctx context.Context, userID, conversationID uuid.UUID, msgID string, ts time.Time) error {
	chatID, _, err := s.resolveChatID(ctx, userID, conversationID)
	if err != nil {
		return err
	}

	msg, err := s.scyllaStore.GetMessage(ctx, chatID, msgID, ts)
	if err != nil {
		return fmt.Errorf("message not found: %w", err)
	}
	if msg.SenderID != userID.String() {
		return errors.New("can only delete your own messages")
	}

	if err := s.scyllaStore.SoftDeleteMessage(ctx, chatID, msgID, ts); err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}

	// Invalidate Redis cache
	cacheKey := fmt.Sprintf("chat_history:%s", chatID)
	s.rdb.Del(ctx, cacheKey)

	// Broadcast
	go func() {
		bctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		s.broadcastToConversation(bctx, conversationID, nil, map[string]interface{}{
			"type":            "message_deleted",
			"conversation_id": conversationID.String(),
			"msg_id":          msgID,
		})
	}()

	return nil
}

// ToggleReaction adds or removes a reaction. Returns true if added, false if removed.
func (s *Service) ToggleReaction(ctx context.Context, userID, conversationID uuid.UUID, msgID, emoji string) (bool, error) {
	chatID, _, err := s.resolveChatID(ctx, userID, conversationID)
	if err != nil {
		return false, err
	}

	// Check if user already reacted with this emoji
	reactions, err := s.scyllaStore.GetReactions(ctx, chatID, msgID)
	if err != nil {
		return false, err
	}

	var exists bool
	for _, r := range reactions {
		if r.UserID == userID.String() && r.Emoji == emoji {
			exists = true
			break
		}
	}

	added := false
	if exists {
		if err := s.scyllaStore.RemoveReaction(ctx, chatID, msgID, emoji, userID.String()); err != nil {
			return false, err
		}
	} else {
		if err := s.scyllaStore.AddReaction(ctx, &scylla.Reaction{
			ChatID:    chatID,
			MsgID:     msgID,
			Emoji:     emoji,
			UserID:    userID.String(),
			CreatedAt: time.Now(),
		}); err != nil {
			return false, err
		}
		added = true
	}

	// Broadcast
	go func() {
		bctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		s.broadcastToConversation(bctx, conversationID, nil, map[string]interface{}{
			"type":            "reaction_update",
			"conversation_id": conversationID.String(),
			"msg_id":          msgID,
			"emoji":           emoji,
			"user_id":         userID.String(),
			"added":           added,
		})
	}()

	return added, nil
}

// ---------------------------------------------------------------------------
// Read Receipts & Typing
// ---------------------------------------------------------------------------

// MarkRead marks a conversation as read up to a given message.
func (s *Service) MarkRead(ctx context.Context, userID, conversationID uuid.UUID, messageID string) error {
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("not a conversation member")
	}

	if err := s.convStore.MarkRead(ctx, conversationID, userID, messageID); err != nil {
		return err
	}

	// Broadcast read receipt
	go func() {
		bctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		s.broadcastToConversation(bctx, conversationID, &userID, map[string]interface{}{
			"type":            "read_receipt",
			"conversation_id": conversationID.String(),
			"user_id":         userID.String(),
			"message_id":      messageID,
			"read_at":         time.Now().Format(time.RFC3339Nano),
		})
	}()

	return nil
}

// SetTyping broadcasts a typing indicator to conversation members.
func (s *Service) SetTyping(ctx context.Context, userID, conversationID uuid.UUID) error {
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("not a conversation member")
	}

	// Set ephemeral Redis key with 3s TTL
	typingKey := fmt.Sprintf("typing:%s:%s", conversationID.String(), userID.String())
	s.rdb.Set(ctx, typingKey, "1", 3*time.Second)

	// Broadcast to other members
	go func() {
		bctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		s.broadcastToConversation(bctx, conversationID, &userID, map[string]interface{}{
			"type":            "typing",
			"conversation_id": conversationID.String(),
			"user_id":         userID.String(),
			"is_typing":       true,
		})
	}()

	return nil
}

// ---------------------------------------------------------------------------
// Conversation Management
// ---------------------------------------------------------------------------

// UpdateConversation updates group name and/or icon.
func (s *Service) UpdateConversation(ctx context.Context, userID, conversationID uuid.UUID, name, iconURL *string) error {
	conv, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if conv == nil {
		return errors.New("conversation not found")
	}
	if conv.Type != "group" {
		return errors.New("cannot update a direct conversation")
	}
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("not a conversation member")
	}
	return s.convStore.UpdateConversation(ctx, conversationID, name, iconURL)
}

// LeaveConversation removes the user from a conversation.
func (s *Service) LeaveConversation(ctx context.Context, userID, conversationID uuid.UUID) error {
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("not a conversation member")
	}
	return s.convStore.LeaveConversation(ctx, conversationID, userID)
}

// CreateGroupConversation creates a new group-type conversation.
func (s *Service) CreateGroupConversation(ctx context.Context, creatorID uuid.UUID, memberIDs []uuid.UUID, name string) (uuid.UUID, error) {
	if s.convStore == nil {
		return uuid.Nil, errors.New("conversation store not configured")
	}
	return s.convStore.CreateGroupConversation(ctx, creatorID, memberIDs, name)
}

// AddGroupConversationMember adds a member to a group conversation.
func (s *Service) AddGroupConversationMember(ctx context.Context, conversationID, userID uuid.UUID) error {
	if s.convStore == nil {
		return errors.New("conversation store not configured")
	}
	return s.convStore.AddGroupConversationMember(ctx, conversationID, userID)
}

// RemoveGroupConversationMember removes a member from a group conversation.
func (s *Service) RemoveGroupConversationMember(ctx context.Context, conversationID, userID uuid.UUID) error {
	if s.convStore == nil {
		return errors.New("conversation store not configured")
	}
	return s.convStore.RemoveGroupConversationMember(ctx, conversationID, userID)
}

// ---------------------------------------------------------------------------
// Pinned messages
// ---------------------------------------------------------------------------

// PinMessage pins a message in a conversation. The caller must be a member.
func (s *Service) PinMessage(ctx context.Context, userID, conversationID uuid.UUID, messageID string) error {
	if s.convStore == nil {
		return errors.New("conversation store not configured")
	}
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("not a conversation member")
	}
	if err := s.convStore.PinMessage(ctx, conversationID, messageID, userID); err != nil {
		return err
	}
	// Broadcast pin event to all conversation members.
	go func() {
		bctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		s.broadcastToConversation(bctx, conversationID, nil, map[string]interface{}{
			"type":            "message_pinned",
			"conversation_id": conversationID.String(),
			"message_id":      messageID,
			"pinned_by":       userID.String(),
			"pinned_at":       time.Now().Format(time.RFC3339Nano),
		})
	}()
	return nil
}

// UnpinMessage removes the pinned message from a conversation. The caller must be a member.
func (s *Service) UnpinMessage(ctx context.Context, userID, conversationID uuid.UUID) error {
	if s.convStore == nil {
		return errors.New("conversation store not configured")
	}
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("not a conversation member")
	}
	if err := s.convStore.UnpinMessage(ctx, conversationID); err != nil {
		return err
	}
	// Broadcast unpin event to all conversation members.
	go func() {
		bctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		s.broadcastToConversation(bctx, conversationID, nil, map[string]interface{}{
			"type":            "message_unpinned",
			"conversation_id": conversationID.String(),
			"unpinned_by":     userID.String(),
		})
	}()
	return nil
}

// GetPinnedMessage returns the currently pinned message for a conversation.
// Returns nil, nil when no message is pinned.
func (s *Service) GetPinnedMessage(ctx context.Context, userID, conversationID uuid.UUID) (*postgres.PinnedMessage, error) {
	if s.convStore == nil {
		return nil, errors.New("conversation store not configured")
	}
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("not a conversation member")
	}
	return s.convStore.GetPinnedMessage(ctx, conversationID)
}
