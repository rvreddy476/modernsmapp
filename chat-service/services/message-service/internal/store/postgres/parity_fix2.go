package postgres

import (
	"context"

	"github.com/google/uuid"
)

// Chat parity fix pass 2 (lane M, 2026-10-18).

// RequestSenderIDs returns who sent the message request behind each of the
// given request conversations: the message_requests row's sender, or — when
// that row is gone (the sender's account was purged, which deletes it) —
// the conversation's creator. Conversations with neither are absent.
func (s *ConversationStore) RequestSenderIDs(ctx context.Context, convIDs []uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
	out := make(map[uuid.UUID]uuid.UUID, len(convIDs))
	if len(convIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT c.id, COALESCE(mr.sender_id, c.created_by)
		FROM chat.conversations c
		LEFT JOIN chat.message_requests mr ON mr.conversation_id = c.id
		WHERE c.id = ANY($1::uuid[]) AND COALESCE(mr.sender_id, c.created_by) IS NOT NULL
	`, convIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var convID, sender uuid.UUID
		if err := rows.Scan(&convID, &sender); err != nil {
			return nil, err
		}
		out[convID] = sender
	}
	return out, rows.Err()
}
