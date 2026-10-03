package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrDatingConversationNotFound: no dating conversation for that match id.
var ErrDatingConversationNotFound = errors.New("dating conversation not found")

// SetDatingFirstMovers records who may send the first message in a dating
// conversation (dating mechanic M5). It writes only while the conversation
// has no message yet, so a retry after the first message never re-closes it.
// An empty list clears the rule.
func (s *ConversationStore) SetDatingFirstMovers(ctx context.Context, conversationID uuid.UUID, movers []uuid.UUID) error {
	var value any
	if len(movers) > 0 {
		value = movers
	}
	_, err := s.db.Exec(ctx, `
		UPDATE chat.conversations
		SET dating_first_movers = $2
		WHERE id = $1 AND source_app = 'dating' AND last_message_at IS NULL
	`, conversationID, value)
	return err
}

// DatingConversationByMatch returns the dating conversation for a match id.
func (s *ConversationStore) DatingConversationByMatch(ctx context.Context, matchID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.db.QueryRow(ctx, `
		SELECT id FROM chat.conversations
		WHERE source_app = 'dating' AND match_id = $1
		LIMIT 1`, matchID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrDatingConversationNotFound
	}
	return id, err
}
