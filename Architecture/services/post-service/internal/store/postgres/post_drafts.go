package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Module 1 P0-5 — server-side drafts for the unified composer
// (text / photo / carousel / poll / article). Reel and long-video drafts
// remain in reel_drafts; both are published by the same worker loop.

// ErrDraftQuota is returned when the author exceeds the active-draft cap.
var ErrDraftQuota = errors.New("draft quota exceeded")

// MaxActiveDraftsPerAuthor caps stored drafts per author. Blocked drafts
// count too (they hold media); published/deleted do not.
const MaxActiveDraftsPerAuthor = 100

type PostDraft struct {
	ID              uuid.UUID       `json:"id"`
	AuthorID        uuid.UUID       `json:"author_id"`
	PostType        string          `json:"post_type"`
	Payload         json.RawMessage `json:"payload"`
	ScheduleAt      *time.Time      `json:"schedule_at,omitempty"`
	Status          string          `json:"status"`
	BlockedReason   *string         `json:"blocked_reason,omitempty"`
	PublishedPostID *uuid.UUID      `json:"published_post_id,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	// ClaimToken identifies the holder of the current publish claim.
	// Finalize/block/release are conditional on it (Codex P1-3).
	// Not serialized — internal concurrency control, not client state.
	ClaimToken *uuid.UUID `json:"-"`
}

const postDraftCols = `id, author_id, post_type, payload, schedule_at,
	status, blocked_reason, published_post_id, created_at, updated_at, claim_token`

func scanPostDraft(row pgx.Row) (*PostDraft, error) {
	var d PostDraft
	err := row.Scan(&d.ID, &d.AuthorID, &d.PostType, &d.Payload, &d.ScheduleAt,
		&d.Status, &d.BlockedReason, &d.PublishedPostID, &d.CreatedAt, &d.UpdatedAt, &d.ClaimToken)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// CreatePostDraft inserts a draft, enforcing the per-author quota.
//
// Codex P1-4: COUNT-then-INSERT in a plain READ COMMITTED transaction is
// not a quota — two concurrent creates can both read 99 and both insert.
// A per-author transaction-scoped advisory lock serializes the
// check-and-insert for that author only (no table lock, no serializable
// retry loop), so the limit actually holds under concurrency.
func (s *Store) CreatePostDraft(ctx context.Context, d *PostDraft, mediaIDs []uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Serialize concurrent creates for this author. Released on commit
	// or rollback automatically (xact-scoped).
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtext('post_drafts:' || $1::text))`,
		d.AuthorID); err != nil {
		return err
	}

	var active int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM post_drafts
		WHERE author_id = $1 AND status IN ('draft','publishing','blocked')`,
		d.AuthorID).Scan(&active); err != nil {
		return err
	}
	if active >= MaxActiveDraftsPerAuthor {
		return ErrDraftQuota
	}

	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO post_drafts (id, author_id, post_type, payload, schedule_at, status)
		VALUES ($1, $2, $3, $4, $5, 'draft')
		RETURNING created_at, updated_at
	`, d.ID, d.AuthorID, d.PostType, d.Payload, d.ScheduleAt).Scan(&d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return err
	}
	// fixes-v2 / Codex P1-5: the media reference set is written in the
	// SAME transaction as the draft. v1 wrote it afterwards and only
	// logged failures, so a successful API response could leave a draft
	// with no reference rows — which silently defeats safe reclamation.
	if err := replaceDraftMediaTx(ctx, tx, d.ID, mediaIDs); err != nil {
		return err
	}
	d.Status = "draft"
	return tx.Commit(ctx)
}

