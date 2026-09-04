package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MediaAssetStore struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *MediaAssetStore {
	return &MediaAssetStore{db: db}
}

type MediaAsset struct {
	ID               uuid.UUID `json:"id"`
	UploaderID       uuid.UUID `json:"uploader_id"`
	FileType         string    `json:"file_type"`
	MediaSubtype     string    `json:"media_subtype"`
	MimeType         string    `json:"mime_type"`
	FileSizeBytes    int64     `json:"file_size_bytes"`
	StorageBucket    string    `json:"storage_bucket"`
	StorageKey       string    `json:"storage_key"`
	ProcessingStatus string    `json:"processing_status"`
	ModerationStatus string    `json:"moderation_status"`
	Width            *int      `json:"width,omitempty"`
	Height           *int      `json:"height,omitempty"`
	DurationSeconds  *int      `json:"duration_seconds,omitempty"`
	// DurationMs is the ffprobe duration in milliseconds (migration 016).
	// NULL on rows written before it existed; see DurationMsValue.
	DurationMs *int    `json:"duration_ms,omitempty"`
	Blurhash   *string `json:"blurhash,omitempty"`
	AltText    string  `json:"alt_text"`
	// AltDecorative marks media the author explicitly declared decorative
	// — distinct from "not described yet" (Codex P1-7). Hydrated media
	// carries it so every referencing surface can skip it correctly.
	AltDecorative bool `json:"alt_decorative"`
	// UploadPurpose is the composer lease that scopes confirmed-media
	// reclamation (Slice C, C-P0-4). Empty for every other surface, which is
	// exactly what keeps those assets permanently out of the sweep.
	UploadPurpose string         `json:"upload_purpose,omitempty"`
	OriginalURL   *string        `json:"original_url,omitempty"`
	CdnURL        *string        `json:"cdn_url,omitempty"`
	ThumbnailURL  *string        `json:"thumbnail_url,omitempty"`
	HLSMasterKey  string         `json:"hls_master_key,omitempty"`
	IsVertical    bool           `json:"is_vertical"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	Variants      []MediaVariant `json:"variants,omitempty"`
}

type MediaVariant struct {
	MediaAssetID uuid.UUID `json:"media_asset_id"`
	Name         string    `json:"variant"`
	Width        *int      `json:"width,omitempty"`
	Height       *int      `json:"height,omitempty"`
	SizeBytes    *int64    `json:"size_bytes,omitempty"`
	Mime         string    `json:"mime"`
	ObjectKey    string    `json:"object_key"`
	CreatedAt    time.Time `json:"created_at"`
}

// CreateMedia inserts a new media asset record.
func (s *MediaAssetStore) CreateMedia(ctx context.Context, m *MediaAsset) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO media_assets (id, uploader_id, file_type, media_subtype, mime_type, file_size_bytes, storage_bucket, storage_key, processing_status, alt_text, alt_decorative, upload_purpose, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NULLIF($12, ''), $13, $13)
	`, m.ID, m.UploaderID, m.FileType, m.MediaSubtype, m.MimeType, m.FileSizeBytes, m.StorageBucket, m.StorageKey, m.ProcessingStatus, m.AltText, m.AltDecorative, m.UploadPurpose, m.CreatedAt)
	return err
}

// UpdateStatus sets the media processing status.
func (s *MediaAssetStore) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE media_assets SET processing_status = $1, updated_at = NOW() WHERE id = $2
	`, status, id)
	return err
}

// UpdateMediaMeta sets dimensions, blurhash, and optionally duration.
// durationMs is the ffprobe millisecond value; nil (images) leaves both
// duration columns untouched.
func (s *MediaAssetStore) UpdateMediaMeta(ctx context.Context, id uuid.UUID, width, height int, blurhash string, durationSeconds, durationMs *int) error {
	_, err := s.db.Exec(ctx, `
		UPDATE media_assets SET width = $1, height = $2, blurhash = $3,
		       duration_seconds = COALESCE($4, duration_seconds),
		       duration_ms = COALESCE($5, duration_ms),
		       updated_at = NOW()
		WHERE id = $6
	`, width, height, blurhash, durationSeconds, durationMs, id)
	return err
}

// UpdateMediaModerationStatus sets the content-moderation verdict
// ('passed' or 'rejected') the transcode worker's frame scan produced.
func (s *MediaAssetStore) UpdateMediaModerationStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE media_assets SET moderation_status = $1, updated_at = NOW() WHERE id = $2
	`, status, id)
	return err
}

