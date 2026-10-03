// Deck store — mechanic M1, the refilling swipe deck.
//
// dating_deck_ledger holds one row per deck card the user acted on. The daily
// card allowance counts the rows inside DeckQuotaWindow; nothing else reads
// it. actedOnPredicate is the "never show a card twice" half: it is evaluated
// against dating_sparks and dating_matches, the records of the action itself,
// so it holds whether or not a ledger row was written.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DeckQuotaWindow is the rolling window the daily card allowance counts.
const DeckQuotaWindow = 24 * time.Hour

// Deck ledger actions.
const (
	DeckActionSpark      = "spark"
	DeckActionPass       = "pass"
	DeckActionSuperSpark = "super_spark"
)

// actedOnPredicate is true when viewer has a live spark toward candidate (one
// newer than the pair's last closed or expired match, so an unmatched pair
// can meet again) or the pair holds an open match.
func actedOnPredicate(viewer, candidate string) string {
	return `(EXISTS (SELECT 1 FROM dating_sparks ao
            WHERE ao.from_user_id = ` + viewer + ` AND ao.to_user_id = ` + candidate + `
              AND ao.created_at > ` + pairLastClosedSQL(viewer, candidate) + `)
        OR EXISTS (SELECT 1 FROM dating_matches om
            WHERE om.user_a = LEAST(` + viewer + `, ` + candidate + `)
              AND om.user_b = GREATEST(` + viewer + `, ` + candidate + `)
              AND om.status IN ` + openMatchStatuses + `))`
}

// RecordDeckAction writes the ledger row for a deck card the user acted on.
// A repeat on the same candidate inside the window is one card, not two.
func (s *Store) RecordDeckAction(ctx context.Context, userID, candidateID uuid.UUID, action string) error {
	if userID == uuid.Nil || candidateID == uuid.Nil {
		return fmt.Errorf("invalid: user_id and candidate_id required")
	}
	_, err := s.db.Exec(ctx, `
        INSERT INTO dating_deck_ledger (user_id, candidate_id, action)
        SELECT $1, $2, $3
        WHERE NOT EXISTS (SELECT 1 FROM dating_deck_ledger
            WHERE user_id = $1 AND candidate_id = $2
              AND acted_at > now() - make_interval(secs => $4))`,
		userID, candidateID, action, DeckQuotaWindow.Seconds())
	if err != nil {
		return fmt.Errorf("record deck action: %w", err)
	}
	return nil
}

// DeckUsage returns how many deck cards the user acted on inside the window
// and when the oldest of them was (nil when none), on the database clock.
func (s *Store) DeckUsage(ctx context.Context, userID uuid.UUID) (int, *time.Time, error) {
	var used int
	var oldest *time.Time
	err := s.db.QueryRow(ctx, `
        SELECT COUNT(*)::int, min(acted_at) FROM dating_deck_ledger
        WHERE user_id = $1 AND acted_at > now() - make_interval(secs => $2)`,
		userID, DeckQuotaWindow.Seconds()).Scan(&used, &oldest)
	if err != nil {
		return 0, nil, fmt.Errorf("deck usage: %w", err)
	}
	return used, oldest, nil
}
