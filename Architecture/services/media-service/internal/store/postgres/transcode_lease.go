package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Transcode lease and stall sweeper (2026-09-29, migration 021).
//
// # THE PROBLEM
//
// A worker that dies mid-transcode leaves its asset at
// processing_status='processing'. Nothing in the row tells a dead job from a
// live one: updated_at does not move while ffmpeg runs, and a long upload
// legitimately takes hours (a 24-minute video at ~0.5x per rendition). So a
// "processing for longer than N minutes" rule would kill healthy jobs.
//
// # THE LEASE
//
// The worker stamps transcode_heartbeat_at when it starts a job and every 30 s
// while it runs. A heartbeat older than HeartbeatStaleAfter is a dead job.
//
// # WHAT A NULL HEARTBEAT MEANS — AND WHY IT IS GATED
//
// NULL means "queued, not started since". That is ALSO the state of every
// asset waiting in Kafka behind a long job: the worker consumes serially, so a
// three-hour transcode holds every later upload at NULL for three hours (this
// is exactly the state of the three "stuck" assets on dev, 2026-09-29 — no
// transcoding_jobs rows, queued behind 707f035a). Re-queuing those would do
// nothing useful and, with the attempts cap, would eventually FAIL a healthy
// upload. So a NULL heartbeat only counts as lost work when the transcode
// pipeline is idle: no heartbeat anywhere within HeartbeatStaleAfter and the
// sweeping worker itself not busy. A busy pipeline means Kafka is the queue.
//
// # SAFETY OF A RE-QUEUE
//
// A re-queue writes a new MediaTranscodeRequested with a fresh event id
// through the same store path the operator reprocess route uses
// (requeueTranscodeTx): an unpublished request/completion is refused
// (ErrTranscodeInFlight → skipped), and the worker skips a delivery whose
// event is no longer the asset's current request (CurrentTranscodeRequest), so
// the original message still sitting in Kafka does not run a second full
// transcode.

// StallPolicy is the sweep's thresholds.
type StallPolicy struct {
	// HeartbeatStaleAfter: a heartbeat older than this is a dead job. The
	// worker stamps every 30 s, so this is twenty missed beats.
	HeartbeatStaleAfter time.Duration
	// OrphanAfter: a NULL-heartbeat asset queued longer than this, with the
	// pipeline idle, is lost work. Covers assets orphaned before migration
	// 021 and requests the consumer never picked up.
	OrphanAfter time.Duration
	// MaxAttempts: after this many automatic re-queues the asset is failed
	// instead of re-queued again.
	MaxAttempts int
	// Batch bounds one sweep.
	Batch int
}

// DefaultStallPolicy is the production policy.
func DefaultStallPolicy() StallPolicy {
	return StallPolicy{
		HeartbeatStaleAfter: 10 * time.Minute,
		OrphanAfter:         30 * time.Minute,
		MaxAttempts:         3,
		Batch:               20,
	}
}

// StallAction is the sweep's decision for one asset.
type StallAction string

const (
	StallSkip    StallAction = "skip"
	StallRequeue StallAction = "requeue"
	StallGiveUp  StallAction = "give_up"
	// StallAbandonReprocess: a READY asset's reprocess died and used up its
	// re-queues. The asset keeps serving what it has; the outstanding
	// request is recorded as failed so it is not swept again, and
	// ready_generation stays behind media_generation, which keeps the
	// fingerprint fence closed for it (plan O-obs-1 / P-14).
	StallAbandonReprocess StallAction = "abandon_reprocess"
)

// StalledTranscodeCandidate is the state the rule decides on.
type StalledTranscodeCandidate struct {
	MediaID          uuid.UUID
	FileType         string
	ProcessingStatus string
	HeartbeatAt      *time.Time
	UpdatedAt        time.Time
	Attempts         int
	// InFlight: an unpublished transcode request or completion exists, so
	// the relay has work for this asset that nobody has seen yet.
	InFlight bool
	// OutstandingReprocess (plan O-obs-1): the asset is 'ready' but a
	// re-queue bumped media_generation past ready_generation and the
	// current request has no recorded outcome — a reprocess whose worker
	// died, or whose message was lost, on an asset that keeps serving the
	// previous generation. The 'processing'-only sweep never saw these.
	OutstandingReprocess bool
}

// StallVerdict is the decision plus the human reason logged with it.
type StallVerdict struct {
	Action StallAction
	Reason string
}

