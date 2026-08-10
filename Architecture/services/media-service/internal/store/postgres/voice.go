package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Module 1 P0-6 — voice post support.

// UpdateMediaDuration persists the server-measured duration (seconds).
// Voice posts never trust a client-declared duration: this value comes
// from ffprobe at confirm time and backs the 180 s cap.
func (s *MediaAssetStore) UpdateMediaDuration(ctx context.Context, mediaID uuid.UUID, seconds int) error {
	_, err := s.db.Exec(ctx,
		`UPDATE media_assets SET duration_seconds = $2 WHERE id = $1`,
		mediaID, seconds)
	return err
}

// IsMediaReferencedByPost reports whether any post still attaches this
// media. Retained for read-only diagnostics ONLY — it must not be used to
// authorize a deletion, because a check in one statement followed by a
// delete in another is not race-free under READ COMMITTED. Use
// DeleteOrphanMediaAtomic for anything destructive.
//
// post_media lives in the shared `app` database alongside media_assets.
// If the table is absent the query errors and the caller refuses the
// delete — fail-closed, since deleting live media is unrecoverable.
func (s *MediaAssetStore) IsMediaReferencedByPost(ctx context.Context, mediaID uuid.UUID) (bool, error) {
	var referenced bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM post_media WHERE media_id = $1)`, mediaID).Scan(&referenced)
	if err != nil {
		return false, err
	}
	return referenced, nil
}

// ErrMediaStillReferenced means the asset is attached to a published post
// or a surviving draft, or lost the race to a concurrent attachment.
var ErrMediaStillReferenced = errors.New("media is still referenced")

// ErrMediaTooYoung means the asset has not aged past the reclaim window.
var ErrMediaTooYoung = errors.New("media is inside the reclaim window")

// ErrMediaNotFound means there is no such asset (already reclaimed).
var ErrMediaNotFound = errors.New("media not found")

// DeleteOrphanMediaAtomic performs the ENTIRE eligibility decision and the
// deletion inside one transaction (Module 1 fixes-v3 / LB-1).
//
// Ordering and locking, deliberately:
//
//  1. SELECT … FOR UPDATE on the media_assets row. This is the
//     serialization point. With the foreign keys added by post-service
//     migration 030, any concurrent INSERT into post_media /
//     post_draft_media must take a FOR KEY SHARE lock on this same row,
//     which conflicts with our FOR UPDATE. The attacher therefore blocks
//     until we commit or roll back — closing the TOCTOU window that a
//     bare "SELECT NOT EXISTS then DELETE" leaves open, because under
//     READ COMMITTED a subquery cannot see a concurrent uncommitted
//     insert no matter how the statements are arranged.
//  2. Age check inside the same transaction.
//  3. Reference checks against BOTH published post_media and every
//     SURVIVING post_draft_media row (a draft that is not soft-deleted).
//     v2 only checked post_media, so any unposted draft asset older than
//     the window was wrongly considered orphaned.
//  4. Record object keys in media_blob_reclaim BEFORE the rows are
//     removed, so a later blob-delete failure can still be retried.
//  5. Delete child rows, then the asset.
//
// If the foreign keys are not yet applied, step 1 no longer blocks the
// attacher and the guarantee degrades to the (weaker) snapshot check;
// the delete would then fail on the FK once applied. This is documented
// in the handover as the reason migration 030 is a launch gate.
func (s *MediaAssetStore) DeleteOrphanMediaAtomic(ctx context.Context, mediaID uuid.UUID, minAge time.Duration) ([]string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// 1. Lock the asset row.
	var createdAt time.Time
	var storageKey string
	err = tx.QueryRow(ctx, `
		SELECT created_at, storage_key FROM media_assets WHERE id = $1 FOR UPDATE`,
		mediaID).Scan(&createdAt, &storageKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrMediaNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock media row: %w", err)
	}

	// 2. Reclaim window.
	if time.Since(createdAt) < minAge {
		return nil, ErrMediaTooYoung
	}

	// 3. References — published AND surviving drafts. Any error here is
	// fail-closed: we refuse rather than guess.
	var referenced bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM post_media WHERE media_id = $1)
		    OR EXISTS(
		        SELECT 1 FROM post_draft_media dm
		        JOIN post_drafts d ON d.id = dm.draft_id
		        WHERE dm.media_id = $1 AND d.status <> 'deleted')`,
		mediaID).Scan(&referenced)
	if err != nil {
		return nil, fmt.Errorf("%w: reference check unavailable: %v", ErrMediaStillReferenced, err)
	}
	if referenced {
		return nil, ErrMediaStillReferenced
	}

	// 4. Collect object keys and durably record the reclaim intent BEFORE
	// the rows disappear.
	objectKeys := []string{storageKey}
	rows, err := tx.Query(ctx, `SELECT object_key FROM media_variants WHERE media_asset_id = $1`, mediaID)
	if err != nil {
		return nil, fmt.Errorf("fetch variant keys: %w", err)
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		objectKeys = append(objectKeys, key)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, key := range objectKeys {
		if key == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO media_blob_reclaim (object_key, media_id)
			VALUES ($1, $2) ON CONFLICT (object_key) DO NOTHING`, key, mediaID); err != nil {
			return nil, fmt.Errorf("record blob reclaim: %w", err)
		}
	}

	// 5. Clear stale draft-media references.
	//
	// Found by executing the integration suite against live PostgreSQL:
	// soft-deleting a draft sets post_drafts.status='deleted' but leaves
	// its post_draft_media rows in place (they are what
	// OrphanedDraftMedia uses to FIND reclaimable media). The step-3
	// check correctly ignores those rows, but the FK added by
	// post-service migration 030 does not — it is ON DELETE RESTRICT and
	// sees a live child row, so the DELETE below raised 23503 and orphan
	// media could NEVER be reclaimed.
	//
	// Since step 3 has just proven that every remaining reference belongs
	// to a soft-deleted draft, removing those rows here is safe and is
	// part of the same atomic decision. Rows belonging to surviving
	// drafts are untouched — step 3 would have refused already.
	if _, err := tx.Exec(ctx, `
		DELETE FROM post_draft_media dm
		USING post_drafts d
		WHERE dm.draft_id = d.id
		  AND dm.media_id = $1
		  AND d.status = 'deleted'`, mediaID); err != nil {
		return nil, fmt.Errorf("clear stale draft media references: %w", err)
	}

	// Remove the rows. A foreign_key_violation here means an attacher
	// won the race; surface it as a conflict, not a 500.
	if _, err := tx.Exec(ctx, `DELETE FROM transcoding_jobs WHERE media_asset_id = $1`, mediaID); err != nil {
		return nil, fmt.Errorf("delete transcoding_jobs: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM media_variants WHERE media_asset_id = $1`, mediaID); err != nil {
		return nil, fmt.Errorf("delete variants: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM media_assets WHERE id = $1`, mediaID); err != nil {
		if isForeignKeyViolation(err) {
			return nil, ErrMediaStillReferenced
		}
		return nil, fmt.Errorf("delete media_asset: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		if isForeignKeyViolation(err) {
			return nil, ErrMediaStillReferenced
		}
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return objectKeys, nil
}

// isForeignKeyViolation reports SQLSTATE 23503.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23503"
	}
	return false
}

// ClearBlobReclaim removes a reclaim row once the object is confirmed
// gone. Deleting an absent object is a no-op, so retries are safe.
func (s *MediaAssetStore) ClearBlobReclaim(ctx context.Context, objectKey string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM media_blob_reclaim WHERE object_key = $1`, objectKey)
	return err
}

// RecordBlobReclaimFailure notes a failed attempt for observability.
func (s *MediaAssetStore) RecordBlobReclaimFailure(ctx context.Context, objectKey, reason string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE media_blob_reclaim
		SET attempts = attempts + 1, last_error = $2, updated_at = NOW()
		WHERE object_key = $1`, objectKey, reason)
	return err
}

// PendingBlobReclaims returns object keys awaiting deletion, oldest first.
func (s *MediaAssetStore) PendingBlobReclaims(ctx context.Context, limit int) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT object_key FROM media_blob_reclaim
		ORDER BY created_at ASC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// SetMediaModerationStatus records the safety verdict that gates public
// distribution of a voice post.
func (s *MediaAssetStore) SetMediaModerationStatus(ctx context.Context, mediaID uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE media_assets SET moderation_status = $2 WHERE id = $1`,
		mediaID, status)
	return err
}
