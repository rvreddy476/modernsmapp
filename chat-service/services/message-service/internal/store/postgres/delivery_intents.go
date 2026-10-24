package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/atpost/chat-message-service/internal/contentcrypt"
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
	// Quoted-reply snapshot; rides the durable intent so repair replays it.
	ReplyToID       *uuid.UUID
	ReplyToPreview  string
	ReplyToSenderID *uuid.UUID
	// PostID is the shared post on a "post" card (TikTok parity T-25).
	PostID *uuid.UUID
	// Extra is the rich-message payload (chat parity §2) as JSON; nil for a
	// plain text / media / post message.
	Extra       []byte
	CompletedAt *time.Time
}

// sealIntent returns the stored forms of an intent's content columns: the
// message text, the quote snapshot and the rich payload, each bound to the
// message.
func (s *ConversationStore) sealIntent(ctx context.Context, intent MessageDeliveryIntent) (text, replyPreview string, extra []byte, err error) {
	row := contentcrypt.MessageRow(intent.ConversationID, intent.MessageID)
	if text, err = s.sealText(ctx, contentcrypt.ScopeIntentText, row, intent.MessageText); err != nil {
		return "", "", nil, err
	}
	if replyPreview, err = s.sealText(ctx, contentcrypt.ScopeIntentReplyPreview, row, intent.ReplyToPreview); err != nil {
		return "", "", nil, err
	}
	if extra, err = s.sealJSON(ctx, contentcrypt.ScopeIntentExtra, row, intent.Extra); err != nil {
		return "", "", nil, err
	}
	return text, replyPreview, extra, nil
}

// openIntent opens a scanned intent's content columns in place. They are
// authoritative (repair replays them into Scylla), so a failure fails the
// read rather than replaying ciphertext or an empty message.
func (s *ConversationStore) openIntent(ctx context.Context, intent *MessageDeliveryIntent) error {
	row := contentcrypt.MessageRow(intent.ConversationID, intent.MessageID)
	var err error
	if intent.MessageText, err = s.openText(ctx, contentcrypt.ScopeIntentText, row, intent.MessageText); err != nil {
		return err
	}
	if intent.ReplyToPreview, err = s.openText(ctx, contentcrypt.ScopeIntentReplyPreview, row, intent.ReplyToPreview); err != nil {
		return err
	}
	if intent.Extra, err = s.openJSON(ctx, contentcrypt.ScopeIntentExtra, row, intent.Extra); err != nil {
		return err
	}
	return nil
}