// ClassifyStalledTranscode is the selection rule, pure so every branch is
// unit-tested. now must come from the same clock as the timestamps (the
// sweep passes the database's NOW()). pipelineBusy is true when any
// transcode heartbeat is fresh or the sweeping worker is itself busy.
func ClassifyStalledTranscode(c StalledTranscodeCandidate, p StallPolicy, now time.Time, pipelineBusy bool) StallVerdict {
	if c.FileType != "video" {
		return StallVerdict{StallSkip, "not a processing video"}
	}
	reprocess := c.ProcessingStatus == "ready" && c.OutstandingReprocess
	if c.ProcessingStatus != "processing" && !reprocess {
		return StallVerdict{StallSkip, "not a processing video"}
	}
	if c.InFlight {
		return StallVerdict{StallSkip, "transcode request or completion still unpublished"}
	}

	var stalled string
	if c.HeartbeatAt != nil {
		silent := now.Sub(*c.HeartbeatAt)
		if silent < p.HeartbeatStaleAfter {
			return StallVerdict{StallSkip, "heartbeat fresh"}
		}
		stalled = fmt.Sprintf("no transcode heartbeat for %s (last %s)",
			silent.Round(time.Second), c.HeartbeatAt.UTC().Format(time.RFC3339))
	} else {
		waited := now.Sub(c.UpdatedAt)
		if waited < p.OrphanAfter {
			return StallVerdict{StallSkip, "queued recently"}
		}
		if pipelineBusy {
			// Queued behind running work; Kafka is holding it.
			return StallVerdict{StallSkip, "queued behind a running transcode"}
		}
		stalled = fmt.Sprintf("never started: queued %s ago with the transcode pipeline idle",
			waited.Round(time.Second))
	}

	if c.Attempts >= p.MaxAttempts {
		if reprocess {
			return StallVerdict{StallAbandonReprocess, fmt.Sprintf(
				"reprocess of a ready asset stalled: %s; abandoned after %d automatic re-queues (asset keeps serving generation %s)",
				stalled, c.Attempts, "ready_generation")}
		}
		return StallVerdict{StallGiveUp, fmt.Sprintf(
			"transcode stalled: %s; gave up after %d automatic re-queues", stalled, c.Attempts)}
	}
	if reprocess {
		return StallVerdict{StallRequeue, fmt.Sprintf(
			"reprocess of a ready asset %s; automatic re-queue %d of %d", stalled, c.Attempts+1, p.MaxAttempts)}
	}
	return StallVerdict{StallRequeue, fmt.Sprintf(
		"%s; automatic re-queue %d of %d", stalled, c.Attempts+1, p.MaxAttempts)}
}

// StallOutcome is one asset the sweep acted on (or deliberately skipped
// under lock).
type StallOutcome struct {
	MediaID uuid.UUID
	Action  StallAction
	Reason  string
	// EventID: the new request (requeue) or the event the failure was
	// recorded against (give up).
	EventID string
	// Attempts after the action.
	Attempts int
}

// StampTranscodeHeartbeat renews the lease. It deliberately leaves updated_at
// alone: the heartbeat is the only moving part, so nothing that orders or
// ages assets by updated_at sees a running job as "just changed".
func (s *MediaAssetStore) StampTranscodeHeartbeat(ctx context.Context, mediaID uuid.UUID) error {
	if _, err := s.db.Exec(ctx,
		`UPDATE media_assets SET transcode_heartbeat_at = NOW() WHERE id = $1`, mediaID); err != nil {
		return fmt.Errorf("stamp transcode heartbeat for %s: %w", mediaID, err)
	}
	return nil
}

