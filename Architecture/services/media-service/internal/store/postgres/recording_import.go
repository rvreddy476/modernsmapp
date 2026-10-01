package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Live recordings become media assets (1 Oct 2026, migration 025). The
// service half is internal/service/recording_import.go.

// UploadPurposeLiveRecording is the upload_purpose of an imported live
// recording. It is NOT a reclamation lease: confirmed reclamation is scoped
// to UploadPurposeComposer only, and an imported row is born 'processing',
// never 'pending_upload', so neither sweep (ListReclaimableMedia,
// ListOrphanedPendingUploads) can select it. The value says where the asset
// came from; normaliseUploadPurpose never lets a client claim it.
const UploadPurposeLiveRecording = "live_recording"

// ImportSourceLiveRecording is the import_source of a live recording; its
// import_source_ref is the stream id.
const ImportSourceLiveRecording = "live_recording"

// ImportedMedia is what an import lookup reports about an existing asset.
type ImportedMedia struct {
	ID               uuid.UUID
	UploaderID       uuid.UUID
	ProcessingStatus string
}

// FindImportedMedia returns the asset an earlier import of (source, ref)
// created, or nil when there is none.
func (s *MediaAssetStore) FindImportedMedia(ctx context.Context, source, ref string) (*ImportedMedia, error) {
	var m ImportedMedia
	err := s.db.QueryRow(ctx, `
		SELECT id, uploader_id, processing_status
		  FROM media_assets
		 WHERE import_source = $1 AND import_source_ref = $2
	`, source, ref).Scan(&m.ID, &m.UploaderID, &m.ProcessingStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find imported media %s/%s: %w", source, ref, err)
	}
	return &m, nil
}

// ImportedVideo is one copied recording to register.
type ImportedVideo struct {
	ID            uuid.UUID
	OwnerID       uuid.UUID
	MimeType      string
	SizeBytes     int64
	StorageBucket string
	StorageKey    string
	OriginalETag  string
	UploadPurpose string
	Source        string
	SourceRef     string
}

// CreateImportedVideo registers an already-copied video and queues its
// transcode in ONE transaction: the row is born 'processing' with the
// media.transcode.requested outbox row beside it, exactly the state
// ConfirmUpload + QueueTranscode leave an uploaded video in, so the relay,
// the worker, HLS and the copyright fingerprint (enqueued on a ready
// completion) all run as for any upload. moderation_status takes the column
// default, 'pending'.
//
// Idempotent on (import_source, import_source_ref). When another import of
// the same pair committed first, nothing is written and the existing asset
// is returned with created=false; the caller then owns the object it copied
// for the losing attempt.
func (s *MediaAssetStore) CreateImportedVideo(ctx context.Context, in ImportedVideo) (*ImportedMedia, bool, error) {
	if in.ID == uuid.Nil || in.OwnerID == uuid.Nil {
		return nil, false, fmt.Errorf("create imported video: invalid identity")
	}
	if strings.TrimSpace(in.Source) == "" || strings.TrimSpace(in.SourceRef) == "" {
		return nil, false, fmt.Errorf("create imported video: source and source_ref are required")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin import: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var generation int64
	err = tx.QueryRow(ctx, `
		INSERT INTO media_assets
		       (id, uploader_id, file_type, media_subtype, mime_type, file_size_bytes,
		        storage_bucket, storage_key, processing_status, alt_text, alt_decorative,
		        upload_purpose, import_source, import_source_ref,
		        upload_confirmed_at, upload_time_source, original_etag,
		        created_at, updated_at)
		VALUES ($1, $2, 'video', 'general', $3, $4,
		        $5, $6, 'processing', '', FALSE,
		        NULLIF($7, ''), $8, $9,
		        NOW(), $10, NULLIF($11, ''),
		        NOW(), NOW())
		ON CONFLICT (import_source, import_source_ref) WHERE import_source IS NOT NULL DO NOTHING
		RETURNING media_generation
	`, in.ID, in.OwnerID, in.MimeType, in.SizeBytes,
		in.StorageBucket, in.StorageKey,
		in.UploadPurpose, in.Source, in.SourceRef,
		UploadTimeConfirmed, in.OriginalETag).Scan(&generation)
	if errors.Is(err, pgx.ErrNoRows) {
		// Lost the race (or a retry after a commit): report the winner.
		_ = tx.Rollback(ctx)
		existing, ferr := s.FindImportedMedia(ctx, in.Source, in.SourceRef)
		if ferr != nil {
			return nil, false, ferr
		}
		if existing == nil {
			return nil, false, fmt.Errorf("create imported video: conflict on %s/%s but no row", in.Source, in.SourceRef)
		}
		return existing, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("insert imported video: %w", err)
	}

	if err := insertTranscodeRequestTx(ctx, tx, &MediaAsset{
		ID:         in.ID,
		UploaderID: in.OwnerID,
		StorageKey: in.StorageKey,
		MimeType:   in.MimeType,
	}, generation); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit import: %w", err)
	}
	return &ImportedMedia{ID: in.ID, UploaderID: in.OwnerID, ProcessingStatus: "processing"}, true, nil
}
