package postgres

import (
	"context"
	"fmt"
	"hash/fnv"

	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Transcode classes on the asset row (migration 035; re-audit N-1, lv-21).
//
// The short consumer sorts every request into 'short' or 'long'
// (cmd/worker/transcode_classes.go) and records the class here, against the
// request's event id. The per-class queue gauges read it: a request whose
// recorded class belongs to another (older) request, or that nothing has
// routed yet, is waiting on the short intake topic, so it counts as short.

// TranscodeClassShort is the class of anything not (yet) routed long.
const TranscodeClassShort = "short"

// RecordTranscodeClass records that request eventID of mediaID was routed
// into class. Only the gauges read it; a failure costs a metric, never a job.
func (s *MediaAssetStore) RecordTranscodeClass(ctx context.Context, mediaID uuid.UUID, eventID, class string) error {
	if _, err := s.db.Exec(ctx, `
		UPDATE media_assets SET transcode_class = $3, transcode_class_event = $2
		 WHERE id = $1
	`, mediaID, eventID, class); err != nil {
		return fmt.Errorf("record transcode class for %s: %w", mediaID, err)
	}
	return nil
}

// TranscodeAgesByClass is the oldest age, in seconds, per class; a class
// with nothing waiting is absent.
type TranscodeAgesByClass struct {
	// Pending: OldestPendingTranscodeAge per class (uploaded/processing, by
	// created_at).
	Pending map[string]float64
	// Queued: OldestQueuedTranscodeWait per class (published request, no
	// heartbeat since it was queued, by updated_at).
	Queued map[string]float64
}

// OldestTranscodeAgesByClass is OldestPendingTranscodeAge and
// OldestQueuedTranscodeWait split by the class each asset's CURRENT request
// was routed into. The outbox holds at most one request row per asset
// (UNIQUE (media_asset_id, event_type)), so the join never multiplies rows.
func (s *MediaAssetStore) OldestTranscodeAgesByClass(ctx context.Context) (TranscodeAgesByClass, error) {
	out := TranscodeAgesByClass{Pending: map[string]float64{}, Queued: map[string]float64{}}
	const classOf = `COALESCE(CASE WHEN o.event_id IS NOT NULL AND m.transcode_class_event = o.event_id::text
	                              THEN m.transcode_class END, '` + TranscodeClassShort + `')`
	rows, err := s.db.Query(ctx, `
		SELECT `+classOf+` AS class,
		       EXTRACT(EPOCH FROM (NOW() - MIN(m.created_at)))::float8
		  FROM media_assets m
		  LEFT JOIN media_event_outbox o ON o.media_asset_id = m.id AND o.event_type = $1
		 WHERE m.processing_status IN ('uploaded', 'processing')
		 GROUP BY 1
	`, sharedevents.MediaTranscodeRequested)
	if err != nil {
		return out, fmt.Errorf("oldest pending transcode by class: %w", err)
	}
	if err := scanClassAges(rows, out.Pending); err != nil {
		return out, fmt.Errorf("oldest pending transcode by class: %w", err)
	}
	rows, err = s.db.Query(ctx, `
		SELECT `+classOf+` AS class,
		       EXTRACT(EPOCH FROM (NOW() - MIN(m.updated_at)))::float8
		  FROM media_assets m
		  JOIN media_event_outbox o ON o.media_asset_id = m.id AND o.event_type = $1
		                            AND o.published_at IS NOT NULL
		 WHERE m.processing_status = 'processing'
		   AND m.file_type = 'video'
		   AND m.transcode_heartbeat_at IS NULL
		 GROUP BY 1
	`, sharedevents.MediaTranscodeRequested)
	if err != nil {
		return out, fmt.Errorf("oldest queued transcode by class: %w", err)
	}
	if err := scanClassAges(rows, out.Queued); err != nil {
		return out, fmt.Errorf("oldest queued transcode by class: %w", err)
	}
	return out, nil
}

type classAgeRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}

func scanClassAges(rows classAgeRows, into map[string]float64) error {
	defer rows.Close()
	for rows.Next() {
		var class string
		var age *float64
		if err := rows.Scan(&class, &age); err != nil {
			return err
		}
		if age != nil {
			into[class] = *age
		}
	}
	return rows.Err()
}

// WithTranscodeMediaLock serialises every transcode run of ONE asset across
// worker replicas and consumer loops (REG-9). WithTranscodeEventLock keys on
// the event id, so a reprocess (a fresh event id) and the run it supersedes
// could encode at once and write the same hls/<q>_NNN.ts keys and rung
// markers under one asset. Keyed on the media id, the newer request waits
// for the older run to return; it then re-encodes every unit (the markers
// are generation-pinned) and the older run's completion is refused as
// stale, so two generations never interleave in one playlist. A session
// advisory lock: PostgreSQL releases it if the worker dies.
func (s *MediaAssetStore) WithTranscodeMediaLock(ctx context.Context, mediaID uuid.UUID, fn func() error) error {
	if mediaID == uuid.Nil {
		return fmt.Errorf("transcode lock: no media id")
	}
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire transcode lock connection: %w", err)
	}
	defer conn.Release()
	key := TranscodeMediaLockKey(mediaID)
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		return fmt.Errorf("acquire transcode media lock: %w", err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, key) }()
	return fn()
}

// TranscodeMediaLockKey is the advisory-lock key of one asset's transcodes.
func TranscodeMediaLockKey(mediaID uuid.UUID) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("media-transcode-asset:" + mediaID.String()))
	return int64(h.Sum64()) //nolint:gosec // G115: a hash folded into the advisory-lock key space; wrap-around is intended
}