// CurrentTranscodeRequest returns the event id of the asset's current
// MediaTranscodeRequested outbox row. found=false means there is no row
// (an event from before the outbox, or one pruned) and the caller must not
// treat the delivery as superseded.
func (s *MediaAssetStore) CurrentTranscodeRequest(ctx context.Context, mediaID uuid.UUID) (eventID string, found bool, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT event_id FROM media_event_outbox
		 WHERE media_asset_id = $1 AND event_type = $2
	`, mediaID, sharedevents.MediaTranscodeRequested).Scan(&eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("current transcode request for %s: %w", mediaID, err)
	}
	return eventID, true, nil
}

func millis(d time.Duration) int64 { return d.Milliseconds() }

// ReclaimStalledTranscodes runs one sweep. localBusy reports whether the
// calling worker is handling a message (or was within its idle grace).
//
// Candidates are listed without a lock, then each is re-read and decided
// under SELECT … FOR UPDATE SKIP LOCKED in its own transaction: two replicas
// sweeping at once never act on the same asset, a heartbeat that renewed
// between the list and the lock is seen, and one bad row does not roll back
// the others.
func (s *MediaAssetStore) ReclaimStalledTranscodes(ctx context.Context, p StallPolicy, localBusy bool) ([]StallOutcome, error) {
	if p.Batch <= 0 || p.Batch > 500 {
		p.Batch = 20
	}
	// Two kinds of candidate: a 'processing' video, and a 'ready' video with
	// an outstanding reprocess (media_generation bumped past
	// ready_generation by a re-queue — always ≥ 2 — with the current
	// request published and no outcome recorded for it). A ready asset
	// whose transcode simply finished has ready_generation =
	// media_generation and is never a candidate, stale heartbeat or not.
	rows, err := s.db.Query(ctx, `
		SELECT m.id FROM media_assets m
		 WHERE m.file_type = 'video'
		   AND (   m.processing_status = 'processing'
		        OR (m.processing_status = 'ready'
		            AND m.media_generation >= 2
		            AND m.media_generation > COALESCE(m.ready_generation, 0)
		            AND EXISTS (SELECT 1 FROM media_event_outbox o
		                         WHERE o.media_asset_id = m.id AND o.event_type = $4
		                           AND o.published_at IS NOT NULL
		                           AND NOT EXISTS (SELECT 1 FROM media_transcode_inbox i WHERE i.event_id = o.event_id))))
		   AND (   (m.transcode_heartbeat_at IS NOT NULL
		            AND m.transcode_heartbeat_at <= NOW() - $1::bigint * INTERVAL '1 millisecond')
		        OR (m.transcode_heartbeat_at IS NULL
		            AND m.updated_at <= NOW() - $2::bigint * INTERVAL '1 millisecond'))
		 ORDER BY m.updated_at
		 LIMIT $3
	`, millis(p.HeartbeatStaleAfter), millis(p.OrphanAfter), p.Batch, sharedevents.MediaTranscodeRequested)
	if err != nil {
		return nil, fmt.Errorf("list stalled transcodes: %w", err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan stalled transcode: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list stalled transcodes: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	busy := localBusy
	if !busy {
		if busy, err = s.anyFreshTranscodeHeartbeat(ctx, p.HeartbeatStaleAfter); err != nil {
			return nil, err
		}
	}

	var out []StallOutcome
	var errs []error
	for _, id := range ids {
		o, err := s.reclaimStalledTranscode(ctx, id, p, busy)
		if err != nil {
			errs = append(errs, fmt.Errorf("media %s: %w", id, err))
			continue
		}
		if o != nil {
			out = append(out, *o)
		}
	}
	return out, errors.Join(errs...)
}

// anyFreshTranscodeHeartbeat: is some worker transcoding right now?
func (s *MediaAssetStore) anyFreshTranscodeHeartbeat(ctx context.Context, within time.Duration) (bool, error) {
	var busy bool
	if err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM media_assets
			 WHERE transcode_heartbeat_at >= NOW() - $1::bigint * INTERVAL '1 millisecond')
	`, millis(within)).Scan(&busy); err != nil {
		return false, fmt.Errorf("probe transcode heartbeats: %w", err)
	}
	return busy, nil
}

