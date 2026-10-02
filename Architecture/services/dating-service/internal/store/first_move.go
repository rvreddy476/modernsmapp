// First move store — mechanic M5.
//
// dating_profiles.first_move_enabled is the per-user opt-in. Opening
// questions are kept as rows: replacing the set archives the old rows
// (archived_at) rather than deleting them. dating_matches.first_mover_ids is
// the snapshot, taken when the match forms, of who sends the first message.
// dating_match_extend_ledger counts the free 24-hour extends.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrOpeningQuestionNotFound: no live opening question with that id.
var ErrOpeningQuestionNotFound = errors.New("not_found: opening question not found")

// FreeExtendWindow is the rolling window the free extend counts.
const FreeExtendWindow = 24 * time.Hour

// OpeningQuestion is one of a user's opening questions.
type OpeningQuestion struct {
	ID       uuid.UUID `json:"id"`
	UserID   uuid.UUID `json:"-"`
	Position int       `json:"-"`
	Text     string    `json:"text"`
}

// FirstMoveSettings is the user's opt-in and live opening questions.
type FirstMoveSettings struct {
	Enabled   bool
	Questions []OpeningQuestion
}

// GetFirstMoveSettings reads the opt-in (false without a profile) and the
// live questions in order.
func (s *Store) GetFirstMoveSettings(ctx context.Context, userID uuid.UUID) (*FirstMoveSettings, error) {
	out := &FirstMoveSettings{Questions: []OpeningQuestion{}}
	err := s.db.QueryRow(ctx, `
        SELECT first_move_enabled FROM dating_profiles WHERE user_id = $1 AND deleted_at IS NULL`, userID).Scan(&out.Enabled)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("first move setting: %w", err)
	}
	qs, err := s.LiveOpeningQuestions(ctx, []uuid.UUID{userID})
	if err != nil {
		return nil, err
	}
	if q := qs[userID]; q != nil {
		out.Questions = q
	}
	return out, nil
}