// UpdateMediaOrientation sets the is_vertical flag for a media asset.
func (s *MediaAssetStore) UpdateMediaOrientation(ctx context.Context, id uuid.UUID, isVertical bool) error {
	_, err := s.db.Exec(ctx, `
		UPDATE media_assets SET is_vertical = $1, updated_at = NOW() WHERE id = $2
	`, isVertical, id)
	return err
}

// GetMedia fetches a single media asset record by ID.
func (s *MediaAssetStore) GetMedia(ctx context.Context, id uuid.UUID) (*MediaAsset, error) {
	var m MediaAsset
	err := s.db.QueryRow(ctx, `
		SELECT id, uploader_id, file_type, media_subtype, mime_type, file_size_bytes, storage_bucket, storage_key, processing_status, COALESCE(moderation_status, 'pending'),
		       width, height, duration_seconds, duration_ms, blurhash, alt_text, COALESCE(alt_decorative,FALSE), original_url, cdn_url, thumbnail_url,
		       COALESCE(hls_master_key, ''), is_vertical, created_at, updated_at
		FROM media_assets WHERE id = $1
	`, id).Scan(
		&m.ID, &m.UploaderID, &m.FileType, &m.MediaSubtype, &m.MimeType, &m.FileSizeBytes, &m.StorageBucket, &m.StorageKey, &m.ProcessingStatus, &m.ModerationStatus,
		&m.Width, &m.Height, &m.DurationSeconds, &m.DurationMs, &m.Blurhash, &m.AltText, &m.AltDecorative, &m.OriginalURL, &m.CdnURL, &m.ThumbnailURL,
		&m.HLSMasterKey, &m.IsVertical, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// UpdateHLSMasterKey sets the hls_master_key for a media asset.
func (s *MediaAssetStore) UpdateHLSMasterKey(ctx context.Context, id uuid.UUID, key string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE media_assets SET hls_master_key = $1, updated_at = NOW() WHERE id = $2
	`, key, id)
	return err
}

// GetMediaWithVariants fetches media and all its variants.
func (s *MediaAssetStore) GetMediaWithVariants(ctx context.Context, id uuid.UUID) (*MediaAsset, error) {
	m, err := s.GetMedia(ctx, id)
	if err != nil {
		return nil, err
	}
	variants, err := s.GetVariants(ctx, id)
	if err != nil {
		return nil, err
	}
	m.Variants = variants
	return m, nil
}

// InsertVariants batch-inserts variant records for a media asset.
func (s *MediaAssetStore) InsertVariants(ctx context.Context, variants []MediaVariant) error {
	if len(variants) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, v := range variants {
		batch.Queue(`
			INSERT INTO media_variants (media_asset_id, variant, width, height, size_bytes, mime, object_key, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
			ON CONFLICT (media_asset_id, variant) DO UPDATE SET
				width = EXCLUDED.width, height = EXCLUDED.height,
				size_bytes = EXCLUDED.size_bytes, object_key = EXCLUDED.object_key
		`, v.MediaAssetID, v.Name, v.Width, v.Height, v.SizeBytes, v.Mime, v.ObjectKey)
	}
	br := s.db.SendBatch(ctx, batch)
	defer br.Close()
	for range variants {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// GetVariants returns all variants for a media asset.
func (s *MediaAssetStore) GetVariants(ctx context.Context, mediaAssetID uuid.UUID) ([]MediaVariant, error) {
	rows, err := s.db.Query(ctx, `
		SELECT media_asset_id, variant, width, height, size_bytes, mime, object_key, created_at
		FROM media_variants WHERE media_asset_id = $1
		ORDER BY variant
	`, mediaAssetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var variants []MediaVariant
	for rows.Next() {
		var v MediaVariant
		if err := rows.Scan(&v.MediaAssetID, &v.Name, &v.Width, &v.Height, &v.SizeBytes, &v.Mime, &v.ObjectKey, &v.CreatedAt); err != nil {
			return nil, err
		}
		variants = append(variants, v)
	}
	return variants, nil
}

// GetMediaBatch fetches multiple media asset records with their variants.
func (s *MediaAssetStore) GetMediaBatch(ctx context.Context, ids []uuid.UUID) ([]MediaAsset, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	rows, err := s.db.Query(ctx, `
		SELECT id, uploader_id, file_type, media_subtype, mime_type, file_size_bytes, storage_bucket, storage_key, processing_status, COALESCE(moderation_status, 'pending'),
		       width, height, duration_seconds, duration_ms, blurhash, alt_text, COALESCE(alt_decorative,FALSE), original_url, cdn_url, thumbnail_url,
		       COALESCE(hls_master_key, ''), is_vertical, created_at, updated_at
		FROM media_assets WHERE id = ANY($1)
	`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	mediaMap := make(map[uuid.UUID]int, len(ids))
	var result []MediaAsset
	for rows.Next() {
		var m MediaAsset
		if err := rows.Scan(
			&m.ID, &m.UploaderID, &m.FileType, &m.MediaSubtype, &m.MimeType, &m.FileSizeBytes, &m.StorageBucket, &m.StorageKey, &m.ProcessingStatus, &m.ModerationStatus,
			&m.Width, &m.Height, &m.DurationSeconds, &m.DurationMs, &m.Blurhash, &m.AltText, &m.AltDecorative, &m.OriginalURL, &m.CdnURL, &m.ThumbnailURL,
			&m.HLSMasterKey, &m.IsVertical, &m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, m)
		mediaMap[m.ID] = len(result) - 1
	}

	// Batch-load variants
	vRows, err := s.db.Query(ctx, `
		SELECT media_asset_id, variant, width, height, size_bytes, mime, object_key, created_at
		FROM media_variants WHERE media_asset_id = ANY($1)
		ORDER BY media_asset_id, variant
	`, ids)
	if err != nil {
		return result, nil // Return media without variants on error
	}
	defer vRows.Close()

	for vRows.Next() {
		var v MediaVariant
		if err := vRows.Scan(&v.MediaAssetID, &v.Name, &v.Width, &v.Height, &v.SizeBytes, &v.Mime, &v.ObjectKey, &v.CreatedAt); err != nil {
			continue
		}
		if idx, ok := mediaMap[v.MediaAssetID]; ok {
			result[idx].Variants = append(result[idx].Variants, v)
		}
	}

	return result, nil
}

// ─── Transcoding Jobs ──────────────────────────────────────────────

type TranscodingJob struct {
	ID              uuid.UUID  `json:"id"`
	MediaAssetID    uuid.UUID  `json:"media_asset_id"`
	TargetQuality   string     `json:"target_quality"`
	Status          string     `json:"status"`
	OutputURL       *string    `json:"output_url,omitempty"`
	OutputSizeBytes *int64     `json:"output_size_bytes,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	ErrorMessage    *string    `json:"error_message,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

// CreateTranscodingJob inserts a new transcoding job record.
func (s *MediaAssetStore) CreateTranscodingJob(ctx context.Context, job *TranscodingJob) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO transcoding_jobs (id, media_asset_id, target_quality, status, created_at)
		VALUES ($1, $2, $3, $4, NOW())
	`, job.ID, job.MediaAssetID, job.TargetQuality, job.Status)
	return err
}

// UpdateTranscodingJob updates the status and optional fields of a transcoding job.
func (s *MediaAssetStore) UpdateTranscodingJob(ctx context.Context, jobID uuid.UUID, status string, outputURL *string, outputSizeBytes *int64, errorMessage *string) error {
	var completedAt, startedAt interface{}
	if status == "processing" {
		now := time.Now()
		startedAt = &now
	}
	if status == "completed" || status == "failed" {
		now := time.Now()
		completedAt = &now
	}

	_, err := s.db.Exec(ctx, `
		UPDATE transcoding_jobs SET
			status = $1,
			output_url = COALESCE($2, output_url),
			output_size_bytes = COALESCE($3, output_size_bytes),
			error_message = COALESCE($4, error_message),
			started_at = COALESCE($5, started_at),
			completed_at = COALESCE($6, completed_at)
		WHERE id = $7
	`, status, outputURL, outputSizeBytes, errorMessage, startedAt, completedAt, jobID)
	return err
}

// GetTranscodingJobs returns all transcoding jobs for a media asset.
func (s *MediaAssetStore) GetTranscodingJobs(ctx context.Context, mediaAssetID uuid.UUID) ([]TranscodingJob, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, media_asset_id, target_quality, status, output_url, output_size_bytes,
		       started_at, completed_at, error_message, created_at
		FROM transcoding_jobs WHERE media_asset_id = $1
		ORDER BY created_at
	`, mediaAssetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []TranscodingJob
	for rows.Next() {
		var j TranscodingJob
		if err := rows.Scan(
			&j.ID, &j.MediaAssetID, &j.TargetQuality, &j.Status,
			&j.OutputURL, &j.OutputSizeBytes, &j.StartedAt, &j.CompletedAt,
			&j.ErrorMessage, &j.CreatedAt,
		); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, nil
}

// ─── Delete ────────────────────────────────────────────────────────

// DeleteMedia removes a media asset and all its variants and transcoding jobs.
// Returns the list of object keys that were associated (for blob cleanup).
func (s *MediaAssetStore) DeleteMedia(ctx context.Context, id uuid.UUID) ([]string, error) {
	// 1. Collect all object keys for blob cleanup
	var objectKeys []string

	var storageKey string
	err := s.db.QueryRow(ctx, `SELECT storage_key FROM media_assets WHERE id = $1`, id).Scan(&storageKey)
	if err != nil {
		return nil, fmt.Errorf("fetch media storage_key: %w", err)
	}
	objectKeys = append(objectKeys, storageKey)

	rows, err := s.db.Query(ctx, `SELECT object_key FROM media_variants WHERE media_asset_id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("fetch variant keys: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		objectKeys = append(objectKeys, key)
	}

	// 2. Delete in correct FK order within a transaction
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM transcoding_jobs WHERE media_asset_id = $1`, id); err != nil {
		return nil, fmt.Errorf("delete transcoding_jobs: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM media_variants WHERE media_asset_id = $1`, id); err != nil {
		return nil, fmt.Errorf("delete variants: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM media_assets WHERE id = $1`, id); err != nil {
		return nil, fmt.Errorf("delete media_asset: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	return objectKeys, nil
}

// ListReclaimableMedia returns reclamation candidates — Slice C, C-CLB-1.
//
// # ONLY `pending_upload`, AND WHY THAT IS A RETREAT ON PURPOSE
//
// An earlier version of this query also returned CONFIRMED assets carrying the
// composer lease, so an abandoned-but-uploaded composer photo could be swept.
// The lease scoping was right as far as it went — it stopped the sweep being a
// global collector for every old ready asset — but scoping decides WHICH
// confirmed assets are eligible, and the unsafe part was reclaiming a confirmed
// asset at all.
//
// The review proved it against live PostgreSQL. A reclaim transaction locked a
// ready, composer-leased media row, saw no reference, and held the lock; a
// concurrent writer set `users.avatar_media_id` to that asset and committed in
// 304 ms, unblocked, because a plain UUID column takes no lock on the media
// row. The reclaim then deleted the asset and committed. Final state: media row
// gone, avatar reference still pointing at it. No error anywhere.
//
// So confirmed reclamation is OFF until every live-reference writer joins a
// claim protocol. See ErrMediaConfirmed for the full reasoning and the cost.
//
// `pending_upload` reclamation — the original audit-H9 case — is untouched and
// still runs. Nothing can reference an asset whose bytes never arrived.
//
// # THE LIVE-REFERENCE PREDICATE IS STILL APPLIED
//
// Kept, even though a `pending_upload` asset should never have a live claim.
// It is a cheap contradiction check: if one of these ever DOES carry a
// reference, something upstream is wrong in a way that should stop the delete,
// not proceed with it. It also fails closed on an unclassified media-referencing
// column, so a new claim added by another team makes this sweep do nothing
// rather than delete something nobody has classified.
//
// Selection here is only a candidate list — DeleteOrphanMediaAtomic re-checks
// age, status and references under a row lock before anything is removed.
func (s *MediaAssetStore) ListReclaimableMedia(ctx context.Context, olderThan time.Time, limit int) ([]uuid.UUID, error) {
	if limit <= 0 {
		limit = 500
	}

	refs, resolveErr := ResolveLiveReferences(ctx, s.db)
	if resolveErr != nil {
		return nil, resolveErr
	}

	query := `
		SELECT id FROM media_assets m
		WHERE m.created_at < $1
		  AND m.processing_status = $3
		  AND NOT (` + liveReferenceSQL(refs, "m.id") + `)
		ORDER BY m.created_at ASC
		LIMIT $2`
	rows, err := s.db.Query(ctx, query, olderThan, limit, ProcessingStatusPendingUpload)
	if err != nil {
		return nil, fmt.Errorf("list reclaimable media: %w", err)
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

// ListOrphanedPendingUploads returns the IDs of media assets stuck
// in `pending_upload` past the cutoff time — audit H9. The 3-step
// upload flow has a window where a client crashes between
// /v1/media/init and /v1/media/confirm; the row sits at
// `pending_upload` forever with no GC. The OrphanGCWorker polls this
// every 15 min and purges anything older than 24 h.
//
// Bounded by `limit` so a backlog doesn't pin the connection. Ordered
// by created_at ASC so the oldest orphans get cleaned first.
func (s *MediaAssetStore) ListOrphanedPendingUploads(ctx context.Context, olderThan time.Time, limit int) ([]uuid.UUID, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.db.Query(ctx, `
		SELECT id FROM media_assets
		WHERE processing_status = 'pending_upload'
		  AND created_at < $1
		ORDER BY created_at ASC
		LIMIT $2
	`, olderThan, limit)
	if err != nil {
		return nil, fmt.Errorf("list orphan pending uploads: %w", err)
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

// SoftDeleteMediaByUploader marks all media assets belonging to the given uploader
// as deleted (GDPR right-to-erasure). The actual blob objects are not removed here;
// a separate background job should purge them using the stored object keys.
func (s *MediaAssetStore) SoftDeleteMediaByUploader(ctx context.Context, uploaderID string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE media_assets SET processing_status = 'deleted', updated_at = NOW()
		 WHERE uploader_id = $1::uuid`,
		uploaderID)
	return err
}

// ─── URL Population ────────────────────────────────────────────────

// UpdateMediaURLs sets the original_url, cdn_url, and thumbnail_url for a media asset.
func (s *MediaAssetStore) UpdateMediaURLs(ctx context.Context, id uuid.UUID, originalURL, cdnURL, thumbnailURL *string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE media_assets SET original_url = $1, cdn_url = $2, thumbnail_url = $3, updated_at = NOW()
		WHERE id = $4
	`, originalURL, cdnURL, thumbnailURL, id)
	return err
}

// UpdateAltText sets the alt_text field for a media asset owned by uploaderID.
// Returns pgx.ErrNoRows if the asset does not exist or is not owned by uploaderID.
func (s *MediaAssetStore) UpdateAltText(ctx context.Context, id uuid.UUID, uploaderID uuid.UUID, altText string) error {
	return s.UpdateAltTextWithDecorative(ctx, id, uploaderID, altText, false)
}

// UpdateAltTextWithDecorative sets the description and the decorative
// marker together (Module 1 fixes-v1 / Codex P1-7). The two are mutually
// exclusive: marking decorative clears any description, and supplying a
// description clears the decorative marker — so "described" and
// "intentionally undescribed" stay distinguishable after publish.
func (s *MediaAssetStore) UpdateAltTextWithDecorative(ctx context.Context, id uuid.UUID, uploaderID uuid.UUID, altText string, decorative bool) error {
	if decorative {
		altText = ""
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE media_assets
		SET alt_text = $1, alt_decorative = $4, updated_at = NOW()
		WHERE id = $2 AND uploader_id = $3
	`, altText, id, uploaderID, decorative)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// DurationMsValue is the duration to put on the wire: the ffprobe
// millisecond value when the row has one, else the legacy whole-second
// column scaled up, else 0 ("unknown" — callers omit it).
func (m *MediaAsset) DurationMsValue() int {
	if m == nil {
		return 0
	}
	if m.DurationMs != nil && *m.DurationMs > 0 {
		return *m.DurationMs
	}
	if m.DurationSeconds != nil && *m.DurationSeconds > 0 {
		return *m.DurationSeconds * 1000
	}
	return 0
}