// replaceDraftMediaTx rewrites a draft's media reference set inside tx.
func replaceDraftMediaTx(ctx context.Context, tx pgx.Tx, draftID uuid.UUID, mediaIDs []uuid.UUID) error {
	if _, err := tx.Exec(ctx, `DELETE FROM post_draft_media WHERE draft_id = $1`, draftID); err != nil {
		return err
	}
	for _, mediaID := range mediaIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO post_draft_media (draft_id, media_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING`, draftID, mediaID); err != nil {
			return err
		}
	}
	return nil
}

// GetPostDraft fetches one draft, owner-scoped. (nil, nil) when absent.
func (s *Store) GetPostDraft(ctx context.Context, id, authorID uuid.UUID) (*PostDraft, error) {
	d, err := scanPostDraft(s.db.QueryRow(ctx, `
		SELECT `+postDraftCols+` FROM post_drafts
		WHERE id = $1 AND author_id = $2 AND status != 'deleted'`, id, authorID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return d, err
}

// ListPostDrafts returns the author's active drafts, newest first.
func (s *Store) ListPostDrafts(ctx context.Context, authorID uuid.UUID, limit int) ([]PostDraft, error) {
	if limit <= 0 || limit > MaxActiveDraftsPerAuthor {
		limit = MaxActiveDraftsPerAuthor
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+postDraftCols+` FROM post_drafts
		WHERE author_id = $1 AND status IN ('draft','publishing','blocked')
		ORDER BY updated_at DESC LIMIT $2`, authorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PostDraft
	for rows.Next() {
		var d PostDraft
		if err := rows.Scan(&d.ID, &d.AuthorID, &d.PostType, &d.Payload, &d.ScheduleAt,
			&d.Status, &d.BlockedReason, &d.PublishedPostID, &d.CreatedAt, &d.UpdatedAt, &d.ClaimToken); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// UpdatePostDraft replaces payload/type/schedule for a draft still in an
// editable state. Editing a blocked draft returns it to 'draft' so the
// author's fix gets retried. Returns false when nothing was updated
// (missing, deleted, published, or mid-publish).
func (s *Store) UpdatePostDraft(ctx context.Context, id, authorID uuid.UUID, postType string,
	payload json.RawMessage, scheduleAt *time.Time, mediaIDs []uuid.UUID) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE post_drafts
		SET post_type = $3, payload = $4, schedule_at = $5,
		    status = 'draft', blocked_reason = NULL, updated_at = NOW()
		WHERE id = $1 AND author_id = $2 AND status IN ('draft','blocked')`,
		id, authorID, postType, payload, scheduleAt)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	// P1-5: reference set updated atomically with the payload it describes.
	if err := replaceDraftMediaTx(ctx, tx, id, mediaIDs); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// DeletePostDraft soft-deletes. A cancelled draft can never publish: the
// claim query only picks status='draft'.
func (s *Store) DeletePostDraft(ctx context.Context, id, authorID uuid.UUID) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE post_drafts SET status = 'deleted', updated_at = NOW()
		WHERE id = $1 AND author_id = $2 AND status IN ('draft','blocked')`,
		id, authorID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ClaimDuePostDrafts atomically claims up to limit due drafts for this
// worker: status draft→publishing under FOR UPDATE SKIP LOCKED, so N
// replicas never claim the same draft. Also reclaims drafts stuck in
// 'publishing' for over staleAfter (a worker that died mid-publish);
// the draft-id-as-post-id idempotency makes the retry safe.
func (s *Store) ClaimDuePostDrafts(ctx context.Context, now time.Time, staleAfter time.Duration, limit int) ([]PostDraft, error) {
	// A fresh token per claim — a reclaim invalidates the dead worker's
	// token, so its late finalize/block cannot take effect (Codex P1-3).
	rows, err := s.db.Query(ctx, `
		WITH due AS (
			SELECT id FROM post_drafts
			WHERE (status = 'draft' AND schedule_at IS NOT NULL AND schedule_at <= $1)
			   OR (status = 'publishing' AND claimed_at < $2)
			ORDER BY schedule_at ASC
			LIMIT $3
			FOR UPDATE SKIP LOCKED
		)
		UPDATE post_drafts d
		SET status = 'publishing', claim_token = gen_random_uuid(),
		    claimed_at = NOW(), updated_at = NOW()
		FROM due WHERE d.id = due.id
		RETURNING d.id, d.author_id, d.post_type, d.payload, d.schedule_at,
		          d.status, d.blocked_reason, d.published_post_id, d.created_at,
		          d.updated_at, d.claim_token`,
		now, now.Add(-staleAfter), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PostDraft
	for rows.Next() {
		var d PostDraft
		if err := rows.Scan(&d.ID, &d.AuthorID, &d.PostType, &d.Payload, &d.ScheduleAt,
			&d.Status, &d.BlockedReason, &d.PublishedPostID, &d.CreatedAt, &d.UpdatedAt, &d.ClaimToken); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ErrDraftClaimLost is returned when a state transition is attempted
// without holding the current claim (another worker reclaimed it, or the
// draft was cancelled/edited underneath us).
var ErrDraftClaimLost = errors.New("draft claim lost")

// ClaimPostDraftForPublish atomically transitions ONE draft from
// 'draft' → 'publishing' and stamps a fresh claim token, owner-scoped.
//
// Codex P1-3: immediate publication previously only read the row, so a
// concurrent delete or edit could interleave between the read and the
// insert. Both publication paths now go through this compare-and-set, so
// a cancelled draft can never publish and an edit either lands before the
// claim (and is published) or is rejected as not-editable after it.
//
// Returns ErrDraftClaimLost when the draft is not in a claimable state.
func (s *Store) ClaimPostDraftForPublish(ctx context.Context, id, authorID uuid.UUID) (*PostDraft, error) {
	token := uuid.New()
	d, err := scanPostDraft(s.db.QueryRow(ctx, `
		UPDATE post_drafts
		SET status = 'publishing', claim_token = $3, claimed_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND author_id = $2 AND status IN ('draft','blocked')
		RETURNING `+postDraftCols, id, authorID, token))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDraftClaimLost
	}
	if err != nil {
		return nil, err
	}
	d.ClaimToken = &token
	return d, nil
}

// MarkPostDraftPublished finalizes a successful publish. Conditional on
// still holding the claim: if another worker reclaimed the draft, this is
// a no-op and the caller learns it lost the race.
func (s *Store) MarkPostDraftPublished(ctx context.Context, id, postID uuid.UUID, token *uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE post_drafts
		SET status = 'published', published_post_id = $2, updated_at = NOW()
		WHERE id = $1 AND ($3::uuid IS NULL OR claim_token = $3)`, id, postID, token)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrDraftClaimLost
	}
	return nil
}

