package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/segmentio/kafka-go"
)

// A quarantined request fails its asset (reels-5 remainder, 2026-11-18).
//
// A request the retry loop gives up on (a permanent error before the job
// ran, or the attempts cap) used to get only a media_transcode_quarantine
// row; the asset stayed 'processing' for ever and the creator saw a spinner.
// The quarantine now also records the asset FAILED + manual_review through
// CompleteTranscode, the terminal path every other permanent failure takes
// (inbox row, media row and completion event in one commit, so post-service
// rejects the post), with a 'pipeline' transcoding_jobs row carrying the
// reason the status route shows the creator.
//
// REPLAY. A quarantined asset is replayed with the internal reprocess route,
// which re-queues a FAILED asset with a fresh event id (RequeueTranscode
// moves it back to 'processing'):
//
//	curl -X POST -H "X-Internal-Service-Key: $INTERNAL_KEY" \
//	     https://<media-service>/v1/media/internal/<media_id>/reprocess
//
// It answers 409 TRANSCODE_IN_FLIGHT until the relay has published the
// failure's completion event (seconds); retry then. The quarantine row
// (media_transcode_quarantine: topic, partition_id, offset_id,
// message_value, failure) names the media id in message_value. A message
// that names no parseable media id cannot be replayed this way; its bytes
// stay in the quarantine row for an operator.

// quarantineStore is what quarantineAndFail needs (*postgres.MediaAssetStore).
type quarantineStore interface {
	mediaReader
	requestLookup
	jobRecorder
	QuarantineTranscode(ctx context.Context, m kafka.Message, cause error) error
	CompleteTranscode(ctx context.Context, eventID string, mediaAssetID uuid.UUID, outcome, hlsMasterKey,
		moderationStatus string, completion postgres.TranscodeCompletion) error
}

// quarantineAndFail is the retry loop's quarantine: the durable row first
// (the offset may only advance past a recorded message), then the asset's
// terminal failure. An error from either keeps the offset where it is.
func quarantineAndFail(store quarantineStore) func(context.Context, kafka.Message, error) error {
	return func(ctx context.Context, m kafka.Message, cause error) error {
		if err := store.QuarantineTranscode(ctx, m, cause); err != nil {
			return err
		}
		return failQuarantinedAsset(ctx, store, m, cause)
	}
}

// failQuarantinedAsset records the request's asset failed. Nothing to do for
// a message that is not a readable transcode request, an asset that is gone
// or not a video, or a request a re-queue has superseded.
func failQuarantinedAsset(ctx context.Context, store quarantineStore, m kafka.Message, cause error) error {
	var envelope events.EventEnvelope
	if json.Unmarshal(m.Value, &envelope) != nil || envelope.EventType != events.MediaTranscodeRequested || envelope.EventID == "" {
		return nil
	}
	var payload postgres.TranscodeRequestPayload
	if json.Unmarshal(envelope.Payload, &payload) != nil {
		return nil
	}
	mediaID, err := uuid.Parse(payload.MediaAssetID)
	if err != nil {
		return nil //nolint:nilerr // no media id: the quarantine row is the whole record
	}
	if superseded, _ := supersededDelivery(ctx, store, mediaID, envelope.EventID); superseded {
		return nil
	}
	asset, err := store.GetMedia(ctx, mediaID)
	switch {
	case errors.Is(err, pgx.ErrNoRows) || errors.Is(err, postgres.ErrMediaNotFound) || (err == nil && asset == nil):
		return nil // deleted: nothing to fail
	case err != nil:
		return fmt.Errorf("read quarantined asset %s: %w", mediaID, err)
	case asset.FileType != "video":
		return nil // never touch an asset with no transcode pipeline
	}
	reason := fmt.Errorf("processing stopped after repeated errors and was set aside for review: %w", cause)
	recordJobFailure(ctx, store, mediaID, "pipeline", reason)
	err = store.CompleteTranscode(ctx, envelope.EventID, mediaID, "failed", "", "manual_review",
		postgres.TranscodeCompletion{
			ProcessingStatus: "failed", ModerationStatus: "manual_review",
			MediaGeneration: payload.MediaGeneration,
		})
	switch {
	case err == nil:
		log.Printf("Quarantined transcode of media %s recorded failed; replay with the internal reprocess route", mediaID)
		return nil
	case errors.Is(err, postgres.ErrTranscodeAlreadyApplied),
		errors.Is(err, postgres.ErrTranscodeAssetGone),
		errors.Is(err, postgres.ErrTranscodeGenerationStale):
		return nil
	}
	return fmt.Errorf("record quarantined transcode of %s failed: %w", mediaID, err)
}
