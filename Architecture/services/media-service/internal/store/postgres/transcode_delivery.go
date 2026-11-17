package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Transcode delivery start counter (migration 033, long-video audit lv-6).
// The policy (how many lost runs are allowed) lives in the worker; the store
// only counts.

// BeginTranscodeDelivery records that a run of request eventID is starting
// on mediaID and returns the counts AFTER this start. A request other than
// the one counted so far resets both counts. ErrMediaNotFound when the asset
// row is gone.
func (s *MediaAssetStore) BeginTranscodeDelivery(ctx context.Context, mediaID uuid.UUID, eventID string) (starts, finishes int, err error) {
	err = s.db.QueryRow(ctx, `
		UPDATE media_assets SET
		    transcode_delivery_starts = CASE WHEN transcode_delivery_event IS DISTINCT FROM $2
		                                     THEN 1 ELSE transcode_delivery_starts + 1 END,
		    transcode_delivery_finishes = CASE WHEN transcode_delivery_event IS DISTINCT FROM $2
		                                       THEN 0 ELSE transcode_delivery_finishes END,
		    transcode_delivery_drains = CASE WHEN transcode_delivery_event IS DISTINCT FROM $2
		                                     THEN 0 ELSE transcode_delivery_drains END,
		    transcode_delivery_event = $2
		 WHERE id = $1
		RETURNING transcode_delivery_starts, transcode_delivery_finishes
	`, mediaID, eventID).Scan(&starts, &finishes)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, ErrMediaNotFound
	}
	if err != nil {
		return 0, 0, fmt.Errorf("count transcode start for %s: %w", mediaID, err)
	}
	return starts, finishes, nil
}

// FinishTranscodeDelivery records that a run of request eventID returned
// (any outcome, including a clean drain). A no-op when the asset is gone or
// a newer request has taken over the counts.
func (s *MediaAssetStore) FinishTranscodeDelivery(ctx context.Context, mediaID uuid.UUID, eventID string) error {
	if _, err := s.db.Exec(ctx, `
		UPDATE media_assets SET transcode_delivery_finishes = transcode_delivery_finishes + 1
		 WHERE id = $1 AND transcode_delivery_event = $2
	`, mediaID, eventID); err != nil {
		return fmt.Errorf("count transcode finish for %s: %w", mediaID, err)
	}
	return nil
}

// DrainTranscodeDelivery records that a run of request eventID began a
// graceful drain (SIGTERM; migration 035, REG-2). It is written BEFORE the
// worker waits out its drain window and counts as that run's FINISH (plus
// one drain), so a run the kubelet kills at the end of the grace period is
// not a lost worker. The worker writes either this or
// FinishTranscodeDelivery for one run, never both. A no-op when the asset is
// gone or a newer request has taken over the counts.
func (s *MediaAssetStore) DrainTranscodeDelivery(ctx context.Context, mediaID uuid.UUID, eventID string) error {
	if _, err := s.db.Exec(ctx, `
		UPDATE media_assets SET transcode_delivery_finishes = transcode_delivery_finishes + 1,
		                        transcode_delivery_drains = transcode_delivery_drains + 1
		 WHERE id = $1 AND transcode_delivery_event = $2
	`, mediaID, eventID); err != nil {
		return fmt.Errorf("count transcode drain for %s: %w", mediaID, err)
	}
	return nil
}
