package scylla

import (
	"context"
	"errors"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

type MessageStore struct {
	session *gocql.Session
}

func New(session *gocql.Session) *MessageStore {
	return &MessageStore{session: session}
}

type Message struct {
	ConversationID uuid.UUID  `json:"conversation_id"`
	Bucket         string     `json:"bucket"`
	Ts             time.Time  `json:"ts"`
	MsgID          uuid.UUID  `json:"msg_id"`
	SenderID       uuid.UUID  `json:"sender_id"`
	Type           string     `json:"type"`
	Text           string     `json:"text,omitempty"`
	MediaID        *uuid.UUID `json:"media_id,omitempty"`
	IsDeleted      bool       `json:"is_deleted"`
	CreatedAt      time.Time  `json:"created_at"`
}

type MessageCursor struct {
	Bucket string    `json:"bucket"`
	Ts     time.Time `json:"ts"`
	MsgID  uuid.UUID `json:"msg_id"`
}

// scanTarget holds gocql-compatible types for scanning Scylla rows.
type scanTarget struct {
	ConversationID gocql.UUID
	Bucket         string
	Ts             time.Time
	MsgID          gocql.UUID
	SenderID       gocql.UUID
	Type           string
	Text           string
	MediaID        *gocql.UUID
	IsDeleted      bool
	CreatedAt      time.Time
}

func (t *scanTarget) toMessage() Message {
	m := Message{
		ConversationID: uuidFromGocql(t.ConversationID),
		Bucket:         t.Bucket,
		Ts:             t.Ts,
		MsgID:          uuidFromGocql(t.MsgID),
		SenderID:       uuidFromGocql(t.SenderID),
		Type:           t.Type,
		Text:           t.Text,
		IsDeleted:      t.IsDeleted,
		CreatedAt:      t.CreatedAt,
	}
	if t.MediaID != nil {
		id := uuidFromGocql(*t.MediaID)
		m.MediaID = &id
	}
	return m
}

func uuidFromGocql(g gocql.UUID) uuid.UUID {
	var id uuid.UUID
	copy(id[:], g[:])
	return id
}

func uuidToGocql(id uuid.UUID) gocql.UUID {
	var g gocql.UUID
	copy(g[:], id[:])
	return g
}

func uuidPtrToGocql(id *uuid.UUID) *gocql.UUID {
	if id == nil {
		return nil
	}
	g := uuidToGocql(*id)
	return &g
}

// CurrentBucket returns the current month bucket string in YYYYMM format.
func CurrentBucket() string {
	return time.Now().UTC().Format("200601")
}

// PrevBucket returns the previous month bucket given a YYYYMM string.
func PrevBucket(bucket string) string {
	t, err := time.Parse("200601", bucket)
	if err != nil {
		return ""
	}
	return t.AddDate(0, -1, 0).Format("200601")
}

// CreateMessage writes a message to the bucketed messages table.
func (s *MessageStore) CreateMessage(ctx context.Context, msg *Message) error {
	return s.session.Query(`
		INSERT INTO messages (conversation_id, bucket, ts, msg_id, sender_id, type, text, media_id, is_deleted, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, uuidToGocql(msg.ConversationID), msg.Bucket, msg.Ts, uuidToGocql(msg.MsgID), uuidToGocql(msg.SenderID), msg.Type, msg.Text, uuidPtrToGocql(msg.MediaID), msg.IsDeleted, msg.CreatedAt).
		WithContext(ctx).Exec()
}

func (s *MessageStore) GetMessage(ctx context.Context, conversationID uuid.UUID, bucket string, ts time.Time, msgID uuid.UUID) (*Message, error) {
	var t scanTarget
	err := s.session.Query(`
		SELECT conversation_id, bucket, ts, msg_id, sender_id, type, text, media_id, is_deleted, created_at
		FROM messages
		WHERE conversation_id = ? AND bucket = ? AND ts = ? AND msg_id = ?
		LIMIT 1
	`, uuidToGocql(conversationID), bucket, ts, uuidToGocql(msgID)).WithContext(ctx).
		Scan(&t.ConversationID, &t.Bucket, &t.Ts, &t.MsgID, &t.SenderID, &t.Type, &t.Text, &t.MediaID, &t.IsDeleted, &t.CreatedAt)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	m := t.toMessage()
	return &m, nil
}

// GetMessages retrieves messages with cursor-based pagination across monthly buckets.
// Walks backward through buckets until limit is reached or 12 months scanned.
func (s *MessageStore) GetMessages(ctx context.Context, conversationID uuid.UUID, cursor *MessageCursor, limit int) ([]Message, *MessageCursor, error) {
	var messages []Message
	remaining := limit

	bucket := CurrentBucket()
	if cursor != nil {
		bucket = cursor.Bucket
	}

	convGocql := uuidToGocql(conversationID)

	maxBuckets := 12
	for i := 0; i < maxBuckets && remaining > 0; i++ {
		if bucket == "" {
			break
		}

		var q *gocql.Query
		if cursor != nil && i == 0 {
			q = s.session.Query(`
				SELECT conversation_id, bucket, ts, msg_id, sender_id, type, text, media_id, is_deleted, created_at
				FROM messages
				WHERE conversation_id = ? AND bucket = ? AND (ts, msg_id) < (?, ?)
				LIMIT ?
			`, convGocql, bucket, cursor.Ts, uuidToGocql(cursor.MsgID), remaining)
		} else {
			q = s.session.Query(`
				SELECT conversation_id, bucket, ts, msg_id, sender_id, type, text, media_id, is_deleted, created_at
				FROM messages
				WHERE conversation_id = ? AND bucket = ?
				LIMIT ?
			`, convGocql, bucket, remaining)
		}

		iter := q.WithContext(ctx).Iter()
		var t scanTarget
		for iter.Scan(&t.ConversationID, &t.Bucket, &t.Ts, &t.MsgID, &t.SenderID, &t.Type, &t.Text, &t.MediaID, &t.IsDeleted, &t.CreatedAt) {
			if !t.IsDeleted {
				messages = append(messages, t.toMessage())
				remaining--
			}
			if remaining <= 0 {
				break
			}
		}
		if err := iter.Close(); err != nil {
			return nil, nil, err
		}

		bucket = PrevBucket(bucket)
	}

	var nextCursor *MessageCursor
	if len(messages) > 0 {
		last := messages[len(messages)-1]
		nextCursor = &MessageCursor{
			Bucket: last.Bucket,
			Ts:     last.Ts,
			MsgID:  last.MsgID,
		}
	}

	return messages, nextCursor, nil
}

// SoftDeleteMessage marks a message as deleted (avoids Scylla tombstones).
func (s *MessageStore) SoftDeleteMessage(ctx context.Context, conversationID uuid.UUID, bucket string, ts time.Time, msgID uuid.UUID) error {
	return s.session.Query(`
		UPDATE messages SET is_deleted = true
		WHERE conversation_id = ? AND bucket = ? AND ts = ? AND msg_id = ?
	`, uuidToGocql(conversationID), bucket, ts, uuidToGocql(msgID)).WithContext(ctx).Exec()
}

// UpsertInbox writes a row to the conversations_by_user inbox projection.
// Called for each member of a conversation when a message is sent.
func (s *MessageStore) UpsertInbox(ctx context.Context, userID, conversationID, senderID uuid.UUID, text string, ts time.Time) error {
	bucket := ts.UTC().Format("200601")
	truncatedText := text
	if len(truncatedText) > 100 {
		truncatedText = truncatedText[:100]
	}
	return s.session.Query(`
		INSERT INTO conversations_by_user (user_id, bucket, last_ts, conversation_id, last_sender_id, last_text)
		VALUES (?, ?, ?, ?, ?, ?)
	`, uuidToGocql(userID), bucket, ts, uuidToGocql(conversationID), uuidToGocql(senderID), truncatedText).WithContext(ctx).Exec()
}

// --- Reactions ---

type Reaction struct {
	ConversationID uuid.UUID `json:"conversation_id"`
	Bucket         string    `json:"bucket"`
	MsgTs          time.Time `json:"msg_ts"`
	MsgID          uuid.UUID `json:"msg_id"`
	Emoji          string    `json:"emoji"`
	UserID         uuid.UUID `json:"user_id"`
	CreatedAt      time.Time `json:"created_at"`
}

type MsgKey struct {
	Ts    time.Time
	MsgID uuid.UUID
}

// AddReaction inserts a reaction row. Scylla INSERT is an upsert, so re-adding is idempotent.
func (s *MessageStore) AddReaction(ctx context.Context, convID uuid.UUID, bucket string, msgTs time.Time, msgID uuid.UUID, emoji string, userID uuid.UUID) error {
	return s.session.Query(`
		INSERT INTO message_reactions (conversation_id, bucket, msg_ts, msg_id, emoji, user_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, uuidToGocql(convID), bucket, msgTs, uuidToGocql(msgID), emoji, uuidToGocql(userID), time.Now()).
		WithContext(ctx).Exec()
}

// RemoveReaction deletes a reaction row.
func (s *MessageStore) RemoveReaction(ctx context.Context, convID uuid.UUID, bucket string, msgTs time.Time, msgID uuid.UUID, emoji string, userID uuid.UUID) error {
	return s.session.Query(`
		DELETE FROM message_reactions
		WHERE conversation_id = ? AND bucket = ? AND msg_ts = ? AND msg_id = ? AND emoji = ? AND user_id = ?
	`, uuidToGocql(convID), bucket, msgTs, uuidToGocql(msgID), emoji, uuidToGocql(userID)).
		WithContext(ctx).Exec()
}

// HasReaction checks if a specific reaction exists.
func (s *MessageStore) HasReaction(ctx context.Context, convID uuid.UUID, bucket string, msgTs time.Time, msgID uuid.UUID, emoji string, userID uuid.UUID) (bool, error) {
	var count int
	err := s.session.Query(`
		SELECT COUNT(*) FROM message_reactions
		WHERE conversation_id = ? AND bucket = ? AND msg_ts = ? AND msg_id = ? AND emoji = ? AND user_id = ?
	`, uuidToGocql(convID), bucket, msgTs, uuidToGocql(msgID), emoji, uuidToGocql(userID)).
		WithContext(ctx).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetReactionsForMessages retrieves reactions for a batch of messages in the same bucket.
// Returns reactions grouped by msg_id.
func (s *MessageStore) GetReactionsForMessages(ctx context.Context, convID uuid.UUID, bucket string, keys []MsgKey) (map[uuid.UUID][]Reaction, error) {
	result := make(map[uuid.UUID][]Reaction)
	if len(keys) == 0 {
		return result, nil
	}

	convGocql := uuidToGocql(convID)
	for _, key := range keys {
		iter := s.session.Query(`
			SELECT conversation_id, bucket, msg_ts, msg_id, emoji, user_id, created_at
			FROM message_reactions
			WHERE conversation_id = ? AND bucket = ? AND msg_ts = ? AND msg_id = ?
		`, convGocql, bucket, key.Ts, uuidToGocql(key.MsgID)).WithContext(ctx).Iter()

		var (
			rConvID, rMsgID, rUserID gocql.UUID
			rBucket, rEmoji          string
			rMsgTs, rCreatedAt       time.Time
		)
		for iter.Scan(&rConvID, &rBucket, &rMsgTs, &rMsgID, &rEmoji, &rUserID, &rCreatedAt) {
			r := Reaction{
				ConversationID: uuidFromGocql(rConvID),
				Bucket:         rBucket,
				MsgTs:          rMsgTs,
				MsgID:          uuidFromGocql(rMsgID),
				Emoji:          rEmoji,
				UserID:         uuidFromGocql(rUserID),
				CreatedAt:      rCreatedAt,
			}
			result[r.MsgID] = append(result[r.MsgID], r)
		}
		if err := iter.Close(); err != nil {
			return nil, err
		}
	}

	return result, nil
}
