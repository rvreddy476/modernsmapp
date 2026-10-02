// Prompt clips — mechanic M15 (DATING_MEDIA_PROMPTS_ENABLED).
//
// A prompt answer may carry a short voice or video clip: the media asset
// (scope dating_clip in media-service, never exposed by id), its kind and
// length, and its moderation state:
//
//	pending         media-service has not decided yet (the sweeper asks again)
//	pending_review  a moderator decides (audio always lands here today)
//	approved        shown on cards
//	rejected        never shown; the owner sees the reason
//
// A clip-only answer has an empty text answer. Purged with the prompt rows.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Clip moderation states.
const (
	ClipStatusPending       = "pending"
	ClipStatusPendingReview = "pending_review"
	ClipStatusApproved      = "approved"
	ClipStatusRejected      = "rejected"
)

// promptCols is every prompt column a read returns, clip included.
const promptCols = `id, user_id, prompt_id, answer, created_at, updated_at,
    clip_media_id, clip_kind, clip_duration_ms, clip_status, clip_reason`

func scanPrompt(row pgx.Row) (Prompt, error) {
	var p Prompt
	err := row.Scan(&p.ID, &p.UserID, &p.PromptID, &p.Answer, &p.CreatedAt, &p.UpdatedAt,
		&p.ClipMediaID, &p.ClipKind, &p.ClipDurationMs, &p.ClipStatus, &p.ClipReason)
	return p, err
}

// GetPrompt reads one of the user's prompt answers.
func (s *Store) GetPrompt(ctx context.Context, userID uuid.UUID, promptID int) (*Prompt, error) {
	p, err := scanPrompt(s.db.QueryRow(ctx, `SELECT `+promptCols+` FROM dating_prompts WHERE user_id = $1 AND prompt_id = $2`, userID, promptID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPromptNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get prompt: %w", err)
	}
	return &p, nil
}

// SetPromptClip attaches a clip to the user's prompt (creating a clip-only
// answer when there is none) and returns the clip media it replaced, if any.
func (s *Store) SetPromptClip(ctx context.Context, userID uuid.UUID, promptID int, mediaID uuid.UUID, kind string, durationMs int, status string, reason *string) (*uuid.UUID, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin prompt clip: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var previous *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT clip_media_id FROM dating_prompts WHERE user_id = $1 AND prompt_id = $2 FOR UPDATE`, userID, promptID).Scan(&previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("read prompt clip: %w", err)
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO dating_prompts (user_id, prompt_id, answer, clip_media_id, clip_kind, clip_duration_ms, clip_status, clip_reason, clip_checked_at, clip_source)
        VALUES ($1, $2, '', $3, $4, $5, $6, $7, now(), 'auto')
        ON CONFLICT (user_id, prompt_id) DO UPDATE
        SET clip_media_id = EXCLUDED.clip_media_id, clip_kind = EXCLUDED.clip_kind,
            clip_duration_ms = EXCLUDED.clip_duration_ms, clip_status = EXCLUDED.clip_status,
            clip_reason = EXCLUDED.clip_reason, clip_checked_at = now(), clip_source = 'auto', updated_at = now()`,
		userID, promptID, mediaID, kind, durationMs, status, reason); err != nil {
		return nil, fmt.Errorf("set prompt clip: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	if previous != nil && *previous == mediaID {
		previous = nil
	}
	return previous, nil
}

// ClearPromptClip removes the clip from the user's prompt and returns its
// media. A prompt left with no text and no clip is removed.
func (s *Store) ClearPromptClip(ctx context.Context, userID uuid.UUID, promptID int) (*uuid.UUID, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin clear prompt clip: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var media *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT clip_media_id FROM dating_prompts WHERE user_id = $1 AND prompt_id = $2 FOR UPDATE`, userID, promptID).Scan(&media)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPromptNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read prompt clip: %w", err)
	}
	if _, err := tx.Exec(ctx, `
        UPDATE dating_prompts
        SET clip_media_id = NULL, clip_kind = NULL, clip_duration_ms = NULL, clip_status = NULL,
            clip_reason = NULL, clip_checked_at = NULL, clip_source = NULL, updated_at = now()
        WHERE user_id = $1 AND prompt_id = $2`, userID, promptID); err != nil {
		return nil, fmt.Errorf("clear prompt clip: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM dating_prompts WHERE user_id = $1 AND prompt_id = $2 AND answer = ''`, userID, promptID); err != nil {
		return nil, fmt.Errorf("drop empty prompt: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return media, nil
}

// SetPromptClipStatus records a moderation decision for the clip currently
// on the prompt (mediaID must still be it). source is auto or admin.
func (s *Store) SetPromptClipStatus(ctx context.Context, userID uuid.UUID, promptID int, mediaID uuid.UUID, status string, reason *string, source string) error {
	tag, err := s.db.Exec(ctx, `
        UPDATE dating_prompts
        SET clip_status = $4, clip_reason = $5, clip_source = $6, clip_checked_at = now(), updated_at = now()
        WHERE user_id = $1 AND prompt_id = $2 AND clip_media_id = $3`,
		userID, promptID, mediaID, status, reason, source)
	if err != nil {
		return fmt.Errorf("set prompt clip status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPromptNotFound
	}
	return nil
}

// TouchPromptClip records a check that changed nothing.
func (s *Store) TouchPromptClip(ctx context.Context, userID uuid.UUID, promptID int) error {
	_, err := s.db.Exec(ctx, `UPDATE dating_prompts SET clip_checked_at = now() WHERE user_id = $1 AND prompt_id = $2`, userID, promptID)
	return err
}

// ListPromptClipsByStatus lists clips in status, oldest check first,
// checked before olderThan ago.
func (s *Store) ListPromptClipsByStatus(ctx context.Context, status string, olderThan time.Duration, limit int) ([]Prompt, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
        SELECT `+promptCols+` FROM dating_prompts
        WHERE clip_status = $1 AND clip_media_id IS NOT NULL
          AND (clip_checked_at IS NULL OR clip_checked_at < now() - make_interval(secs => $2))
        ORDER BY clip_checked_at NULLS FIRST
        LIMIT $3`, status, olderThan.Seconds(), limit)
	if err != nil {
		return nil, fmt.Errorf("list prompt clips: %w", err)
	}
	defer rows.Close()
	var out []Prompt
	for rows.Next() {
		p, err := scanPrompt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