func (s *ConversationStore) ReserveMessageDeliveryIntent(ctx context.Context, intent MessageDeliveryIntent) (*MessageDeliveryIntent, error) {
	text, replyPreview, extra, err := s.sealIntent(ctx, intent)
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO chat.message_delivery_intents (
			idempotency_key, request_hash, conversation_id, sender_id,
			message_id, bucket, message_ts, message_type, message_text,
			media_id, member_ids, first_request_message,
			request_receiver_id, source_app, match_id,
			reply_to_id, reply_to_preview, reply_to_sender_id, post_id, extra
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
		ON CONFLICT (idempotency_key) DO NOTHING
	`, intent.IdempotencyKey, intent.RequestHash, intent.ConversationID, intent.SenderID,
		intent.MessageID, intent.Bucket, intent.MessageTS, intent.MessageType, text,
		intent.MediaID, intent.MemberIDs, intent.FirstRequest, intent.RequestReceiverID,
		intent.SourceApp, intent.MatchID, intent.ReplyToID, replyPreview, intent.ReplyToSenderID,
		intent.PostID, extra)
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
		       request_receiver_id, source_app, match_id,
		       reply_to_id, reply_to_preview, reply_to_sender_id, post_id, extra, completed_at
		FROM chat.message_delivery_intents WHERE idempotency_key = $1
	`, key).Scan(&intent.IdempotencyKey, &intent.RequestHash, &intent.ConversationID,
		&intent.SenderID, &intent.MessageID, &intent.Bucket, &intent.MessageTS,
		&intent.MessageType, &intent.MessageText, &intent.MediaID, &intent.MemberIDs,
		&intent.FirstRequest, &intent.RequestReceiverID, &intent.SourceApp,
		&intent.MatchID, &intent.ReplyToID, &intent.ReplyToPreview, &intent.ReplyToSenderID,
		&intent.PostID, &intent.Extra, &intent.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := s.openIntent(ctx, &intent); err != nil {
		return nil, err
	}
	return &intent, nil
}

func (s *ConversationStore) FetchPendingMessageDeliveryIntents(ctx context.Context, limit int) ([]MessageDeliveryIntent, error) {
	rows, err := s.db.Query(ctx, `
		SELECT idempotency_key, request_hash, conversation_id, sender_id,
		       message_id, bucket, message_ts, message_type, message_text,
		       media_id, member_ids, first_request_message,
		       request_receiver_id, source_app, match_id,
		       reply_to_id, reply_to_preview, reply_to_sender_id, post_id, extra, completed_at
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
			&intent.MatchID, &intent.ReplyToID, &intent.ReplyToPreview, &intent.ReplyToSenderID,
			&intent.PostID, &intent.Extra, &intent.CompletedAt); err != nil {
			return nil, err
		}
		if err := s.openIntent(ctx, &intent); err != nil {
			// Logged by message id only; the repair worker retries the
			// batch, and an intent that never opens is an operator alarm.
			s.logger().Error("field encryption: delivery intent did not open; skipped", "message_id", intent.MessageID, "err", err)
			continue
		}
		intents = append(intents, intent)
	}
	return intents, rows.Err()
}

// CompleteMessageDeliveryIntent marks the cross-store write done and drops
// the content it carried (retention, 22 Oct 2026): once Scylla, the inbox
// projections and the outbox hold the message, the intent's copy of the text,
// quote and rich payload is only a second place to leak it from. The ids,
// bucket, timestamp and member list stay — they are what repair, unsend and
// the disappearing-message worker key on. A completed intent is never
// replayed (the send path returns early on one), so nothing reads the blanked
// columns back.
func (s *ConversationStore) CompleteMessageDeliveryIntent(ctx context.Context, key string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.message_delivery_intents
		SET completed_at = COALESCE(completed_at, NOW()),
		    message_text = '', reply_to_preview = '', extra = NULL
		WHERE idempotency_key = $1
	`, key)
	return err
}

func (s *ConversationStore) InsertOutboxEventOnce(ctx context.Context, dedupeKey, eventType string, payload interface{}) error {
	data, err := s.sealOutboxPayload(ctx, eventType, dedupeKey, payload)
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

// ViewerMayAccessChatMedia: the media rides a message in a conversation the
// viewer is an active member of and has NOT hidden (hidden chats: media in a
// locked conversation needs an open token — ViewerMayAccessLockedChatMedia),
// or it is an active catalogue sticker.
func (s *ConversationStore) ViewerMayAccessChatMedia(ctx context.Context, viewerID, mediaID uuid.UUID) (bool, error) {
	var allowed bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM chat.message_media_references r
			JOIN chat.conversation_members m ON m.conversation_id = r.conversation_id
			WHERE r.media_id = $1 AND m.user_id = $2 AND m.left_at IS NULL AND `+memberNotLocked+`
		) OR EXISTS (
			-- Chat parity §2.7: an active catalogue sticker is deliverable
			-- to any signed-in viewer (the catalogue is the same for all).
			SELECT 1 FROM chat.stickers st
			JOIN chat.sticker_packs p ON p.id = st.pack_id
			WHERE st.media_id = $1 AND st.active AND p.active
		)
	`, mediaID, viewerID).Scan(&allowed)
	return allowed, err
}