// reclaimStalledTranscode decides and acts on one asset under its row lock.
// A nil outcome means the row was locked by another sweeper (or is gone).
func (s *MediaAssetStore) reclaimStalledTranscode(ctx context.Context, id uuid.UUID, p StallPolicy, pipelineBusy bool) (*StallOutcome, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin stall reclaim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		c          StalledTranscodeCandidate
		uploaderID uuid.UUID
		mimeType   string
		storageKey string
		now        time.Time
	)
	err = tx.QueryRow(ctx, `
		SELECT m.id, m.uploader_id, m.file_type, m.mime_type, m.storage_key,
		       m.processing_status, m.transcode_heartbeat_at, m.updated_at,
		       m.transcode_attempts, NOW(),
		       EXISTS (SELECT 1 FROM media_event_outbox o
		                WHERE o.media_asset_id = m.id
		                  AND o.event_type IN ($2, $3)
		                  AND o.published_at IS NULL),
		       (m.media_generation >= 2
		        AND m.media_generation > COALESCE(m.ready_generation, 0)
		        AND EXISTS (SELECT 1 FROM media_event_outbox o
		                     WHERE o.media_asset_id = m.id AND o.event_type = $2
		                       AND o.published_at IS NOT NULL
		                       AND NOT EXISTS (SELECT 1 FROM media_transcode_inbox i WHERE i.event_id = o.event_id)))
		  FROM media_assets m
		 WHERE m.id = $1
		   FOR UPDATE OF m SKIP LOCKED
	`, id, sharedevents.MediaTranscodeRequested, sharedevents.MediaTranscodeCompleted).Scan(
		&c.MediaID, &uploaderID, &c.FileType, &mimeType, &storageKey,
		&c.ProcessingStatus, &c.HeartbeatAt, &c.UpdatedAt,
		&c.Attempts, &now, &c.InFlight, &c.OutstandingReprocess)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lock stall candidate: %w", err)
	}

	verdict := ClassifyStalledTranscode(c, p, now, pipelineBusy)
	switch verdict.Action {
	case StallRequeue:
		media := &MediaAsset{ID: c.MediaID, UploaderID: uploaderID, StorageKey: storageKey, MimeType: mimeType}
		_, rotate, err := currentRequestTx(ctx, tx, c.MediaID)
		if err != nil {
			return nil, err
		}
		eventID, err := requeueTranscodeTx(ctx, tx, media, rotate, true)
		if errors.Is(err, ErrTranscodeInFlight) {
			return &StallOutcome{MediaID: c.MediaID, Action: StallSkip,
				Reason: ErrTranscodeInFlight.Error(), Attempts: c.Attempts}, nil
		}
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit stall requeue: %w", err)
		}
		return &StallOutcome{MediaID: c.MediaID, Action: StallRequeue, Reason: verdict.Reason,
			EventID: eventID, Attempts: c.Attempts + 1}, nil

	case StallGiveUp:
		eventID, err := failStalledTranscodeTx(ctx, tx, c.MediaID, verdict.Reason)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit stall give-up: %w", err)
		}
		return &StallOutcome{MediaID: c.MediaID, Action: StallGiveUp, Reason: verdict.Reason,
			EventID: eventID, Attempts: c.Attempts}, nil

	case StallAbandonReprocess:
		eventID, err := abandonStalledReprocessTx(ctx, tx, c.MediaID, verdict.Reason)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit stall abandon: %w", err)
		}
		return &StallOutcome{MediaID: c.MediaID, Action: StallAbandonReprocess, Reason: verdict.Reason,
			EventID: eventID, Attempts: c.Attempts}, nil
	}
	return &StallOutcome{MediaID: c.MediaID, Action: StallSkip, Reason: verdict.Reason, Attempts: c.Attempts}, nil
}

// currentRequestTx reads the asset's current request row: its event id and
// the rotation override it carried. A stalled operator reprocess with
// rotate_degrees keeps its rotation on the automatic re-queue; the override
// sets an absolute display rotation (ffmpeg -display_rotation), so applying it
// again to an original the dead run already rewrote is a no-op.
func currentRequestTx(ctx context.Context, tx pgx.Tx, mediaID uuid.UUID) (eventID string, rotate int, err error) {
	var raw []byte
	err = tx.QueryRow(ctx, `
		SELECT event_id, payload FROM media_event_outbox
		 WHERE media_asset_id = $1 AND event_type = $2
	`, mediaID, sharedevents.MediaTranscodeRequested).Scan(&eventID, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, fmt.Errorf("read current transcode request: %w", err)
	}
	var payload TranscodeRequestPayload
	if json.Unmarshal(raw, &payload) == nil && payload.RotateDegrees%90 == 0 {
		rotate = payload.RotateDegrees
	}
	return eventID, rotate, nil
}