// SetFirstMoveSettings writes the opt-in (nil: unchanged) and, when
// questions is non-nil, replaces the live question set: the old rows are
// archived, the new ones inserted in order. One transaction. A user with no
// profile row is ErrProfileNotFound.
func (s *Store) SetFirstMoveSettings(ctx context.Context, userID uuid.UUID, enabled *bool, questions []string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin first move: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if enabled != nil {
		tag, err := tx.Exec(ctx, `
            UPDATE dating_profiles SET first_move_enabled = $2, updated_at = now()
            WHERE user_id = $1 AND deleted_at IS NULL`, userID, *enabled)
		if err != nil {
			return fmt.Errorf("set first move: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrProfileNotFound
		}
	}
	if questions != nil {
		if _, err := tx.Exec(ctx, `
            UPDATE dating_opening_questions SET archived_at = now()
            WHERE user_id = $1 AND archived_at IS NULL`, userID); err != nil {
			return fmt.Errorf("archive opening questions: %w", err)
		}
		for i, text := range questions {
			if _, err := tx.Exec(ctx, `
                INSERT INTO dating_opening_questions (user_id, position, text) VALUES ($1, $2, $3)`,
				userID, i+1, text); err != nil {
				return fmt.Errorf("insert opening question: %w", err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit first move: %w", err)
	}
	return nil
}

// LiveOpeningQuestions returns each user's live questions, in order.
func (s *Store) LiveOpeningQuestions(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID][]OpeningQuestion, error) {
	out := map[uuid.UUID][]OpeningQuestion{}
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
        SELECT id, user_id, position, text FROM dating_opening_questions
        WHERE user_id = ANY($1) AND archived_at IS NULL
        ORDER BY user_id, position`, userIDs)
	if err != nil {
		return nil, fmt.Errorf("opening questions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var q OpeningQuestion
		if err := rows.Scan(&q.ID, &q.UserID, &q.Position, &q.Text); err != nil {
			return nil, fmt.Errorf("scan opening question: %w", err)
		}
		out[q.UserID] = append(out[q.UserID], q)
	}
	return out, rows.Err()
}

// GetLiveOpeningQuestion returns one live question, or
// ErrOpeningQuestionNotFound.
func (s *Store) GetLiveOpeningQuestion(ctx context.Context, id uuid.UUID) (*OpeningQuestion, error) {
	var q OpeningQuestion
	err := s.db.QueryRow(ctx, `
        SELECT id, user_id, position, text FROM dating_opening_questions
        WHERE id = $1 AND archived_at IS NULL`, id).Scan(&q.ID, &q.UserID, &q.Position, &q.Text)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrOpeningQuestionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("opening question: %w", err)
	}
	return &q, nil
}

// FirstMoveOptedIn returns which of the users have opted in to moving first.
func (s *Store) FirstMoveOptedIn(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	rows, err := s.db.Query(ctx, `
        SELECT user_id FROM dating_profiles
        WHERE user_id = ANY($1) AND first_move_enabled AND deleted_at IS NULL`, userIDs)
	if err != nil {
		return nil, fmt.Errorf("first move opt-in: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// SetMatchFirstMovers snapshots who sends the first message in a match. Only
// while the match has no first message, so a saga retry cannot change it.
func (s *Store) SetMatchFirstMovers(ctx context.Context, matchID uuid.UUID, movers []uuid.UUID) error {
	if movers == nil {
		movers = []uuid.UUID{}
	}
	_, err := s.db.Exec(ctx, `
        UPDATE dating_matches SET first_mover_ids = $2
        WHERE id = $1 AND first_message_at IS NULL`, matchID, movers)
	if err != nil {
		return fmt.Errorf("set first movers: %w", err)
	}
	return nil
}

// FreeExtendUsage returns how many free extends the user took inside
// FreeExtendWindow and when the oldest was (nil when none).
func (s *Store) FreeExtendUsage(ctx context.Context, userID uuid.UUID) (int, *time.Time, error) {
	var used int
	var oldest *time.Time
	err := s.db.QueryRow(ctx, `
        SELECT COUNT(*)::int, min(extended_at) FROM dating_match_extend_ledger
        WHERE user_id = $1 AND extended_at > now() - make_interval(secs => $2)`,
		userID, FreeExtendWindow.Seconds()).Scan(&used, &oldest)
	if err != nil {
		return 0, nil, fmt.Errorf("free extend usage: %w", err)
	}
	return used, oldest, nil
}

// ErrFreeExtendLimited: the free extend allowance is used.
var ErrFreeExtendLimited = errors.New("rate_limited: free extend used")

// FreeExtendMatch pushes a first-move match's expiry out by `by`, charging
// one free extend to userID: refused (ErrFreeExtendLimited) at or above
// limit inside the window. One transaction under a per-user lock. Returns
// the new expiry. The match must still be waiting for its first message
// (ErrMatchNotFound otherwise).
func (s *Store) FreeExtendMatch(ctx context.Context, userID, matchID uuid.UUID, by time.Duration, limit int) (time.Time, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("begin extend: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 7023))`, userID.String()); err != nil {
		return time.Time{}, fmt.Errorf("lock extend: %w", err)
	}
	var used int
	if err := tx.QueryRow(ctx, `
        SELECT COUNT(*)::int FROM dating_match_extend_ledger
        WHERE user_id = $1 AND extended_at > now() - make_interval(secs => $2)`,
		userID, FreeExtendWindow.Seconds()).Scan(&used); err != nil {
		return time.Time{}, fmt.Errorf("count extends: %w", err)
	}
	if used >= limit {
		return time.Time{}, ErrFreeExtendLimited
	}
	var expires time.Time
	err = tx.QueryRow(ctx, `
        UPDATE dating_matches
        SET expires_at = GREATEST(COALESCE(expires_at, now()), now()) + make_interval(secs => $2)
        WHERE id = $1 AND status = 'matched' AND first_message_at IS NULL
        RETURNING expires_at`, matchID, by.Seconds()).Scan(&expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrMatchNotFound
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("extend match: %w", err)
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO dating_match_extend_ledger (user_id, match_id) VALUES ($1, $2)`, userID, matchID); err != nil {
		return time.Time{}, fmt.Errorf("record extend: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return time.Time{}, fmt.Errorf("commit extend: %w", err)
	}
	return expires, nil
}