// MarkPostDraftBlocked parks a draft in an actionable blocked state
// (standing/moderation/validation failure at publish time). Conditional
// on the claim so a stale worker cannot block a draft the author has
// since edited back into 'draft'.
func (s *Store) MarkPostDraftBlocked(ctx context.Context, id uuid.UUID, reason string, token *uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE post_drafts
		SET status = 'blocked', blocked_reason = $2, claim_token = NULL, updated_at = NOW()
		WHERE id = $1 AND ($3::uuid IS NULL OR claim_token = $3)`, id, reason, token)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrDraftClaimLost
	}
	return nil
}

// ReleasePostDraftClaim returns a claimed draft to 'draft' after a
// transient failure so the next tick retries it. Conditional on the claim.
func (s *Store) ReleasePostDraftClaim(ctx context.Context, id uuid.UUID, token *uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE post_drafts
		SET status = 'draft', claim_token = NULL, claimed_at = NULL, updated_at = NOW()
		WHERE id = $1 AND status = 'publishing'
		  AND ($2::uuid IS NULL OR claim_token = $2)`, id, token)
	return err
}

// RecordDraftMedia records the media a draft references, so deletion can
// later identify reclaimable assets (Codex P1-4).
func (s *Store) RecordDraftMedia(ctx context.Context, draftID uuid.UUID, mediaIDs []uuid.UUID) error {
	if len(mediaIDs) == 0 {
		// Replace-semantics: an edit that removes all media clears the rows.
		_, err := s.db.Exec(ctx, `DELETE FROM post_draft_media WHERE draft_id = $1`, draftID)
		return err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM post_draft_media WHERE draft_id = $1`, draftID); err != nil {
		return err
	}
	for _, mediaID := range mediaIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO post_draft_media (draft_id, media_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING`, draftID, mediaID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// OrphanedDraftMedia returns media referenced ONLY by drafts that were
// soft-deleted more than retention ago — i.e. not attached to any post
// and not referenced by any surviving draft. These are safe to delete
// from media-service (Codex P1-4: reference checks before cleanup).
func (s *Store) OrphanedDraftMedia(ctx context.Context, retention time.Duration, limit int) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT dm.media_id
		FROM post_draft_media dm
		JOIN post_drafts d ON d.id = dm.draft_id
		WHERE d.status = 'deleted'
		  AND d.updated_at < $1
		  -- not used by any live post
		  AND NOT EXISTS (SELECT 1 FROM post_media pm WHERE pm.media_id = dm.media_id)
		  -- not referenced by any draft that is still alive
		  AND NOT EXISTS (
		        SELECT 1 FROM post_draft_media dm2
		        JOIN post_drafts d2 ON d2.id = dm2.draft_id
		        WHERE dm2.media_id = dm.media_id
		          AND d2.status <> 'deleted')
		LIMIT $2`, time.Now().Add(-retention), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CleanupDeletedPostDrafts hard-deletes rows soft-deleted more than
// retention ago (documented retention: 30 days).
func (s *Store) CleanupDeletedPostDrafts(ctx context.Context, retention time.Duration) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM post_drafts
		WHERE status = 'deleted' AND updated_at < $1`,
		time.Now().Add(-retention))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
