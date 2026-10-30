// Mutual-spark retry markers (audit 2026-10-30 L2). See dating_pending_matches
// in database/setup.sql.
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// PendingMatch is one claimed dating_pending_matches row.
type PendingMatch struct {
	UserA       uuid.UUID
	UserB       uuid.UUID
	SparkTarget map[string]any
	Attempts    int
	CreatedAt   time.Time
}

// RecordPendingMatch writes (or re-arms) the pair's marker, first due after
// delay so the live request has time to finish the match itself.
func (s *Store) RecordPendingMatch(ctx context.Context, x, y uuid.UUID, sparkTarget map[string]any, delay time.Duration) error {
	a, b := canonicalPair(x, y)
	if a == uuid.Nil || a == b {
		return fmt.Errorf("invalid: two distinct user ids required")
	}
	if sparkTarget == nil {
		sparkTarget = map[string]any{}
	}
	raw, err := json.Marshal(sparkTarget)
	if err != nil {
		return fmt.Errorf("marshal spark target: %w", err)
	}
	if _, err := s.db.Exec(ctx, `
        INSERT INTO dating_pending_matches (user_a, user_b, spark_target, next_attempt_at)
        VALUES ($1, $2, $3::jsonb, now() + make_interval(secs => $4))
        ON CONFLICT (user_a, user_b) DO UPDATE
        SET spark_target = EXCLUDED.spark_target,
            next_attempt_at = EXCLUDED.next_attempt_at,
            failed_at = NULL`, a, b, raw, delay.Seconds()); err != nil {
		return fmt.Errorf("record pending match: %w", err)
	}
	return nil
}

// ClearPendingMatch removes the pair's marker (the match formed, or the pair
// can no longer match). Idempotent.
func (s *Store) ClearPendingMatch(ctx context.Context, x, y uuid.UUID) error {
	a, b := canonicalPair(x, y)
	if _, err := s.db.Exec(ctx, `DELETE FROM dating_pending_matches WHERE user_a = $1 AND user_b = $2`, a, b); err != nil {
		return fmt.Errorf("clear pending match: %w", err)
	}
	return nil
}

// ClaimDuePendingMatches claims up to limit due markers for lease: each
// claimed row's next_attempt_at moves lease ahead, so another replica skips
// it until this one records the outcome or the lease lapses.
func (s *Store) ClaimDuePendingMatches(ctx context.Context, limit int, lease time.Duration) ([]*PendingMatch, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
        UPDATE dating_pending_matches pm
        SET next_attempt_at = now() + make_interval(secs => $2)
        FROM (
            SELECT user_a, user_b FROM dating_pending_matches
            WHERE failed_at IS NULL AND next_attempt_at <= now()
            ORDER BY next_attempt_at
            LIMIT $1
            FOR UPDATE SKIP LOCKED
        ) due
        WHERE pm.user_a = due.user_a AND pm.user_b = due.user_b
        RETURNING pm.user_a, pm.user_b, pm.spark_target, pm.attempts, pm.created_at`, limit, lease.Seconds())
	if err != nil {
		return nil, fmt.Errorf("claim pending matches: %w", err)
	}
	defer rows.Close()
	var out []*PendingMatch
	for rows.Next() {
		pm := &PendingMatch{}
		var raw []byte
		if err := rows.Scan(&pm.UserA, &pm.UserB, &raw, &pm.Attempts, &pm.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan pending match: %w", err)
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &pm.SparkTarget)
		}
		out = append(out, pm)
	}
	return out, rows.Err()
}

// RecordPendingMatchFailure counts a failed attempt, keeps the error text
// (never a payload) and schedules the next attempt after backoff; giveUp
// sets failed_at so the row is no longer claimed.
func (s *Store) RecordPendingMatchFailure(ctx context.Context, x, y uuid.UUID, cause string, backoff time.Duration, giveUp bool) error {
	a, b := canonicalPair(x, y)
	if len(cause) > 500 {
		cause = cause[:500]
	}
	if _, err := s.db.Exec(ctx, `
        UPDATE dating_pending_matches
        SET attempts = attempts + 1,
            last_error = $3,
            next_attempt_at = now() + make_interval(secs => $4),
            failed_at = CASE WHEN $5 THEN now() ELSE NULL END
        WHERE user_a = $1 AND user_b = $2`, a, b, cause, backoff.Seconds(), giveUp); err != nil {
		return fmt.Errorf("record pending match failure: %w", err)
	}
	return nil
}
