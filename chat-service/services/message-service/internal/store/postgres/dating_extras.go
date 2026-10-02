package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Dating mechanic M9 — in-match extras. dating-service sets two rules on a
// dating conversation when it creates it:
//
//   - dating_receipts_gated: read receipts reach a member only while that
//     member's dating_receipts_until is in the future (dating sets it to the
//     member's pass expiry while they opt in, NULL otherwise);
//   - dating_call_after_exchange: the open-match answer graph-service grants
//     calls on also needs both members to have sent a message
//     (conversation_members.first_sent_at, stamped on delivery).
//
// The rules only ever switch on, so a retry never loosens them.

// SetDatingConversationRules switches the M9 rules on for a conversation.
func (s *ConversationStore) SetDatingConversationRules(ctx context.Context, conversationID uuid.UUID, receiptsGated, callAfterExchange bool) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.conversations
		SET dating_receipts_gated = dating_receipts_gated OR $2,
		    dating_call_after_exchange = dating_call_after_exchange OR $3
		WHERE id = $1 AND source_app = 'dating'
	`, conversationID, receiptsGated, callAfterExchange)
	return err
}

// MarkMemberSent stamps a member's first delivered message in a dating
// conversation (idempotent).
func (s *ConversationStore) MarkMemberSent(ctx context.Context, conversationID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.conversation_members SET first_sent_at = now()
		WHERE conversation_id = $1 AND user_id = $2 AND first_sent_at IS NULL
	`, conversationID, userID)
	return err
}

// SetDatingReceiptsUntil sets until when a member of a match's conversation
// may see the other's read receipts (nil: not at all). Returns whether a
// member row was found.
func (s *ConversationStore) SetDatingReceiptsUntil(ctx context.Context, matchID, userID uuid.UUID, until *time.Time) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE chat.conversation_members m SET dating_receipts_until = $3
		FROM chat.conversations c
		WHERE c.id = m.conversation_id AND c.source_app = 'dating' AND c.match_id = $1 AND m.user_id = $2
	`, matchID, userID, until)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// DatingReceiptsAllowed reports whether viewerID may see read receipts in a
// conversation: always outside a gated dating conversation, otherwise only
// while their dating_receipts_until is in the future.
func (s *ConversationStore) DatingReceiptsAllowed(ctx context.Context, conversationID, viewerID uuid.UUID) (bool, error) {
	var allowed bool
	err := s.db.QueryRow(ctx, `
		SELECT NOT c.dating_receipts_gated
		    OR COALESCE((SELECT m.dating_receipts_until > now() FROM chat.conversation_members m
		                 WHERE m.conversation_id = c.id AND m.user_id = $2), false)
		FROM chat.conversations c WHERE c.id = $1
	`, conversationID, viewerID).Scan(&allowed)
	if err != nil {
		return false, err
	}
	return allowed, nil
}
