package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrDeliveryIntentConflict = errors.New("delivery intent idempotency conflict")

type MessageDeliveryIntent struct {
	IdempotencyKey    string
	RequestHash       string
	ConversationID    uuid.UUID
	SenderID          uuid.UUID
	MessageID         uuid.UUID
	Bucket            string
	MessageTS         time.Time
	MessageType       string
	MessageText       string
	MediaID           *uuid.UUID
	MemberIDs         []uuid.UUID
	FirstRequest      bool
	RequestReceiverID *uuid.UUID
	SourceApp         string
	MatchID           *uuid.UUID
	CompletedAt       *time.Time
}

func (s *ConversationStore) ReserveMessageDeliveryIntent(ctx context.Context, intent MessageDeliveryIntent) (*MessageDeliveryIntent, error) {
	_, err := s.db.Exec(ctx, `
		INSERT INTO chat.message_delivery_intents (
			idempotency_key, request_hash, conversation_id, sender_id,
			message_id, bucket, message_ts, message_type, message_text,
			media_id, member_ids, first_request_message,
			request_receiver_id, source_app, match_id
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (idempotency_key) DO NOTHING
	`, intent.IdempotencyKey, intent.RequestHash, intent.ConversationID, intent.SenderID,
		intent.MessageID, intent.Bucket, intent.MessageTS, intent.MessageType, intent.MessageText,
		intent.MediaID, intent.MemberIDs, intent.FirstRequest, intent.RequestReceiverID,
		intent.SourceApp, intent.MatchID)
	if err != nil {
		return nil, err
	}
	reserved, err := s.GetMessageDeliveryIntent(ctx, intent.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if reserved == nil {
		return nil, errors.New("reserved delivery intent disappeared")
	}
	if reserved.RequestHash != intent.RequestHash {
		return nil, ErrDeliveryIntentConflict
	}
	return reserved, nil
}

func (s *ConversationStore) GetMessageDeliveryIntent(ctx context.Context, key string) (*MessageDeliveryIntent, error) {
	var intent MessageDeliveryIntent
	err := s.db.QueryRow(ctx, `
		SELECT idempotency_key, request_hash, conversation_id, sender_id,
		       message_id, bucket, message_ts, message_type, message_text,
		       media_id, member_ids, first_request_message,
		       request_receiver_id, source_app, match_id, completed_at
		FROM chat.message_delivery_intents WHERE idempotency_key = $1
	`, key).Scan(&intent.IdempotencyKey, &intent.RequestHash, &intent.ConversationID,
		&intent.SenderID, &intent.MessageID, &intent.Bucket, &intent.MessageTS,
		&intent.MessageType, &intent.MessageText, &intent.MediaID, &intent.MemberIDs,
		&intent.FirstRequest, &intent.RequestReceiverID, &intent.SourceApp,
		&intent.MatchID, &intent.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &intent, err
}

func (s *ConversationStore) FetchPendingMessageDeliveryIntents(ctx context.Context, limit int) ([]MessageDeliveryIntent, error) {
	rows, err := s.db.Query(ctx, `
		SELECT idempotency_key, request_hash, conversation_id, sender_id,
		       message_id, bucket, message_ts, message_type, message_text,
		       media_id, member_ids, first_request_message,
		       request_receiver_id, source_app, match_id, completed_at
		FROM chat.message_delivery_intents
		WHERE completed_at IS NULL
		ORDER BY created_at
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var intents []MessageDeliveryIntent
	for rows.Next() {
		var intent MessageDeliveryIntent
		if err := rows.Scan(&intent.IdempotencyKey, &intent.RequestHash,
			&intent.ConversationID, &intent.SenderID, &intent.MessageID,
			&intent.Bucket, &intent.MessageTS, &intent.MessageType,
			&intent.MessageText, &intent.MediaID, &intent.MemberIDs,
			&intent.FirstRequest, &intent.RequestReceiverID, &intent.SourceApp,
			&intent.MatchID, &intent.CompletedAt); err != nil {
			return nil, err
		}
		intents = append(intents, intent)
	}
	return intents, rows.Err()
}

func (s *ConversationStore) CompleteMessageDeliveryIntent(ctx context.Context, key string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.message_delivery_intents
		SET completed_at = COALESCE(completed_at, NOW())
		WHERE idempotency_key = $1
	`, key)
	return err
}

func (s *ConversationStore) InsertOutboxEventOnce(ctx context.Context, dedupeKey, eventType string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO chat.outbox_events (dedupe_key, event_type, payload)
		VALUES ($1, $2, $3)
		ON CONFLICT (dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING
	`, dedupeKey, eventType, data)
	return err
}

func (s *ConversationStore) InsertMessageMediaReference(ctx context.Context, messageID, mediaID, conversationID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO chat.message_media_references (message_id, media_id, conversation_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (message_id) DO UPDATE
		SET media_id = EXCLUDED.media_id, conversation_id = EXCLUDED.conversation_id
	`, messageID, mediaID, conversationID)
	return err
}

func (s *ConversationStore) ViewerMayAccessChatMedia(ctx context.Context, viewerID, mediaID uuid.UUID) (bool, error) {
	var allowed bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM chat.message_media_references r
			JOIN chat.conversation_members m ON m.conversation_id = r.conversation_id
			WHERE r.media_id = $1 AND m.user_id = $2 AND m.left_at IS NULL
		)
	`, mediaID, viewerID).Scan(&allowed)
	return allowed, err
}
