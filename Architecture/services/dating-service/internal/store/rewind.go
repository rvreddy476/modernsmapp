// Rewind store — mechanic M2: undo the caller's last pass.
//
// A rewind never deletes the pass. It stamps dating_passes.rewound_at, which
// takes the row out of the pass cooldown (and out of Nebula); passing the
// same person again re-arms it. dating_rewind_ledger is the allowance record,
// separate from the pass row on purpose: re-passing clears rewound_at, so
// counting on the pass row would let a pass / rewind / pass loop run free.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RewindQuotaWindow is the rolling window the free rewind allowance counts.
const RewindQuotaWindow = 24 * time.Hour

var (
	// ErrNothingToRewind: the caller's last deck action is not a pass that
	// can be undone (no pass, a spark came after it, or it was passed before
	// their last rewind — a rewind is one step, never a chain).
	ErrNothingToRewind = errors.New("conflict: nothing to rewind")
	// ErrRewindLimited: the free rewind allowance is used up.
	ErrRewindLimited = errors.New("rate_limited: rewind limit reached")
)

// rewindablePassSQL selects the one pass a rewind may undo: the user's most
// recent live pass, and only when no spark of theirs and no rewind of theirs
// is newer than it. $1 is the user.
const rewindablePassSQL = `
    SELECT dp.candidate_id FROM dating_passes dp
    WHERE dp.user_id = $1
      AND dp.rewound_at IS NULL
      AND dp.passed_at = (SELECT max(lp.passed_at) FROM dating_passes lp
                          WHERE lp.user_id = $1 AND lp.rewound_at IS NULL)
      AND dp.passed_at > COALESCE((SELECT max(sp.created_at) FROM dating_sparks sp
                                   WHERE sp.from_user_id = $1), '-infinity'::timestamptz)
      AND dp.passed_at > COALESCE((SELECT max(rl.rewound_at) FROM dating_rewind_ledger rl
                                   WHERE rl.user_id = $1), '-infinity'::timestamptz)
    LIMIT 1`

// LastRewindablePass returns the candidate whose pass a rewind would undo, or
// ErrNothingToRewind.
func (s *Store) LastRewindablePass(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	var candidate uuid.UUID
	err := s.db.QueryRow(ctx, rewindablePassSQL, userID).Scan(&candidate)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNothingToRewind
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("last rewindable pass: %w", err)
	}
	return candidate, nil
}

// RewindUsage returns how many rewinds the user made inside the window and
// when the oldest of them was (nil when none), on the database clock.
func (s *Store) RewindUsage(ctx context.Context, userID uuid.UUID) (int, *time.Time, error) {
	var used int
	var oldest *time.Time
	err := s.db.QueryRow(ctx, `
        SELECT COUNT(*)::int, min(rewound_at) FROM dating_rewind_ledger
        WHERE user_id = $1 AND rewound_at > now() - make_interval(secs => $2)`,
		userID, RewindQuotaWindow.Seconds()).Scan(&used, &oldest)
	if err != nil {
		return 0, nil, fmt.Errorf("rewind usage: %w", err)
	}
	return used, oldest, nil
}

// RewindPass undoes the user's pass on candidateID, which must still be the
// rewindable pass (ErrNothingToRewind otherwise). limit is the allowance per
// RewindQuotaWindow (ErrRewindLimited at or above it); limit <= 0 means the
// caller holds a pass and is not limited. The check, the ledger row and the
// undo commit together under a per-user lock. The card also goes back into
// the deck allowance (its dating_deck_ledger row is removed).
func (s *Store) RewindPass(ctx context.Context, userID, candidateID uuid.UUID, limit int) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin rewind: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 7022))`, userID.String()); err != nil {
		return fmt.Errorf("lock rewind: %w", err)
	}
	var current uuid.UUID
	err = tx.QueryRow(ctx, rewindablePassSQL, userID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && current != candidateID) {
		return ErrNothingToRewind
	}
	if err != nil {
		return fmt.Errorf("recheck rewindable pass: %w", err)
	}
	if limit > 0 {
		var used int
		if err := tx.QueryRow(ctx, `
            SELECT COUNT(*)::int FROM dating_rewind_ledger
            WHERE user_id = $1 AND rewound_at > now() - make_interval(secs => $2)`,
			userID, RewindQuotaWindow.Seconds()).Scan(&used); err != nil {
			return fmt.Errorf("count rewinds: %w", err)
		}
		if used >= limit {
			return ErrRewindLimited
		}
	}
	if _, err := tx.Exec(ctx, `
        UPDATE dating_passes SET rewound_at = now()
        WHERE user_id = $1 AND candidate_id = $2 AND rewound_at IS NULL`, userID, candidateID); err != nil {
		return fmt.Errorf("rewind pass: %w", err)
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO dating_rewind_ledger (user_id, candidate_id) VALUES ($1, $2)`, userID, candidateID); err != nil {
		return fmt.Errorf("record rewind: %w", err)
	}
	if _, err := tx.Exec(ctx, `
        DELETE FROM dating_deck_ledger
        WHERE user_id = $1 AND candidate_id = $2 AND action = $3`, userID, candidateID, DeckActionPass); err != nil {
		return fmt.Errorf("return deck card: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit rewind: %w", err)
	}
	return nil
}
