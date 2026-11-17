package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Per-user video upload budget (lv-3, migration 034). The ledger
// media_upload_usage holds one row per confirmed video, written in the same
// transaction that checked the budget under a per-user advisory lock, so two
// confirms of one account cannot both pass a budget only one of them fits.
// The ledger has no foreign key: deleting a video does not refund it.

// UploadUsage is one account's video usage: bytes and known source
// milliseconds charged since a time, and its in-flight long videos (charged
// as long and still 'uploaded' or 'processing').
type UploadUsage struct {
	Bytes        int64
	DurationMs   int64
	InflightLong int
}

// UploadQuotaOverride is one account's raised (or lowered) limits; a nil
// field keeps the deployment default.
type UploadQuotaOverride struct {
	DailyVideoBytes   *int64
	DailyVideoSeconds *int64
	MaxInflightLong   *int
}

// VideoUploadCharge is what one confirmed video costs.
type VideoUploadCharge struct {
	MediaID    uuid.UUID
	UploaderID uuid.UUID
	Bytes      int64
	// DurationMs is the probed source length; 0 means unknown.
	DurationMs int64
	IsLong     bool
}

// ErrUploadAlreadyCharged: AdmitVideoUpload found this media id already in
// the ledger (a retried confirm). It is not a refusal.
var ErrUploadAlreadyCharged = errors.New("upload already charged")

const uploadUsageSQL = `
	SELECT COALESCE(SUM(u.bytes) FILTER (WHERE u.charged_at >= $2), 0),
	       COALESCE(SUM(u.duration_ms) FILTER (WHERE u.charged_at >= $2), 0),
	       COUNT(*) FILTER (WHERE u.is_long AND m.processing_status IN ('uploaded', 'processing'))
	  FROM media_upload_usage u
	  LEFT JOIN media_assets m ON m.id = u.media_id
	 WHERE u.uploader_id = $1 AND u.media_id <> $3`

const uploadOverrideSQL = `
	SELECT daily_video_bytes, daily_video_seconds, max_inflight_long
	  FROM media_upload_quota_overrides WHERE user_id = $1`

type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func readUploadUsage(ctx context.Context, q queryRower, userID uuid.UUID, since time.Time, exclude uuid.UUID) (UploadUsage, *UploadQuotaOverride, error) {
	var u UploadUsage
	if err := q.QueryRow(ctx, uploadUsageSQL, userID, since, exclude).Scan(&u.Bytes, &u.DurationMs, &u.InflightLong); err != nil {
		return UploadUsage{}, nil, fmt.Errorf("read upload usage: %w", err)
	}
	var o UploadQuotaOverride
	err := q.QueryRow(ctx, uploadOverrideSQL, userID).Scan(&o.DailyVideoBytes, &o.DailyVideoSeconds, &o.MaxInflightLong)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, nil, nil
	}
	if err != nil {
		return UploadUsage{}, nil, fmt.Errorf("read upload quota override: %w", err)
	}
	return u, &o, nil
}

// UploadUsageFor is the account's usage since `since` (excluding one media
// id, uuid.Nil for none) and its override, if any. Read-only: upload init
// checks a declared size against it without charging.
func (s *MediaAssetStore) UploadUsageFor(ctx context.Context, userID uuid.UUID, since time.Time, exclude uuid.UUID) (UploadUsage, *UploadQuotaOverride, error) {
	return readUploadUsage(ctx, s.db, userID, since, exclude)
}

// AdmitVideoUpload charges c to its uploader when decide accepts the
// account's usage (excluding c itself) and override, all under one per-user
// advisory lock. A media id already charged returns ErrUploadAlreadyCharged
// without calling decide; decide's error is returned unchanged and nothing
// is written.
func (s *MediaAssetStore) AdmitVideoUpload(ctx context.Context, c VideoUploadCharge, since time.Time,
	decide func(UploadUsage, *UploadQuotaOverride) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('media-upload-quota:' || $1::text, 0))`,
		c.UploaderID); err != nil {
		return fmt.Errorf("lock upload quota: %w", err)
	}
	var charged bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM media_upload_usage WHERE media_id = $1)`,
		c.MediaID).Scan(&charged); err != nil {
		return fmt.Errorf("read upload charge: %w", err)
	}
	if charged {
		return ErrUploadAlreadyCharged
	}
	usage, override, err := readUploadUsage(ctx, tx, c.UploaderID, since, c.MediaID)
	if err != nil {
		return err
	}
	if err := decide(usage, override); err != nil {
		return err
	}
	var duration *int64
	if c.DurationMs > 0 {
		duration = &c.DurationMs
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO media_upload_usage (media_id, uploader_id, bytes, duration_ms, is_long)
		VALUES ($1, $2, $3, $4, $5)`,
		c.MediaID, c.UploaderID, c.Bytes, duration, c.IsLong); err != nil {
		return fmt.Errorf("charge upload: %w", err)
	}
	return tx.Commit(ctx)
}

// TouchResumableUpload slides an OPEN session's expiry on activity (lv-16):
// expires_at = LEAST(created_at + maxLifetime, GREATEST(expires_at, now +
// idleTTL)). It never shortens a session and never takes one past its
// ceiling. It returns the new expiry, or pgx.ErrNoRows when the session is
// not open (completed, expired, aborted) or already past its expiry: the
// same rule ExpireResumableUploads closes sessions by.
func (s *MediaAssetStore) TouchResumableUpload(ctx context.Context, uploadID uuid.UUID, idleTTL, maxLifetime time.Duration) (time.Time, error) {
	var exp time.Time
	err := s.db.QueryRow(ctx, `
		UPDATE resumable_uploads
		   SET expires_at = LEAST(created_at + make_interval(secs => $3),
		                          GREATEST(expires_at, NOW() + make_interval(secs => $2))),
		       updated_at = NOW()
		 WHERE upload_id = $1
		   AND status IN ('initiated', 'uploading')
		   AND expires_at > NOW()
		RETURNING expires_at`, uploadID, idleTTL.Seconds(), maxLifetime.Seconds()).Scan(&exp)
	return exp, err
}

// OpenResumableMediaIDs is the subset of ids whose asset has an open,
// unexpired resumable session: orphan GC leaves those alone (lv-16), so a
// session that outlives 24 h of activity keeps its asset.
func (s *MediaAssetStore) OpenResumableMediaIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT media_id FROM resumable_uploads
		 WHERE media_id = ANY($1) AND status IN ('initiated', 'uploading') AND expires_at > NOW()`, ids)
	if err != nil {
		return nil, fmt.Errorf("list open resumable sessions: %w", err)
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
