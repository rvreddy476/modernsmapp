package scylla

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gocql/gocql"
)

type MessageStore struct {
	session *gocql.Session
}

func New(session *gocql.Session) *MessageStore {
	return &MessageStore{session: session}
}

type Message struct {
	ChatID          string     `json:"chat_id"`
	Timestamp       time.Time  `json:"timestamp"`
	MsgID           string     `json:"msg_id"`
	SenderID        string     `json:"sender_id"`
	ReceiverID      string     `json:"receiver_id"`
	Text            string     `json:"text"`
	MessageType     string     `json:"message_type"`
	MediaID         string     `json:"media_id,omitempty"`
	ReplyToID       string     `json:"reply_to_id,omitempty"`
	ForwardedFromID string     `json:"forwarded_from_id,omitempty"`
	IsEdited        bool       `json:"is_edited"`
	EditedAt        *time.Time `json:"edited_at,omitempty"`
	IsDeleted       bool       `json:"is_deleted"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
}

type Reaction struct {
	ChatID    string    `json:"chat_id"`
	MsgID     string    `json:"msg_id"`
	Emoji     string    `json:"emoji"`
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
}

// GenerateChatID creates a unique, deterministic ID for a 1:1 chat between two users.
func GenerateChatID(userA, userB string) string {
	ids := []string{userA, userB}
	sort.Strings(ids)
	combined := strings.Join(ids, ":")
	hash := sha256.Sum256([]byte(combined))
	return fmt.Sprintf("%x", hash)
}

// CreateMessage saves a message to ScyllaDB.
func (s *MessageStore) CreateMessage(ctx context.Context, m *Message) error {
	if m.MessageType == "" {
		m.MessageType = "text"
	}
	return s.session.Query(`
		INSERT INTO postbook.messages (chat_id, timestamp, msg_id, sender_id, receiver_id, text, message_type, media_id, reply_to_id, forwarded_from_id, is_edited, is_deleted)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, m.ChatID, m.Timestamp, m.MsgID, m.SenderID, m.ReceiverID, m.Text,
		m.MessageType, m.MediaID, m.ReplyToID, m.ForwardedFromID, false, false,
	).WithContext(ctx).Exec()
}

// GetMessages retrieves message history for a chat.
func (s *MessageStore) GetMessages(ctx context.Context, chatID string, limit int) ([]Message, error) {
	iter := s.session.Query(`
		SELECT chat_id, timestamp, msg_id, sender_id, receiver_id, text,
		       message_type, media_id, reply_to_id, forwarded_from_id,
		       is_edited, edited_at, is_deleted, deleted_at
		FROM postbook.messages
		WHERE chat_id = ?
		LIMIT ?
	`, chatID, limit).WithContext(ctx).Iter()

	var messages []Message
	var m Message
	for iter.Scan(
		&m.ChatID, &m.Timestamp, &m.MsgID, &m.SenderID, &m.ReceiverID, &m.Text,
		&m.MessageType, &m.MediaID, &m.ReplyToID, &m.ForwardedFromID,
		&m.IsEdited, &m.EditedAt, &m.IsDeleted, &m.DeletedAt,
	) {
		if m.MessageType == "" {
			m.MessageType = "text"
		}
		messages = append(messages, m)
		m = Message{}
	}

	if err := iter.Close(); err != nil {
		return nil, err
	}
	return messages, nil
}

// GetMessage retrieves a single message by primary key.
func (s *MessageStore) GetMessage(ctx context.Context, chatID, msgID string, ts time.Time) (*Message, error) {
	var m Message
	err := s.session.Query(`
		SELECT chat_id, timestamp, msg_id, sender_id, receiver_id, text,
		       message_type, media_id, reply_to_id, forwarded_from_id,
		       is_edited, edited_at, is_deleted, deleted_at
		FROM postbook.messages
		WHERE chat_id = ? AND timestamp = ? AND msg_id = ?
	`, chatID, ts, msgID).WithContext(ctx).Scan(
		&m.ChatID, &m.Timestamp, &m.MsgID, &m.SenderID, &m.ReceiverID, &m.Text,
		&m.MessageType, &m.MediaID, &m.ReplyToID, &m.ForwardedFromID,
		&m.IsEdited, &m.EditedAt, &m.IsDeleted, &m.DeletedAt,
	)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// UpdateMessageText edits a message's text and marks it as edited.
func (s *MessageStore) UpdateMessageText(ctx context.Context, chatID, msgID string, ts time.Time, newText string) error {
	now := time.Now()
	return s.session.Query(`
		UPDATE postbook.messages
		SET text = ?, is_edited = true, edited_at = ?
		WHERE chat_id = ? AND timestamp = ? AND msg_id = ?
	`, newText, now, chatID, ts, msgID).WithContext(ctx).Exec()
}

// SoftDeleteMessage marks a message as deleted and clears its text.
func (s *MessageStore) SoftDeleteMessage(ctx context.Context, chatID, msgID string, ts time.Time) error {
	now := time.Now()
	return s.session.Query(`
		UPDATE postbook.messages
		SET is_deleted = true, deleted_at = ?, text = ''
		WHERE chat_id = ? AND timestamp = ? AND msg_id = ?
	`, now, chatID, ts, msgID).WithContext(ctx).Exec()
}

// AddReaction adds a reaction to a message.
func (s *MessageStore) AddReaction(ctx context.Context, r *Reaction) error {
	return s.session.Query(`
		INSERT INTO postbook.message_reactions (chat_id, msg_id, emoji, user_id, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, r.ChatID, r.MsgID, r.Emoji, r.UserID, r.CreatedAt).WithContext(ctx).Exec()
}

// RemoveReaction removes a reaction from a message.
func (s *MessageStore) RemoveReaction(ctx context.Context, chatID, msgID, emoji, userID string) error {
	return s.session.Query(`
		DELETE FROM postbook.message_reactions
		WHERE chat_id = ? AND msg_id = ? AND emoji = ? AND user_id = ?
	`, chatID, msgID, emoji, userID).WithContext(ctx).Exec()
}

// GetReactions retrieves all reactions for a message.
func (s *MessageStore) GetReactions(ctx context.Context, chatID, msgID string) ([]Reaction, error) {
	iter := s.session.Query(`
		SELECT chat_id, msg_id, emoji, user_id, created_at
		FROM postbook.message_reactions
		WHERE chat_id = ? AND msg_id = ?
	`, chatID, msgID).WithContext(ctx).Iter()

	var reactions []Reaction
	var r Reaction
	for iter.Scan(&r.ChatID, &r.MsgID, &r.Emoji, &r.UserID, &r.CreatedAt) {
		reactions = append(reactions, r)
		r = Reaction{}
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return reactions, nil
}

// GetReactionsBatch retrieves reactions for multiple messages in a chat.
func (s *MessageStore) GetReactionsBatch(ctx context.Context, chatID string, msgIDs []string) (map[string][]Reaction, error) {
	result := make(map[string][]Reaction)
	for _, msgID := range msgIDs {
		reactions, err := s.GetReactions(ctx, chatID, msgID)
		if err != nil {
			return nil, err
		}
		if len(reactions) > 0 {
			result[msgID] = reactions
		}
	}
	return result, nil
}
