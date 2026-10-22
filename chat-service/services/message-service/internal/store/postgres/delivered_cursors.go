package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Delivered ticks (founder, 22 Oct 2026; migration 017).
//
// A member's delivered cursor is the server ts of the newest message their
// device has acknowledged receiving. It is the delivered twin of
// chat.read_cursors: one row per member per conversation, never one row per
// message, so a sender's tick is a comparison against cursors.

// DeliveredCursor is a member's durable delivered watermark for one
// conversation.
type DeliveredCursor struct {
	ConversationID         uuid.UUID `json:"conversation_id"`
	UserID                 uuid.UUID `json:"user_id"`
	LastDeliveredMessageID uuid.UUID `json:"last_delivered_message_id"`
	LastDeliveredAt        time.Time `json:"last_delivered_at"`
}

// UpsertDeliveredCursor advances the member's delivered watermark.
// Monotonic: a replayed or out-of-order ack never moves it backwards.
// advanced reports whether the stored cursor moved (a no-op ack sends no
// frame).
func (s *ConversationStore) UpsertDeliveredCursor(ctx context.Context, conversationID, userID uuid.UUID, messageID *uuid.UUID, deliveredAt time.Time) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		INSERT INTO chat.delivered_cursors (conversation_id, user_id, last_delivered_message_id, last_delivered_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (conversation_id, user_id) DO UPDATE
			SET last_delivered_message_id = EXCLUDED.last_delivered_message_id,
			    last_delivered_at = EXCLUDED.last_delivered_at,
			    updated_at = NOW()
			WHERE chat.delivered_cursors.last_delivered_at < EXCLUDED.last_delivered_at
	`, conversationID, userID, messageID, deliveredAt)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// GetConversationDeliveredCursors returns every member's delivered watermark
// for ONE conversation, keyed by user id.
func (s *ConversationStore) GetConversationDeliveredCursors(ctx context.Context, conversationID uuid.UUID) (map[uuid.UUID]DeliveredCursor, error) {
	out := make(map[uuid.UUID]DeliveredCursor)
	rows, err := s.db.Query(ctx, `
		SELECT conversation_id, user_id,
		       COALESCE(last_delivered_message_id, '00000000-0000-0000-0000-000000000000'::uuid),
		       last_delivered_at
		FROM chat.delivered_cursors
		WHERE conversation_id = $1
	`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var dc DeliveredCursor
		if err := rows.Scan(&dc.ConversationID, &dc.UserID, &dc.LastDeliveredMessageID, &dc.LastDeliveredAt); err != nil {
			return nil, err
		}
		out[dc.UserID] = dc
	}
	return out, rows.Err()
}

// GetOtherMembersDeliveredCursors returns every OTHER member's delivered
// cursor for a batch of conversations (the inbox's own-message status).
func (s *ConversationStore) GetOtherMembersDeliveredCursors(ctx context.Context, viewerID uuid.UUID, convIDs []uuid.UUID) (map[uuid.UUID]map[uuid.UUID]DeliveredCursor, error) {
	out := map[uuid.UUID]map[uuid.UUID]DeliveredCursor{}
	if len(convIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT conversation_id, user_id,
		       COALESCE(last_delivered_message_id, '00000000-0000-0000-0000-000000000000'::uuid),
		       last_delivered_at
		FROM chat.delivered_cursors
		WHERE conversation_id = ANY($1) AND user_id <> $2
	`, convIDs, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var dc DeliveredCursor
		if err := rows.Scan(&dc.ConversationID, &dc.UserID, &dc.LastDeliveredMessageID, &dc.LastDeliveredAt); err != nil {
			return nil, err
		}
		if out[dc.ConversationID] == nil {
			out[dc.ConversationID] = map[uuid.UUID]DeliveredCursor{}
		}
		out[dc.ConversationID][dc.UserID] = dc
	}
	return out, rows.Err()
}

// GetUserPolicies batch-reads projected policy rows (the tick aggregate over
// a whole group needs every member's receipt setting in one query). Users
// with no row are simply absent; the caller resolves those one by one.
func (s *ConversationStore) GetUserPolicies(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]UserPolicy, error) {
	out := make(map[uuid.UUID]UserPolicy, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT user_id, chat_paused, send_typing_indicators, read_receipts_visibility, privacy_version, refreshed_at,
		       dm_safe_mode, dm_sensitive_media_filter, dm_filtered_keywords, chat_bubble_style
		FROM chat.user_policy WHERE user_id = ANY($1)
	`, userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p UserPolicy
		if err := rows.Scan(&p.UserID, &p.ChatPaused, &p.SendTypingIndicators, &p.ReadReceiptsVisibility, &p.PrivacyVersion, &p.RefreshedAt,
			&p.DMSafeMode, &p.DMSensitiveMediaFilter, &p.DMFilteredKeywords, &p.ChatBubbleStyle); err != nil {
			return nil, err
		}
		out[p.UserID] = p
	}
	return out, rows.Err()
}

// GetReadCursor returns one member's read cursor, or nil when they have
// none. MarkRead reads it before advancing so a group's aggregate read
// frame goes only to the members whose "everyone has read" watermark moved.
func (s *ConversationStore) GetReadCursor(ctx context.Context, conversationID, userID uuid.UUID) (*ReadCursor, error) {
	var rc ReadCursor
	err := s.db.QueryRow(ctx, `
		SELECT conversation_id, user_id, COALESCE(last_read_message_id, '00000000-0000-0000-0000-000000000000'::uuid), last_read_at
		FROM chat.read_cursors
		WHERE conversation_id = $1 AND user_id = $2
	`, conversationID, userID).Scan(&rc.ConversationID, &rc.UserID, &rc.LastReadMessageID, &rc.LastReadAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &rc, nil
}
