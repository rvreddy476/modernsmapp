package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Module 4 M4-P0-3 — the durable transcode inbox.
//
// ClaimTranscode and CompleteTranscode bracket the worker's terminal effect:
// the claim proves this event has not already been applied, and the completion
// records the outcome IN THE SAME TRANSACTION as the media row update. An
// offset may only be committed after CompleteTranscode returns.

// ErrTranscodeAlreadyApplied means an earlier delivery already finished this
// event. It is a SUCCESS for the caller: the state is present, so the offset
// may advance.
var ErrTranscodeAlreadyApplied = errors.New("transcode already applied")

type TranscodeCompletion struct {
	ProcessingStatus string
	HLSMasterURL     string
	MP4URL           string
	ThumbnailURL     string
	ModerationStatus string
}

// AlreadyApplied reports whether this event has a recorded terminal outcome.
//
// Checked before the expensive work, so a redelivery does not re-run ffmpeg
// over an asset that is already done. This is an optimisation, not the
// guarantee — the guarantee is the primary key in CompleteTranscode, which is
// what makes two concurrent replicas safe.
func (s *MediaAssetStore) AlreadyApplied(ctx context.Context, eventID string) (bool, error) {
	if eventID == "" {
		// No id means no way to recognise a replay. Process it: doing the work
		// twice is waste, skipping it loses the asset.
		return false, nil
	}
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM media_transcode_inbox WHERE event_id = $1`, eventID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("transcode inbox lookup: %w", err)
	}
	return n > 0, nil
}

// CompleteTranscode records the terminal outcome and the media row state in ONE
// transaction.
//
// If the inbox row already exists this returns ErrTranscodeAlreadyApplied and
// changes nothing — a redelivery cannot overwrite a completed result with a
// stale one, which is the race two replicas would otherwise lose.
func (s *MediaAssetStore) CompleteTranscode(
	ctx context.Context,
	eventID string,
	mediaAssetID uuid.UUID,
	outcome string,
	hlsMasterKey string,
	moderationStatus string,
	completion TranscodeCompletion,
) error {
	switch outcome {
	case "ready", "failed":
	default:
		return fmt.Errorf("invalid transcode outcome %q", outcome)
	}
	// A terminal state must never be recorded as publishable without a real
	// moderation verdict. Empty is not "passed".
	if moderationStatus == "" {
		return fmt.Errorf("transcode completion for %s carries no moderation status", mediaAssetID)
	}
	if eventID == "" {
		return fmt.Errorf("transcode completion for %s has no event id", mediaAssetID)
	}
	completionPayload, err := json.Marshal(sharedevents.MediaTranscodeCompletedPayload{
		MediaAssetID:     mediaAssetID.String(),
		ProcessingStatus: completion.ProcessingStatus,
		HLSMasterURL:     completion.HLSMasterURL,
		MP4URL:           completion.MP4URL,
		ThumbnailURL:     completion.ThumbnailURL,
		ModerationStatus: completion.ModerationStatus,
	})
	if err != nil {
		return fmt.Errorf("marshal transcode completion: %w", err)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transcode completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if eventID != "" {
		ct, err := tx.Exec(ctx, `
			INSERT INTO media_transcode_inbox (event_id, media_asset_id, outcome)
			VALUES ($1, $2, $3)
			ON CONFLICT (event_id) DO NOTHING
		`, eventID, mediaAssetID, outcome)
		if err != nil {
			return fmt.Errorf("claim transcode inbox: %w", err)
		}
		if ct.RowsAffected() == 0 {
			return ErrTranscodeAlreadyApplied
		}
	}

	// processing_status and moderation_status move together with the inbox
	// row. There is no ordering in which the event is recorded applied but the
	// asset is left mid-flight.
	tag, err := tx.Exec(ctx, `
		UPDATE media_assets
		   SET processing_status = $2,
		       moderation_status = $3,
		       hls_master_key    = COALESCE(NULLIF($4, ''), hls_master_key),
		       updated_at        = NOW()
		 WHERE id = $1
	`, mediaAssetID, outcome, moderationStatus, hlsMasterKey)
	if err != nil {
		return fmt.Errorf("update media asset %s: %w", mediaAssetID, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("update media asset %s: row not found", mediaAssetID)
	}

	// Completion notification is part of the same durable effect. A relay
	// publishes it and marks it afterward, so a crash yields a duplicate with
	// this stable ID rather than a permanently missing event.
	if _, err := tx.Exec(ctx, `
		INSERT INTO media_event_outbox
		       (event_id, media_asset_id, event_type, payload)
		VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT (media_asset_id, event_type) DO NOTHING
	`, eventID+":completed", mediaAssetID, sharedevents.MediaTranscodeCompleted, completionPayload); err != nil {
		return fmt.Errorf("insert transcode completion outbox: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transcode completion: %w", err)
	}
	return nil
}

// OldestPendingTranscodeAge is the operational signal that the worker is alive
// and keeping up. A growing value with no failures means the workload is not
// running at all — the exact condition that went unnoticed while the worker had
// no Kubernetes deployment.
func (s *MediaAssetStore) OldestPendingTranscodeAge(ctx context.Context) (float64, error) {
	var seconds *float64
	err := s.db.QueryRow(ctx, `
		SELECT EXTRACT(EPOCH FROM (NOW() - MIN(created_at)))
		  FROM media_assets
		 WHERE processing_status IN ('uploaded', 'processing')
	`).Scan(&seconds)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	if seconds == nil {
		return 0, nil
	}
	return *seconds, nil
}
