package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Module 1 fixes-v1 / Codex P0-2 — durable caption request lifecycle.
// Status only; the transcript itself lives in media_subtitles so there is
// exactly one canonical content store.

// CaptionJob is the persisted request state for one media asset.
type CaptionJob struct {
	MediaID   uuid.UUID
	Language  string
	Status    string // pending | running | completed | failed
	Attempts  int
	LastError *string
	UpdatedAt time.Time
	// ClaimToken fences complete/fail/release so a stale worker cannot
	// finalize a job another worker has since reclaimed (Codex P1-4).
	ClaimToken *uuid.UUID
}

// ErrCaptionClaimLost is returned when a terminal write is attempted
// without holding the current claim.
var ErrCaptionClaimLost = errors.New("caption job claim lost")

// EnqueueCaptionJob records (or re-arms) a caption request. A completed
// job is not reset — re-requesting captions must not wipe a
// creator-corrected transcript.
func (s *MediaAssetStore) EnqueueCaptionJob(ctx context.Context, mediaID uuid.UUID, language string, requestedBy uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO media_caption_jobs (media_id, language, status, requested_by, updated_at)
		VALUES ($1, $2, 'pending', $3, NOW())
		ON CONFLICT (media_id) DO UPDATE
		SET language = EXCLUDED.language,
		    status = 'pending',
		    last_error = NULL,
		    updated_at = NOW()
		WHERE media_caption_jobs.status <> 'completed'`,
		mediaID, language, requestedBy)
	return err
}

// GetCaptionJob returns the durable request state. (nil, nil) when no
// request was ever made — the caller reports that distinctly from
// "pending", so status is never inferred (Codex P0-2 evidence line 43).
func (s *MediaAssetStore) GetCaptionJob(ctx context.Context, mediaID uuid.UUID) (*CaptionJob, error) {
	var j CaptionJob
	err := s.db.QueryRow(ctx, `
		SELECT media_id, language, status, attempts, last_error, updated_at
		FROM media_caption_jobs WHERE media_id = $1`, mediaID).
		Scan(&j.MediaID, &j.Language, &j.Status, &j.Attempts, &j.LastError, &j.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// ClaimCaptionJobs atomically claims pending work for this worker
// (FOR UPDATE SKIP LOCKED — safe across replicas). Jobs stuck in
// 'running' past staleAfter are reclaimed from a dead worker.
func (s *MediaAssetStore) ClaimCaptionJobs(ctx context.Context, staleAfter time.Duration, limit int) ([]CaptionJob, error) {
	rows, err := s.db.Query(ctx, `
		WITH claimable AS (
			SELECT media_id FROM media_caption_jobs
			WHERE status = 'pending'
			   OR (status = 'running' AND claimed_at < $1)
			ORDER BY created_at ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE media_caption_jobs j
		SET status = 'running', claimed_at = NOW(), claim_token = gen_random_uuid(),
		    attempts = j.attempts + 1, updated_at = NOW()
		FROM claimable WHERE j.media_id = claimable.media_id
		RETURNING j.media_id, j.language, j.status, j.attempts, j.last_error,
		          j.updated_at, j.claim_token`,
		time.Now().Add(-staleAfter), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []CaptionJob
	for rows.Next() {
		var j CaptionJob
		if err := rows.Scan(&j.MediaID, &j.Language, &j.Status, &j.Attempts, &j.LastError,
			&j.UpdatedAt, &j.ClaimToken); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// CompleteCaptionJob marks a request finished successfully. Conditional
// on holding the claim: a stale worker cannot finalize a job another
// worker has reclaimed (Codex P1-4). Returns ErrCaptionClaimLost when the
// token no longer matches — the caller must NOT then approve safety.
func (s *MediaAssetStore) CompleteCaptionJob(ctx context.Context, mediaID uuid.UUID, token *uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE media_caption_jobs
		SET status = 'completed', last_error = NULL, updated_at = NOW()
		WHERE media_id = $1 AND ($2::uuid IS NULL OR claim_token = $2)`, mediaID, token)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrCaptionClaimLost
	}
	return nil
}

// FailCaptionJob records a terminal failure. The media and any draft are
// untouched; the client sees `failed` and can retry (Codex P0-9).
func (s *MediaAssetStore) FailCaptionJob(ctx context.Context, mediaID uuid.UUID, reason string, token *uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE media_caption_jobs
		SET status = 'failed', last_error = $2, updated_at = NOW()
		WHERE media_id = $1 AND ($3::uuid IS NULL OR claim_token = $3)`, mediaID, reason, token)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrCaptionClaimLost
	}
	return nil
}

// ReleaseCaptionJob returns a claimed job to pending for a later retry.
func (s *MediaAssetStore) ReleaseCaptionJob(ctx context.Context, mediaID uuid.UUID, reason string, token *uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE media_caption_jobs
		SET status = 'pending', claim_token = NULL, last_error = $2, updated_at = NOW()
		WHERE media_id = $1 AND status = 'running'
		  AND ($3::uuid IS NULL OR claim_token = $3)`, mediaID, reason, token)
	return err
}

// UpdateSubtitleContent stores the inline transcript text (and marks an
// owner edit). Content lives here — the canonical caption store.
func (s *MediaAssetStore) UpdateSubtitleContent(ctx context.Context, mediaID uuid.UUID, language, content string, ownerEdited bool) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO media_subtitles (media_asset_id, language, source, format, content_url, content, edited_by_owner, updated_at)
		VALUES ($1, $2, 'manual', 'vtt', '', $3, $4, NOW())
		ON CONFLICT (media_asset_id, language) DO UPDATE
		SET content = EXCLUDED.content,
		    edited_by_owner = EXCLUDED.edited_by_owner,
		    updated_at = NOW()`,
		mediaID, language, content, ownerEdited)
	return err
}

// SetAltDecorative marks (or unmarks) a media asset as decorative.
// Mutually exclusive with a non-empty alt text: marking decorative
// clears the description in the same statement (Codex P1-7).
func (s *MediaAssetStore) SetAltDecorative(ctx context.Context, mediaID uuid.UUID, decorative bool) error {
	_, err := s.db.Exec(ctx, `
		UPDATE media_assets
		SET alt_decorative = $2,
		    alt_text = CASE WHEN $2 THEN '' ELSE alt_text END
		WHERE id = $1`, mediaID, decorative)
	return err
}
