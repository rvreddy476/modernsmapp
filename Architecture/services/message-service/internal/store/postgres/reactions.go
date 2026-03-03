package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MessageReaction represents a reaction on a chat message.
type MessageReaction struct {
	ID           uuid.UUID `json:"id"`
	MessageID    string    `json:"message_id"`
	UserID       uuid.UUID `json:"user_id"`
	ReactionType string    `json:"reaction_type"`
	CreatedAt    time.Time `json:"created_at"`
}

// ReactionSummary groups reaction counts by type for a message.
type ReactionSummary struct {
	ReactionType string   `json:"reaction_type"`
	Count        int      `json:"count"`
	UserIDs      []string `json:"user_ids"` // limited to first 3 for preview
}

// AddReaction adds or replaces a user's reaction to a message.
// v2.1: one reaction per user per message (UNIQUE constraint).
func AddReaction(ctx context.Context, db *pgxpool.Pool, messageID string, userID uuid.UUID, reactionType string) (*MessageReaction, error) {
	var r MessageReaction
	err := db.QueryRow(ctx,
		`INSERT INTO chat.message_reactions (message_id, user_id, reaction_type)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (message_id, user_id)
		 DO UPDATE SET reaction_type = EXCLUDED.reaction_type, created_at = NOW()
		 RETURNING id, message_id, user_id, reaction_type, created_at`,
		messageID, userID, reactionType,
	).Scan(&r.ID, &r.MessageID, &r.UserID, &r.ReactionType, &r.CreatedAt)
	return &r, err
}

// RemoveReaction removes a user's reaction from a message.
func RemoveReaction(ctx context.Context, db *pgxpool.Pool, messageID string, userID uuid.UUID) error {
	_, err := db.Exec(ctx,
		`DELETE FROM chat.message_reactions WHERE message_id = $1 AND user_id = $2`,
		messageID, userID,
	)
	return err
}

// GetReactions returns reaction summaries for a message.
func GetReactions(ctx context.Context, db *pgxpool.Pool, messageID string) ([]ReactionSummary, error) {
	rows, err := db.Query(ctx,
		`SELECT reaction_type,
		        COUNT(*) AS count,
		        ARRAY_AGG(user_id::text ORDER BY created_at ASC) AS user_ids
		 FROM chat.message_reactions
		 WHERE message_id = $1
		 GROUP BY reaction_type
		 ORDER BY count DESC`,
		messageID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var summaries []ReactionSummary
	for rows.Next() {
		var s ReactionSummary
		var allUserIDs []string
		if err := rows.Scan(&s.ReactionType, &s.Count, &allUserIDs); err != nil {
			return nil, err
		}
		// Return only first 3 user IDs for preview
		if len(allUserIDs) > 3 {
			s.UserIDs = allUserIDs[:3]
		} else {
			s.UserIDs = allUserIDs
		}
		summaries = append(summaries, s)
	}
	return summaries, rows.Err()
}

// GetUserReaction returns the current user's reaction to a message, or "" if none.
func GetUserReaction(ctx context.Context, db *pgxpool.Pool, messageID string, userID uuid.UUID) (string, error) {
	var reactionType string
	err := db.QueryRow(ctx,
		`SELECT reaction_type FROM chat.message_reactions WHERE message_id = $1 AND user_id = $2`,
		messageID, userID,
	).Scan(&reactionType)
	if err != nil {
		// No reaction found is not an error
		return "", nil
	}
	return reactionType, nil
}
