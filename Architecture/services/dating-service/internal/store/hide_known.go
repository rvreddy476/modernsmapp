// Hide from people I know — mechanic M16 (DATING_HIDE_KNOWN_ENABLED).
//
// dating_hide_known holds who has the setting on; dating_known_people is a
// snapshot of each such user's accepted Momentum connections, refreshed
// daily. While the mechanic is on, FetchCandidates keeps a pair apart when
// either of them has the setting on and lists the other: they never see
// each other in a deck or in picks. Matches and sparks made before are not
// touched. Purged with the profile.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SetHideKnownEnabled switches the effect of the setting on discovery.
func (s *Store) SetHideKnownEnabled(on bool) { s.hideKnownEnabled = on }

// hideKnownPredicate is true when either of the pair (viewer, candidate)
// lists the other in their snapshot. A snapshot exists only while its owner
// has the setting on: DisableHideKnown drops it.
func hideKnownPredicate(viewer, candidate string) string {
	return `EXISTS (SELECT 1 FROM dating_known_people k
        WHERE (k.user_id = ` + viewer + ` AND k.other_id = ` + candidate + `)
           OR (k.user_id = ` + candidate + ` AND k.other_id = ` + viewer + `))`
}

// ReplaceKnownPeople turns the setting on for userID with this snapshot of
// their connections, replacing any earlier one.
func (s *Store) ReplaceKnownPeople(ctx context.Context, userID uuid.UUID, others []uuid.UUID) error {
	if others == nil {
		others = []uuid.UUID{}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin known people: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
        INSERT INTO dating_hide_known (user_id, enabled, connections, refreshed_at, updated_at)
        VALUES ($1, true, $2, now(), now())
        ON CONFLICT (user_id) DO UPDATE
        SET enabled = true, connections = EXCLUDED.connections, refreshed_at = now(), updated_at = now()`,
		userID, len(others)); err != nil {
		return fmt.Errorf("hide known on: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM dating_known_people WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("clear known people: %w", err)
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO dating_known_people (user_id, other_id)
        SELECT $1, o FROM unnest($2::uuid[]) AS o WHERE o <> $1
        ON CONFLICT DO NOTHING`, userID, others); err != nil {
		return fmt.Errorf("store known people: %w", err)
	}
	return tx.Commit(ctx)
}

// DisableHideKnown turns the setting off and drops the snapshot.
func (s *Store) DisableHideKnown(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin hide known off: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
        INSERT INTO dating_hide_known (user_id, enabled, connections, updated_at) VALUES ($1, false, 0, now())
        ON CONFLICT (user_id) DO UPDATE SET enabled = false, connections = 0, refreshed_at = NULL, updated_at = now()`, userID); err != nil {
		return fmt.Errorf("hide known off: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM dating_known_people WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("clear known people: %w", err)
	}
	return tx.Commit(ctx)
}

// HideKnownState is the user's setting (off without a row).
type HideKnownState struct {
	Enabled     bool
	Connections int
	RefreshedAt *time.Time
}

// GetHideKnown reads the user's setting.
func (s *Store) GetHideKnown(ctx context.Context, userID uuid.UUID) (HideKnownState, error) {
	var st HideKnownState
	err := s.db.QueryRow(ctx, `
        SELECT COALESCE(bool_or(enabled), false), COALESCE(max(connections), 0), max(refreshed_at)
        FROM dating_hide_known WHERE user_id = $1`, userID).Scan(&st.Enabled, &st.Connections, &st.RefreshedAt)
	if err != nil {
		return HideKnownState{}, fmt.Errorf("hide known: %w", err)
	}
	return st, nil
}

// HideKnownDueForRefresh lists users with the setting on whose snapshot is
// older than olderThan, oldest first.
func (s *Store) HideKnownDueForRefresh(ctx context.Context, olderThan time.Duration, limit int) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
        SELECT user_id FROM dating_hide_known
        WHERE enabled AND (refreshed_at IS NULL OR refreshed_at < now() - make_interval(secs => $1))
        ORDER BY refreshed_at NULLS FIRST
        LIMIT $2`, olderThan.Seconds(), limit)
	if err != nil {
		return nil, fmt.Errorf("hide known due: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// TouchHideKnown records a refresh attempt that kept the old snapshot, so
// a failing user does not hold the head of the queue.
func (s *Store) TouchHideKnown(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE dating_hide_known SET refreshed_at = now() WHERE user_id = $1 AND enabled`, userID)
	return err
}
