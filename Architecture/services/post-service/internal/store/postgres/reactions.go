package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Reaction represents a user's reaction to a target (post, video, comment, story).
type Reaction struct {
	ID           uuid.UUID `json:"id"`
	TargetType   string    `json:"target_type"`
	TargetID     uuid.UUID `json:"target_id"`
	UserID       uuid.UUID `json:"user_id"`
	ReactionType string    `json:"reaction_type"`
	CreatedAt    time.Time `json:"created_at"`
}

// ReactionCounts holds counts by reaction type for a target.
type ReactionCounts struct {
	Like  int64 `json:"like"`
	Love  int64 `json:"love"`
	Haha  int64 `json:"haha"`
	Wow   int64 `json:"wow"`
	Sad   int64 `json:"sad"`
	Angry int64 `json:"angry"`
	Total int64 `json:"total"`
}

// ValidReactionTypes lists the allowed reaction type values.
var ValidReactionTypes = map[string]bool{
	"like": true, "love": true, "haha": true,
	"wow": true, "sad": true, "angry": true,
}

// ToggleReaction inserts or updates a user's reaction on a target.
// If the user already reacted with the same type, it removes the reaction (toggle off).
// If the user already reacted with a different type, it updates to the new type.
// Returns the new reaction type ("" if removed) and whether it was set.
func (s *Store) ToggleReaction(ctx context.Context, targetType string, targetID, userID uuid.UUID, reactionType string) (string, bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback(ctx)

	// Check existing reaction
	var existingType string
	err = tx.QueryRow(ctx, `
		SELECT reaction_type FROM reactions
		WHERE target_type = $1 AND target_id = $2 AND user_id = $3
	`, targetType, targetID, userID).Scan(&existingType)

	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", false, err
	}

	if existingType == reactionType {
		// Same type — toggle off (remove)
		_, err = tx.Exec(ctx, `
			DELETE FROM reactions
			WHERE target_type = $1 AND target_id = $2 AND user_id = $3
		`, targetType, targetID, userID)
		if err != nil {
			return "", false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", false, err
		}
		return "", false, nil
	}

	if existingType != "" {
		// Different type — update
		_, err = tx.Exec(ctx, `
			UPDATE reactions SET reaction_type = $4, created_at = $5
			WHERE target_type = $1 AND target_id = $2 AND user_id = $3
		`, targetType, targetID, userID, reactionType, time.Now())
	} else {
		// No existing — insert
		_, err = tx.Exec(ctx, `
			INSERT INTO reactions (id, target_type, target_id, user_id, reaction_type, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, uuid.New(), targetType, targetID, userID, reactionType, time.Now())
	}
	if err != nil {
		return "", false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", false, err
	}
	return reactionType, true, nil
}

// GetReactionCounts returns reaction counts by type for a target.
func (s *Store) GetReactionCounts(ctx context.Context, targetType string, targetID uuid.UUID) (*ReactionCounts, error) {
	rows, err := s.db.Query(ctx, `
		SELECT reaction_type, COUNT(*) FROM reactions
		WHERE target_type = $1 AND target_id = $2
		GROUP BY reaction_type
	`, targetType, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := &ReactionCounts{}
	for rows.Next() {
		var rtype string
		var count int64
		if err := rows.Scan(&rtype, &count); err != nil {
			return nil, err
		}
		counts.Total += count
		switch rtype {
		case "like":
			counts.Like = count
		case "love":
			counts.Love = count
		case "haha":
			counts.Haha = count
		case "wow":
			counts.Wow = count
		case "sad":
			counts.Sad = count
		case "angry":
			counts.Angry = count
		}
	}

	return counts, rows.Err()
}

// GetUserReaction returns the user's current reaction type on a target ("" if none).
func (s *Store) GetUserReaction(ctx context.Context, targetType string, targetID, userID uuid.UUID) (string, error) {
	var reactionType string
	err := s.db.QueryRow(ctx, `
		SELECT reaction_type FROM reactions
		WHERE target_type = $1 AND target_id = $2 AND user_id = $3
	`, targetType, targetID, userID).Scan(&reactionType)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return reactionType, err
}
