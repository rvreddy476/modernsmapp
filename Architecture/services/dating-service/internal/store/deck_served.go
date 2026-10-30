package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// DeckServedWindow is how long a dealt card stays actionable by id: the deck
// allowance's own window.
const DeckServedWindow = DeckQuotaWindow

// RecordDeckServed notes the cards GET /pulse/today just returned to the
// viewer (audit H1). It is the durable answer to "was this card served",
// independent of the Redis deck cache, so a served card stays passable after
// the cached batch expires and a card never served is never actionable by id.
// Re-serving a card refreshes its time; rows past the window are pruned for
// this viewer on the way.
func (s *Store) RecordDeckServed(ctx context.Context, userID uuid.UUID, candidateIDs []uuid.UUID) error {
	if userID == uuid.Nil || len(candidateIDs) == 0 {
		return nil
	}
	if _, err := s.db.Exec(ctx, `
        INSERT INTO dating_deck_served (user_id, candidate_id, served_at)
        SELECT $1, c, now() FROM unnest($2::uuid[]) AS c
        ON CONFLICT (user_id, candidate_id) DO UPDATE SET served_at = EXCLUDED.served_at`,
		userID, candidateIDs); err != nil {
		return fmt.Errorf("record deck served: %w", err)
	}
	if _, err := s.db.Exec(ctx, `
        DELETE FROM dating_deck_served
        WHERE user_id = $1 AND served_at <= now() - make_interval(secs => $2)`,
		userID, DeckServedWindow.Seconds()); err != nil {
		return fmt.Errorf("prune deck served: %w", err)
	}
	return nil
}

// WasDealt reports whether candidateID was returned in the viewer's deck
// inside DeckServedWindow.
func (s *Store) WasDealt(ctx context.Context, userID, candidateID uuid.UUID) (bool, error) {
	var dealt bool
	err := s.db.QueryRow(ctx, `
        SELECT EXISTS (SELECT 1 FROM dating_deck_served
                       WHERE user_id = $1 AND candidate_id = $2
                         AND served_at > now() - make_interval(secs => $3))`,
		userID, candidateID, DeckServedWindow.Seconds()).Scan(&dealt)
	if err != nil {
		return false, fmt.Errorf("deck served lookup: %w", err)
	}
	return dealt, nil
}
