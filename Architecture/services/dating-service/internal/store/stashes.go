// Stashes store — spec §10 dating_stashes. The Stash is the soft-intent
// "revisit later" shelf: a user may stash a candidate for up to 14 days
// (default) and reactivate them when a relevant signal fires.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Stash is one row of dating_stashes.
type Stash struct {
	UserID             uuid.UUID `json:"user_id"`
	CandidateID        uuid.UUID `json:"candidate_id"`
	StashedAt          time.Time `json:"stashed_at"`
	ExpiresAt          time.Time `json:"expires_at"`
	ReactivationSignal *string   `json:"reactivation_signal,omitempty"`
}

// ErrStashNotFound is returned when no row matches the user/candidate pair.
var ErrStashNotFound = errors.New("not_found: stash entry not found")

const stashSelectCols = `user_id, candidate_id, stashed_at, expires_at, reactivation_signal`

func scanStash(row pgx.Row) (*Stash, error) {
	st := &Stash{}
	if err := row.Scan(&st.UserID, &st.CandidateID, &st.StashedAt, &st.ExpiresAt, &st.ReactivationSignal); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrStashNotFound
		}
		return nil, fmt.Errorf("scan stash: %w", err)
	}
	return st, nil
}

// AddStash inserts (or refreshes) a stash entry. Idempotent: a duplicate
// (user, candidate) updates expires_at to the new value rather than failing.
func (s *Store) AddStash(ctx context.Context, userID, candidateID uuid.UUID, expiresAt time.Time) error {
	if userID == uuid.Nil || candidateID == uuid.Nil {
		return fmt.Errorf("invalid: user_id and candidate_id required")
	}
	if userID == candidateID {
		return fmt.Errorf("invalid: cannot stash yourself")
	}
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(14 * 24 * time.Hour)
	}
	if _, err := s.db.Exec(ctx, `
        INSERT INTO dating_stashes (user_id, candidate_id, expires_at)
        VALUES ($1, $2, $3)
        ON CONFLICT (user_id, candidate_id) DO UPDATE
            SET expires_at = EXCLUDED.expires_at,
                stashed_at = now(),
                reactivation_signal = NULL`, userID, candidateID, expiresAt); err != nil {
		return fmt.Errorf("add stash: %w", err)
	}
	return nil
}

// RemoveStash deletes the stash entry. The reason argument is currently
// unstored in dating_stashes; callers emit it via the kafka payload for
// audit + analytics. Returns ErrStashNotFound when no row matched.
func (s *Store) RemoveStash(ctx context.Context, userID, candidateID uuid.UUID, reason string) error {
	tag, err := s.db.Exec(ctx, `
        DELETE FROM dating_stashes
        WHERE user_id = $1 AND candidate_id = $2`, userID, candidateID)
	if err != nil {
		return fmt.Errorf("remove stash: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrStashNotFound
	}
	_ = reason // accepted for the audit event; not stored
	return nil
}

// ListStash returns all non-expired stash entries for a user (newest first).
func (s *Store) ListStash(ctx context.Context, userID uuid.UUID) ([]*Stash, error) {
	rows, err := s.db.Query(ctx, `
        SELECT `+stashSelectCols+`
        FROM dating_stashes
        WHERE user_id = $1
          AND (expires_at IS NULL OR expires_at > now())
        ORDER BY stashed_at DESC
        LIMIT 200`, userID)
	if err != nil {
		return nil, fmt.Errorf("list stash: %w", err)
	}
	defer rows.Close()

	out := make([]*Stash, 0, 32)
	for rows.Next() {
		st, err := scanStash(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// MarkStashReactivated stamps a reason string on the row so the discover UI
// can surface "you stashed them; they just posted X" hints.
func (s *Store) MarkStashReactivated(ctx context.Context, userID, candidateID uuid.UUID, signal string) error {
	tag, err := s.db.Exec(ctx, `
        UPDATE dating_stashes
        SET reactivation_signal = $3
        WHERE user_id = $1 AND candidate_id = $2`, userID, candidateID, signal)
	if err != nil {
		return fmt.Errorf("mark stash reactivated: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrStashNotFound
	}
	return nil
}
