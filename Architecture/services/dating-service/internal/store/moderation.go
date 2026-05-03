// Moderation store — dating_moderation_results table.
//
// Shadow mode contract: action_taken='shadow' regardless of confidence
// when the strict feature flag is off. Strict mode allows
// 'warn'|'block'|'held'. Idempotent on (message_id, layer).
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ModerationResult is a row of dating_moderation_results.
type ModerationResult struct {
	ID             uuid.UUID `json:"id"`
	MessageID      uuid.UUID `json:"message_id"`
	ConversationID uuid.UUID `json:"conversation_id"`
	Layer          int       `json:"layer"`
	Confidence     float64   `json:"confidence"`
	Patterns       []string  `json:"patterns"`
	ActionTaken    string    `json:"action_taken"`
	CreatedAt      time.Time `json:"created_at"`
}

// ErrModerationResultNotFound is returned when a row does not exist.
var ErrModerationResultNotFound = errors.New("not_found: moderation result not found")

// RecordModerationResult upserts a moderation outcome on (message_id, layer).
// Re-running with the same key updates the row in place.
func (s *Store) RecordModerationResult(ctx context.Context, r ModerationResult) error {
	if r.MessageID == uuid.Nil {
		return fmt.Errorf("invalid: message_id required")
	}
	if r.ConversationID == uuid.Nil {
		return fmt.Errorf("invalid: conversation_id required")
	}
	if r.Layer != 1 && r.Layer != 2 {
		return fmt.Errorf("invalid: layer must be 1 or 2")
	}
	if r.ActionTaken == "" {
		r.ActionTaken = "shadow"
	}
	switch r.ActionTaken {
	case "shadow", "warn", "block", "held":
	default:
		return fmt.Errorf("invalid: action_taken must be shadow|warn|block|held")
	}
	if r.Patterns == nil {
		r.Patterns = []string{}
	}
	_, err := s.db.Exec(ctx, `
        INSERT INTO dating_moderation_results
            (message_id, conversation_id, layer, confidence, patterns, action_taken)
        VALUES ($1, $2, $3, $4, $5, $6)
        ON CONFLICT (message_id, layer) DO UPDATE
            SET confidence    = EXCLUDED.confidence,
                patterns      = EXCLUDED.patterns,
                action_taken  = EXCLUDED.action_taken`,
		r.MessageID, r.ConversationID, r.Layer, r.Confidence, r.Patterns, r.ActionTaken)
	if err != nil {
		return fmt.Errorf("record moderation result: %w", err)
	}
	return nil
}

// GetModerationResult returns the row for (message_id, layer).
func (s *Store) GetModerationResult(ctx context.Context, messageID uuid.UUID, layer int) (*ModerationResult, error) {
	row := s.db.QueryRow(ctx, `
        SELECT id, message_id, conversation_id, layer, confidence, patterns, action_taken, created_at
        FROM dating_moderation_results
        WHERE message_id = $1 AND layer = $2`, messageID, layer)
	r := &ModerationResult{}
	if err := row.Scan(
		&r.ID, &r.MessageID, &r.ConversationID, &r.Layer, &r.Confidence, &r.Patterns, &r.ActionTaken, &r.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrModerationResultNotFound
		}
		return nil, fmt.Errorf("scan moderation result: %w", err)
	}
	return r, nil
}
