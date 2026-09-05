package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/segmentio/kafka-go"
)

// MediaOutboxEvent is the durable representation of an event awaiting Kafka.
// EventID is generated once in PostgreSQL and is reused on every publish retry.
type MediaOutboxEvent struct {
	EventID     string
	EventType   string
	ActorUserID *string
	Payload     json.RawMessage
	OccurredAt  time.Time
}

// QueueTranscode atomically moves an uploaded video to processing and records
// its transcode request. A successful confirm can therefore never leave a
// processing asset without durable work waiting for the relay.
func (s *MediaAssetStore) QueueTranscode(ctx context.Context, media *MediaAsset) error {
	if media == nil || media.ID == uuid.Nil || media.UploaderID == uuid.Nil {
		return fmt.Errorf("queue transcode: invalid media identity")
	}
	payload, err := json.Marshal(sharedevents.MediaTranscodeRequestedPayload{
		MediaAssetID: media.ID.String(),
		UploaderID:   media.UploaderID.String(),
		StorageKey:   media.StorageKey,
		MimeType:     media.MimeType,
	})
	if err != nil {
		return fmt.Errorf("queue transcode payload: %w", err)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin queue transcode: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	eventID := uuid.NewString()
	if _, err := tx.Exec(ctx, `
		INSERT INTO media_event_outbox
		       (event_id, media_asset_id, event_type, actor_user_id, payload)
		VALUES ($1, $2, $3, $4, $5::jsonb)
		ON CONFLICT (media_asset_id, event_type) DO NOTHING
	`, eventID, media.ID, sharedevents.MediaTranscodeRequested, media.UploaderID, payload); err != nil {
		return fmt.Errorf("insert transcode request outbox: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		UPDATE media_assets
		   SET processing_status = 'processing', updated_at = NOW()
		 WHERE id = $1
		   AND processing_status IN ('uploaded','processing')
	`, media.ID)
	if err != nil {
		return fmt.Errorf("mark media processing: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("queue transcode: media %s is not in an uploadable state", media.ID)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit queue transcode: %w", err)
	}
	return nil
}

// PendingMediaEvents returns unpublished events. Multiple relays may read the
// same row; that intentionally yields a duplicate with the same event_id, not
// loss. Consumers use their transactional inbox to collapse the duplicate.
func (s *MediaAssetStore) PendingMediaEvents(ctx context.Context, eventType string, limit int) ([]MediaOutboxEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
		SELECT event_id, event_type, actor_user_id::text, payload, occurred_at
		  FROM media_event_outbox
		 WHERE published_at IS NULL
		   AND ($1 = '' OR event_type = $1)
		 ORDER BY created_at
		 LIMIT $2
	`, eventType, limit)
	if err != nil {
		return nil, fmt.Errorf("list media outbox: %w", err)
	}
	defer rows.Close()

	out := make([]MediaOutboxEvent, 0, limit)
	for rows.Next() {
		var e MediaOutboxEvent
		if err := rows.Scan(&e.EventID, &e.EventType, &e.ActorUserID, &e.Payload, &e.OccurredAt); err != nil {
			return nil, fmt.Errorf("scan media outbox: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *MediaAssetStore) MarkMediaEventPublished(ctx context.Context, eventID string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE media_event_outbox
		   SET published_at = COALESCE(published_at, NOW()),
		       attempts = attempts + 1,
		       last_error = NULL
		 WHERE event_id = $1
	`, eventID)
	if err != nil {
		return fmt.Errorf("mark media event published: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("mark media event published: event %q not found", eventID)
	}
	return nil
}

func (s *MediaAssetStore) RecordMediaEventFailure(ctx context.Context, eventID string, cause error) error {
	_, err := s.db.Exec(ctx, `
		UPDATE media_event_outbox
		   SET attempts = attempts + 1, last_error = $2
		 WHERE event_id = $1 AND published_at IS NULL
	`, eventID, cause.Error())
	return err
}

// QuarantineTranscode durably records an unprocessable broker message. The
// worker is allowed to commit its offset only after this succeeds.
func (s *MediaAssetStore) QuarantineTranscode(ctx context.Context, m kafka.Message, cause error) error {
	if cause == nil {
		return fmt.Errorf("quarantine transcode: missing failure")
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO media_transcode_quarantine
		       (topic, partition_id, offset_id, message_key, message_value, failure)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (topic, partition_id, offset_id) DO NOTHING
	`, m.Topic, m.Partition, m.Offset, m.Key, m.Value, cause.Error())
	if err != nil {
		return fmt.Errorf("quarantine transcode: %w", err)
	}
	return nil
}

// WithTranscodeEventLock serializes duplicate/rebalanced deliveries of one
// event across worker replicas. PostgreSQL releases this session advisory lock
// if the worker dies, so it has neither a stale lease nor a split-brain window.
func (s *MediaAssetStore) WithTranscodeEventLock(ctx context.Context, eventID string, fn func() error) error {
	if eventID == "" {
		return fmt.Errorf("transcode event has no event_id")
	}
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire transcode lock connection: %w", err)
	}
	defer conn.Release()

	h := fnv.New64a()
	_, _ = h.Write([]byte("media-transcode:" + eventID))
	key := int64(h.Sum64())
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		return fmt.Errorf("acquire transcode event lock: %w", err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, key) }()
	return fn()
}

// Compile-time check that the row scanner behavior used above retains nullable
// actor IDs as pointers. Kept here to make accidental COALESCE fail review.
var _ pgx.Row

// TranscodeRequestPayload is the wire shape of a MediaTranscodeRequested
// event as media-service writes it: the shared payload plus the operator
// rotation override a reprocess may carry. The worker decodes into this
// same type; an ordinary upload simply has RotateDegrees 0 and the field is
// omitted from the JSON, so every existing consumer of the event is
// unaffected.
type TranscodeRequestPayload struct {
	sharedevents.MediaTranscodeRequestedPayload
	// RotateDegrees, when non-zero, asks the worker to stamp this display
	// rotation (degrees counter-clockwise, a quarter turn) onto the original
	// before processing it. For a file whose pixels are sideways with no
	// rotation metadata (Family Outing, 2026-09-05).
	RotateDegrees int `json:"rotate_degrees,omitempty"`
}

// ErrTranscodeInFlight means a transcode request or completion for this
// asset has not been published yet, so a re-run would race it.
var ErrTranscodeInFlight = errors.New("transcode already in flight")

// RequeueTranscode asks the worker to process an asset again — a new
// MediaTranscodeRequested with a FRESH event id, so neither the worker's
// inbox nor post-service's consumer mistakes it for a replay of the first
// run.
//
// The outbox is UNIQUE (media_asset_id, event_type): the first run's
// request and completion rows are still there, published. They are removed
// here so the new request can be inserted and, later, so CompleteTranscode's
// `<id>:completed` row is not silently swallowed by ON CONFLICT DO NOTHING
// (which would leave post-service never hearing about the corrected
// dimensions). An UNPUBLISHED row means a run is in flight; that is refused.
//
// processing_status is left alone for a ready asset: its variants keep
// serving while the worker rebuilds them in place. A failed asset moves to
// 'processing' so the client's status poll shows the retry.
func (s *MediaAssetStore) RequeueTranscode(ctx context.Context, media *MediaAsset, rotateDegrees int) (string, error) {
	if media == nil || media.ID == uuid.Nil || media.UploaderID == uuid.Nil {
		return "", fmt.Errorf("requeue transcode: invalid media identity")
	}
	payload, err := json.Marshal(TranscodeRequestPayload{
		MediaTranscodeRequestedPayload: sharedevents.MediaTranscodeRequestedPayload{
			MediaAssetID: media.ID.String(),
			UploaderID:   media.UploaderID.String(),
			StorageKey:   media.StorageKey,
			MimeType:     media.MimeType,
		},
		RotateDegrees: rotateDegrees,
	})
	if err != nil {
		return "", fmt.Errorf("requeue transcode payload: %w", err)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin requeue transcode: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var pending int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM media_event_outbox
		 WHERE media_asset_id = $1
		   AND event_type IN ($2, $3)
		   AND published_at IS NULL
	`, media.ID, sharedevents.MediaTranscodeRequested, sharedevents.MediaTranscodeCompleted).Scan(&pending); err != nil {
		return "", fmt.Errorf("check transcode outbox: %w", err)
	}
	if pending > 0 {
		return "", ErrTranscodeInFlight
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM media_event_outbox
		 WHERE media_asset_id = $1
		   AND event_type IN ($2, $3)
		   AND published_at IS NOT NULL
	`, media.ID, sharedevents.MediaTranscodeRequested, sharedevents.MediaTranscodeCompleted); err != nil {
		return "", fmt.Errorf("clear published transcode events: %w", err)
	}

	eventID := uuid.NewString()
	if _, err := tx.Exec(ctx, `
		INSERT INTO media_event_outbox
		       (event_id, media_asset_id, event_type, actor_user_id, payload)
		VALUES ($1, $2, $3, $4, $5::jsonb)
	`, eventID, media.ID, sharedevents.MediaTranscodeRequested, media.UploaderID, payload); err != nil {
		return "", fmt.Errorf("insert transcode re-request outbox: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE media_assets
		   SET processing_status = CASE WHEN processing_status = 'ready' THEN 'ready' ELSE 'processing' END,
		       updated_at = NOW()
		 WHERE id = $1
	`, media.ID); err != nil {
		return "", fmt.Errorf("mark media for reprocess: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit requeue transcode: %w", err)
	}
	return eventID, nil
}