// failStalledTranscodeTx records the give-up exactly as a worker's terminal
// failure is recorded — processing_status 'failed', moderation
// 'manual_review', inbox row and MediaTranscodeCompleted{failed} in one
// commit — so the upload UI's status poll and post-service's review gate
// both see "failed". The reason goes on the asset's transcoding_jobs rows,
// which GET /v1/media/:id/status returns.
//
// The failure is recorded against the current request's event id, so if that
// message is still in Kafka its delivery finds the inbox row and is skipped.
func failStalledTranscodeTx(ctx context.Context, tx pgx.Tx, mediaID uuid.UUID, reason string) (string, error) {
	eventID, _, err := currentRequestTx(ctx, tx, mediaID)
	if err != nil {
		return "", err
	}
	// A published completion from an earlier run would swallow this one's
	// notification (UNIQUE (media_asset_id, event_type) … DO NOTHING). An
	// unpublished one never gets here: the candidate would be in flight.
	if _, err := tx.Exec(ctx, `
		DELETE FROM media_event_outbox
		 WHERE media_asset_id = $1 AND event_type = $2 AND published_at IS NOT NULL
	`, mediaID, sharedevents.MediaTranscodeCompleted); err != nil {
		return "", fmt.Errorf("clear published completion: %w", err)
	}

	completion := TranscodeCompletion{ProcessingStatus: "failed", ModerationStatus: "manual_review"}
	record := func(ev string) error {
		return completeTranscodeTx(ctx, tx, ev, mediaID, "failed", "", "manual_review", completion)
	}
	if eventID == "" {
		eventID = "transcode-stalled:" + uuid.NewString()
	}
	if err := record(eventID); errors.Is(err, ErrTranscodeAlreadyApplied) {
		// The current request already has an outcome yet the asset is still
		// processing (something else moved it back). Record against a fresh id.
		eventID = "transcode-stalled:" + uuid.NewString()
		err = record(eventID)
		if err != nil {
			return "", fmt.Errorf("record stall failure: %w", err)
		}
	} else if err != nil {
		return "", fmt.Errorf("record stall failure: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		UPDATE transcoding_jobs
		   SET status = 'failed', error_message = $2, completed_at = NOW()
		 WHERE media_asset_id = $1 AND status IN ('queued', 'processing')
	`, mediaID, reason)
	if err != nil {
		return "", fmt.Errorf("fail lingering transcoding jobs: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// The job never started, so there is no rendition row to carry the
		// reason. One pipeline-level row does.
		if _, err := tx.Exec(ctx, `
			INSERT INTO transcoding_jobs
			       (id, media_asset_id, target_quality, status, error_message, completed_at, created_at)
			VALUES ($1, $2, 'pipeline', 'failed', $3, NOW(), NOW())
		`, uuid.New(), mediaID, reason); err != nil {
			return "", fmt.Errorf("record stall failure job: %w", err)
		}
	}
	return eventID, nil
}

// abandonStalledReprocessTx ends a dead reprocess of a READY asset without
// touching what it serves: the current request gets an inbox row with
// outcome 'failed' (so its message, if it still arrives, is skipped and the
// asset is not swept again), the heartbeat is cleared, and its lingering
// transcoding_jobs rows carry the reason. processing_status,
// moderation_status, hls_master_key and ready_generation are left exactly
// as they were: the previous generation's renditions keep playing, and
// ready_generation < media_generation keeps the fingerprint fence closed.
func abandonStalledReprocessTx(ctx context.Context, tx pgx.Tx, mediaID uuid.UUID, reason string) (string, error) {
	eventID, _, err := currentRequestTx(ctx, tx, mediaID)
	if err != nil {
		return "", err
	}
	if eventID == "" {
		eventID = "transcode-stalled:" + uuid.NewString()
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO media_transcode_inbox (event_id, media_asset_id, outcome)
		VALUES ($1, $2, 'failed')
		ON CONFLICT (event_id) DO NOTHING
	`, eventID, mediaID); err != nil {
		return "", fmt.Errorf("record abandoned reprocess: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE media_assets SET transcode_heartbeat_at = NULL WHERE id = $1
	`, mediaID); err != nil {
		return "", fmt.Errorf("clear heartbeat of abandoned reprocess: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE transcoding_jobs
		   SET status = 'failed', error_message = $2, completed_at = NOW()
		 WHERE media_asset_id = $1 AND status IN ('queued', 'processing')
	`, mediaID, reason)
	if err != nil {
		return "", fmt.Errorf("fail lingering reprocess jobs: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO transcoding_jobs
			       (id, media_asset_id, target_quality, status, error_message, completed_at, created_at)
			VALUES ($1, $2, 'pipeline', 'failed', $3, NOW(), NOW())
		`, uuid.New(), mediaID, reason); err != nil {
			return "", fmt.Errorf("record abandoned reprocess job: %w", err)
		}
	}
	return eventID, nil
}
