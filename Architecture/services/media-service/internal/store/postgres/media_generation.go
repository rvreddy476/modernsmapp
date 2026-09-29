package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Media generation and upload-time identity (Copyright Match plan P-14,
// migration 022).
//
// Every re-queue bumps media_generation before the new request is relayed
// (requeueTranscodeTx), every completion carries the generation it
// processed, and ready_generation follows only a completion for the current
// generation (completeTranscodeTx). The fingerprint job's final write is
// fenced on media_generation = ready_generation = its own generation
// (fingerprint_jobs.go), so a job that read objects mid-overwrite writes
// nothing.

// UploadTimeConfirmed / UploadTimeLegacy are the upload_time_source values.
const (
	UploadTimeConfirmed = "confirmed"
	UploadTimeLegacy    = "legacy_created_at"
)

// MarkUploadConfirmed moves an asset to 'uploaded' and pins its upload-time
// identity: upload_confirmed_at is set ONCE (never overwritten by a
// re-confirm), the source is 'confirmed', and the original's ETag is
// recorded when the caller has it. Replaces the bare UpdateStatus
// (…,"uploaded") call in ConfirmUpload.
func (s *MediaAssetStore) MarkUploadConfirmed(ctx context.Context, id uuid.UUID, originalETag string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE media_assets
		   SET processing_status    = 'uploaded',
		       upload_confirmed_at  = COALESCE(upload_confirmed_at, NOW()),
		       upload_time_source   = COALESCE(upload_time_source, $2),
		       original_etag        = COALESCE(original_etag, NULLIF($3, '')),
		       updated_at           = NOW()
		 WHERE id = $1
	`, id, UploadTimeConfirmed, originalETag)
	if err != nil {
		return fmt.Errorf("mark upload confirmed for %s: %w", id, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("mark upload confirmed for %s: row not found", id)
	}
	return nil
}

// bumpMediaGenerationTx advances the asset's generation under the caller's
// row lock and retires everything the previous generation produced for
// Copyright Match: queued fingerprint jobs are superseded, stored
// fingerprints are stamped superseded_at, and every active pair on either
// side is invalidated with one pair-outbox row each (generation rule 2).
// Stale postings are left for the sweeper.
func bumpMediaGenerationTx(ctx context.Context, tx pgx.Tx, mediaID uuid.UUID) (int64, error) {
	var generation int64
	if err := tx.QueryRow(ctx, `
		UPDATE media_assets
		   SET media_generation = media_generation + 1
		 WHERE id = $1
		 RETURNING media_generation
	`, mediaID).Scan(&generation); err != nil {
		return 0, fmt.Errorf("bump media generation for %s: %w", mediaID, err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE media_fingerprint_jobs
		   SET status = 'superseded', updated_at = NOW()
		 WHERE media_asset_id = $1
		   AND media_generation < $2
		   AND status IN ('queued', 'claimed')
	`, mediaID, generation); err != nil {
		return 0, fmt.Errorf("supersede fingerprint jobs for %s: %w", mediaID, err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE copyright_fingerprints
		   SET superseded_at = COALESCE(superseded_at, NOW())
		 WHERE media_asset_id = $1
		   AND media_generation < $2
	`, mediaID, generation); err != nil {
		return 0, fmt.Errorf("supersede fingerprints for %s: %w", mediaID, err)
	}
	if _, err := invalidatePairsForMediaTx(ctx, tx, mediaID, PairInvalidatedReprocessed); err != nil {
		return 0, err
	}
	return generation, nil
}

// MediaGenerationState is the snapshot the fingerprint worker selects its
// input from, and the fence it checks before writing.
type MediaGenerationState struct {
	MediaID          uuid.UUID
	FileType         string
	ProcessingStatus string
	MediaGeneration  int64
	ReadyGeneration  *int64
	HLSMasterKey     string
	StorageKey       string
	DurationMs       int
	UploadConfirmed  *time.Time
	UploadTimeSource string
}

// GetMediaGenerationState reads one snapshot.
func (s *MediaAssetStore) GetMediaGenerationState(ctx context.Context, id uuid.UUID) (*MediaGenerationState, error) {
	var st MediaGenerationState
	var durMs, durS *int
	err := s.db.QueryRow(ctx, `
		SELECT id, file_type, processing_status, media_generation, ready_generation,
		       COALESCE(hls_master_key, ''), storage_key, duration_ms, duration_seconds,
		       upload_confirmed_at, COALESCE(upload_time_source, '')
		  FROM media_assets WHERE id = $1
	`, id).Scan(&st.MediaID, &st.FileType, &st.ProcessingStatus, &st.MediaGeneration, &st.ReadyGeneration,
		&st.HLSMasterKey, &st.StorageKey, &durMs, &durS, &st.UploadConfirmed, &st.UploadTimeSource)
	if err != nil {
		return nil, err
	}
	switch {
	case durMs != nil && *durMs > 0:
		st.DurationMs = *durMs
	case durS != nil:
		st.DurationMs = *durS * 1000
	}
	return &st, nil
}

// TranscodeTiming is what the readiness metrics (P-16) are computed from:
// when the bytes were confirmed and when the current transcode request was
// written.
type TranscodeTiming struct {
	UploadConfirmedAt *time.Time
	RequestedAt       *time.Time
}

// GetTranscodeTiming reads the timing anchors for one asset. A missing
// request row (a pre-outbox asset) leaves RequestedAt nil.
func (s *MediaAssetStore) GetTranscodeTiming(ctx context.Context, id uuid.UUID) (TranscodeTiming, error) {
	var t TranscodeTiming
	err := s.db.QueryRow(ctx, `
		SELECT m.upload_confirmed_at,
		       (SELECT o.created_at FROM media_event_outbox o
		         WHERE o.media_asset_id = m.id AND o.event_type = $2)
		  FROM media_assets m WHERE m.id = $1
	`, id, sharedevents.MediaTranscodeRequested).Scan(&t.UploadConfirmedAt, &t.RequestedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, nil
	}
	if err != nil {
		return t, fmt.Errorf("transcode timing for %s: %w", id, err)
	}
	return t, nil
}

// OldestQueuedTranscodeWait is the age of the oldest video that is waiting
// for a worker: processing, its request published, no heartbeat since it
// was queued. Unlike OldestPendingTranscodeAge it ignores assets stuck at
// 'uploaded' (which nothing is going to pick up) and jobs that are running,
// so it is the signal fingerprint admission and the circuit breaker use.
func (s *MediaAssetStore) OldestQueuedTranscodeWait(ctx context.Context) (float64, error) {
	var seconds *float64
	err := s.db.QueryRow(ctx, `
		SELECT EXTRACT(EPOCH FROM (NOW() - MIN(m.updated_at)))
		  FROM media_assets m
		 WHERE m.processing_status = 'processing'
		   AND m.file_type = 'video'
		   AND m.transcode_heartbeat_at IS NULL
		   AND EXISTS (SELECT 1 FROM media_event_outbox o
		                WHERE o.media_asset_id = m.id AND o.event_type = $1 AND o.published_at IS NOT NULL)
	`, sharedevents.MediaTranscodeRequested).Scan(&seconds)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	if seconds == nil {
		return 0, nil
	}
	return *seconds, nil
}
