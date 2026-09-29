package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/media-service/internal/fingerprint"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Fingerprint jobs (Copyright Match plan 6.1, 9.1; migration 023).
//
// A job is keyed by (asset, generation, algorithm). It is enqueued in the
// transcode completion transaction, by an operator through the internal
// route, and by the backfill command. The worker claims with
// FOR UPDATE SKIP LOCKED, a claim token and a heartbeat; a lease older than
// FingerprintLeaseStale is reclaimable. Every write of results is fenced:
// the job must still be claimed by this token, and the asset must be at
// media_generation = ready_generation = the job's generation and 'ready'.
// Otherwise the job is marked superseded and nothing is written.

const (
	// FingerprintAlgoVersion is the algorithm this build indexes with.
	FingerprintAlgoVersion = fingerprint.AlgoVersion

	FingerprintPriorityUpload   = 0
	FingerprintPriorityBackfill = 10

	// FingerprintMaxAttempts: after this many claims the job is failed and
	// alerted on. The upload is never affected.
	FingerprintMaxAttempts = 5
	// FingerprintLeaseStale: a claimed job whose heartbeat is older than
	// this is reclaimable.
	FingerprintLeaseStale = 10 * time.Minute
)

// Job statuses.
const (
	FingerprintQueued     = "queued"
	FingerprintClaimed    = "claimed"
	FingerprintDone       = "done"
	FingerprintSuperseded = "superseded"
	FingerprintFailed     = "failed"
	FingerprintSkipped    = "skipped"
)

var (
	// ErrFingerprintNotReady: the asset is not a ready video of a settled
	// generation, so there is nothing to fingerprint yet.
	ErrFingerprintNotReady = errors.New("asset is not a ready video")
	// ErrFingerprintClaimLost: the job is no longer claimed by this token
	// (the lease expired and another worker reclaimed it, or a re-queue
	// superseded it).
	ErrFingerprintClaimLost = errors.New("fingerprint job claim lost")
	// ErrFingerprintSuperseded: the generation fence failed; the job was
	// marked superseded and nothing was written.
	ErrFingerprintSuperseded = errors.New("fingerprint job superseded by a newer generation")
)

// FingerprintJobKey identifies one job.
type FingerprintJobKey struct {
	MediaID    uuid.UUID
	Generation int64
	Algo       int
}

// FingerprintJob is one row of media_fingerprint_jobs.
type FingerprintJob struct {
	FingerprintJobKey
	Priority    int
	Status      string
	SkipReason  *string
	Attempts    int
	ClaimToken  *uuid.UUID
	HeartbeatAt *time.Time
	NotBefore   time.Time
	ProgressMs  int
	Truncation  json.RawMessage
	LastError   *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const fingerprintJobColumns = `media_asset_id, media_generation, algo_version, priority, status, skip_reason,
	attempts, claim_token, heartbeat_at, not_before, progress_ms, truncation, last_error, created_at, updated_at`

func scanFingerprintJob(row pgx.Row) (*FingerprintJob, error) {
	var j FingerprintJob
	if err := row.Scan(&j.MediaID, &j.Generation, &j.Algo, &j.Priority, &j.Status, &j.SkipReason,
		&j.Attempts, &j.ClaimToken, &j.HeartbeatAt, &j.NotBefore, &j.ProgressMs, &j.Truncation, &j.LastError,
		&j.CreatedAt, &j.UpdatedAt); err != nil {
		return nil, err
	}
	return &j, nil
}

// enqueueFingerprintJobTx inserts the job for (asset, generation, current
// algorithm) if none exists. Called inside the completion transaction.
func enqueueFingerprintJobTx(ctx context.Context, tx pgx.Tx, mediaID uuid.UUID, generation int64, priority int) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO media_fingerprint_jobs (media_asset_id, media_generation, algo_version, priority, status)
		VALUES ($1, $2, $3, $4, 'queued')
		ON CONFLICT (media_asset_id, media_generation, algo_version) DO NOTHING
	`, mediaID, generation, FingerprintAlgoVersion, priority); err != nil {
		return fmt.Errorf("enqueue fingerprint job for %s gen %d: %w", mediaID, generation, err)
	}
	return nil
}

// EnqueueFingerprintJob queues a fingerprint for a ready video's current
// generation (the operator route and the backfill command). A job that
// already exists is left alone, except that a failed or skipped one is
// re-queued when retry is true. Returns the row and whether it was created
// or re-queued by this call.
func (s *MediaAssetStore) EnqueueFingerprintJob(ctx context.Context, mediaID uuid.UUID, priority int, retry bool) (*FingerprintJob, bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin enqueue fingerprint: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var fileType, status string
	var generation int64
	var readyGeneration *int64
	err = tx.QueryRow(ctx, `
		SELECT file_type, processing_status, media_generation, ready_generation
		  FROM media_assets WHERE id = $1 FOR SHARE
	`, mediaID).Scan(&fileType, &status, &generation, &readyGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, pgx.ErrNoRows
	}
	if err != nil {
		return nil, false, fmt.Errorf("read media %s for fingerprint: %w", mediaID, err)
	}
	if fileType != "video" || status != "ready" || readyGeneration == nil || *readyGeneration != generation {
		return nil, false, ErrFingerprintNotReady
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO media_fingerprint_jobs (media_asset_id, media_generation, algo_version, priority, status)
		VALUES ($1, $2, $3, $4, 'queued')
		ON CONFLICT (media_asset_id, media_generation, algo_version) DO UPDATE
		   SET status = 'queued', attempts = 0, claim_token = NULL, heartbeat_at = NULL,
		       not_before = NOW(), last_error = NULL, skip_reason = NULL, priority = EXCLUDED.priority,
		       updated_at = NOW()
		 WHERE media_fingerprint_jobs.status IN ('failed', 'skipped') AND $5::boolean
	`, mediaID, generation, FingerprintAlgoVersion, priority, retry)
	if err != nil {
		return nil, false, fmt.Errorf("enqueue fingerprint job for %s: %w", mediaID, err)
	}
	job, err := scanFingerprintJob(tx.QueryRow(ctx, `
		SELECT `+fingerprintJobColumns+` FROM media_fingerprint_jobs
		 WHERE media_asset_id = $1 AND media_generation = $2 AND algo_version = $3
	`, mediaID, generation, FingerprintAlgoVersion))
	if err != nil {
		return nil, false, fmt.Errorf("read fingerprint job for %s: %w", mediaID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit enqueue fingerprint: %w", err)
	}
	return job, tag.RowsAffected() == 1, nil
}

// GetFingerprintJobs lists every job row of an asset (all generations and
// algorithm versions), newest generation first.
func (s *MediaAssetStore) GetFingerprintJobs(ctx context.Context, mediaID uuid.UUID) ([]FingerprintJob, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+fingerprintJobColumns+` FROM media_fingerprint_jobs
		 WHERE media_asset_id = $1
		 ORDER BY media_generation DESC, algo_version DESC
	`, mediaID)
	if err != nil {
		return nil, fmt.Errorf("list fingerprint jobs for %s: %w", mediaID, err)
	}
	defer rows.Close()
	var out []FingerprintJob
	for rows.Next() {
		j, err := scanFingerprintJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan fingerprint job: %w", err)
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

// FingerprintQueueStats is what the worker exports.
type FingerprintQueueStats struct {
	Queued            int
	Claimed           int
	OldestQueuedAgeS  float64
	QueuedNewUploads  int // priority 0
	QueuedBackfill    int // priority > 0
	FailedLast24h     int
	StuckOverAttempts int
}

// FingerprintQueueStats reads the queue counters.
func (s *MediaAssetStore) FingerprintQueueStats(ctx context.Context) (FingerprintQueueStats, error) {
	var st FingerprintQueueStats
	var oldest *float64
	err := s.db.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'queued'),
		       count(*) FILTER (WHERE status = 'claimed'),
		       EXTRACT(EPOCH FROM (NOW() - MIN(created_at) FILTER (WHERE status = 'queued'))),
		       count(*) FILTER (WHERE status = 'queued' AND priority = 0),
		       count(*) FILTER (WHERE status = 'queued' AND priority > 0),
		       count(*) FILTER (WHERE status = 'failed' AND updated_at > NOW() - INTERVAL '24 hours'),
		       count(*) FILTER (WHERE status = 'queued' AND attempts >= $1)
		  FROM media_fingerprint_jobs
		 WHERE status IN ('queued', 'claimed', 'failed')
	`, FingerprintMaxAttempts).Scan(&st.Queued, &st.Claimed, &oldest, &st.QueuedNewUploads, &st.QueuedBackfill, &st.FailedLast24h, &st.StuckOverAttempts)
	if err != nil {
		return st, fmt.Errorf("fingerprint queue stats: %w", err)
	}
	if oldest != nil {
		st.OldestQueuedAgeS = *oldest
	}
	return st, nil
}

// ClaimFingerprintJob takes one job: queued and due, or claimed with a
// heartbeat older than staleAfter (a dead lease). Only jobs with priority ≤
// maxPriority are considered, so the worker can leave backfill rows alone
// while COPYRIGHT_FINGERPRINT_BACKFILL_ENABLED is off. A job that has used
// up its attempts is failed here instead of claimed. Returns nil when there
// is nothing to do.
func (s *MediaAssetStore) ClaimFingerprintJob(ctx context.Context, maxPriority int, staleAfter time.Duration, maxAttempts int) (*FingerprintJob, error) {
	for i := 0; i < 20; i++ {
		job, again, err := s.claimOneFingerprintJob(ctx, maxPriority, staleAfter, maxAttempts)
		if err != nil {
			return nil, err
		}
		if job != nil || !again {
			return job, nil
		}
	}
	return nil, nil
}

func (s *MediaAssetStore) claimOneFingerprintJob(ctx context.Context, maxPriority int, staleAfter time.Duration, maxAttempts int) (job *FingerprintJob, again bool, err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin claim fingerprint: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cand, err := scanFingerprintJob(tx.QueryRow(ctx, `
		SELECT `+fingerprintJobColumns+` FROM media_fingerprint_jobs
		 WHERE priority <= $1
		   AND (   (status = 'queued'  AND not_before <= NOW())
		        OR (status = 'claimed' AND heartbeat_at < NOW() - $2::bigint * INTERVAL '1 millisecond'))
		 ORDER BY priority, not_before
		 LIMIT 1
		 FOR UPDATE SKIP LOCKED
	`, maxPriority, millis(staleAfter)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("select fingerprint job: %w", err)
	}
	if cand.Attempts >= maxAttempts {
		if _, err := tx.Exec(ctx, `
			UPDATE media_fingerprint_jobs
			   SET status = 'failed', claim_token = NULL,
			       last_error = COALESCE(last_error, '') || ' [attempts exhausted]', updated_at = NOW()
			 WHERE media_asset_id = $1 AND media_generation = $2 AND algo_version = $3
		`, cand.MediaID, cand.Generation, cand.Algo); err != nil {
			return nil, false, fmt.Errorf("fail exhausted fingerprint job: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("commit fail exhausted: %w", err)
		}
		return nil, true, nil
	}
	token := uuid.New()
	job, err = scanFingerprintJob(tx.QueryRow(ctx, `
		UPDATE media_fingerprint_jobs
		   SET status = 'claimed', claim_token = $4, heartbeat_at = NOW(),
		       attempts = attempts + 1, updated_at = NOW()
		 WHERE media_asset_id = $1 AND media_generation = $2 AND algo_version = $3
		 RETURNING `+fingerprintJobColumns, cand.MediaID, cand.Generation, cand.Algo, token))
	if err != nil {
		return nil, false, fmt.Errorf("claim fingerprint job: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit claim fingerprint: %w", err)
	}
	return job, false, nil
}

// HeartbeatFingerprintJob renews the lease and records progress. owned is
// false when the token no longer holds the job; the worker then stops.
func (s *MediaAssetStore) HeartbeatFingerprintJob(ctx context.Context, key FingerprintJobKey, token uuid.UUID, progressMs int) (owned bool, err error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE media_fingerprint_jobs
		   SET heartbeat_at = NOW(), progress_ms = GREATEST(progress_ms, $5), updated_at = NOW()
		 WHERE media_asset_id = $1 AND media_generation = $2 AND algo_version = $3
		   AND status = 'claimed' AND claim_token = $4
	`, key.MediaID, key.Generation, key.Algo, token, progressMs)
	if err != nil {
		return false, fmt.Errorf("heartbeat fingerprint job: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ReleaseFingerprintJob puts a claimed job back in the queue, not before
// `delay` from now (pre-emption by a transcode, a transient input problem).
// refund is true when the attempt should not count (pre-emption).
func (s *MediaAssetStore) ReleaseFingerprintJob(ctx context.Context, key FingerprintJobKey, token uuid.UUID, delay time.Duration, reason string, refund bool) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE media_fingerprint_jobs
		   SET status = 'queued', claim_token = NULL, heartbeat_at = NULL,
		       not_before = NOW() + $5::bigint * INTERVAL '1 millisecond',
		       attempts = CASE WHEN $7::boolean THEN GREATEST(attempts - 1, 0) ELSE attempts END,
		       last_error = NULLIF($6, ''), updated_at = NOW()
		 WHERE media_asset_id = $1 AND media_generation = $2 AND algo_version = $3
		   AND status = 'claimed' AND claim_token = $4
	`, key.MediaID, key.Generation, key.Algo, token, millis(delay), reason, refund)
	if err != nil {
		return fmt.Errorf("release fingerprint job: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrFingerprintClaimLost
	}
	return nil
}

// FailFingerprintJob records a terminal failure.
func (s *MediaAssetStore) FailFingerprintJob(ctx context.Context, key FingerprintJobKey, token uuid.UUID, reason string) error {
	return s.endFingerprintJob(ctx, key, token, FingerprintFailed, reason, "")
}

// SkipFingerprintJob records a deliberate skip with its reason (e.g.
// original_too_large). The CHECK constraint requires the reason.
func (s *MediaAssetStore) SkipFingerprintJob(ctx context.Context, key FingerprintJobKey, token uuid.UUID, reason string) error {
	if reason == "" {
		return fmt.Errorf("skip fingerprint job: a reason is required")
	}
	return s.endFingerprintJob(ctx, key, token, FingerprintSkipped, "", reason)
}

// MarkFingerprintJobSuperseded records a fence failure.
func (s *MediaAssetStore) MarkFingerprintJobSuperseded(ctx context.Context, key FingerprintJobKey, token uuid.UUID) error {
	return s.endFingerprintJob(ctx, key, token, FingerprintSuperseded, "generation fence failed", "")
}

func (s *MediaAssetStore) endFingerprintJob(ctx context.Context, key FingerprintJobKey, token uuid.UUID, status, lastError, skipReason string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE media_fingerprint_jobs
		   SET status = $5, claim_token = NULL, last_error = NULLIF($6, ''),
		       skip_reason = NULLIF($7, ''), updated_at = NOW()
		 WHERE media_asset_id = $1 AND media_generation = $2 AND algo_version = $3
		   AND status = 'claimed' AND claim_token = $4
	`, key.MediaID, key.Generation, key.Algo, token, status, lastError, skipReason)
	if err != nil {
		return fmt.Errorf("end fingerprint job as %s: %w", status, err)
	}
	if tag.RowsAffected() != 1 {
		return ErrFingerprintClaimLost
	}
	return nil
}

// fingerprintFenceTx is the write fence (plan 6.1, generation rule 4): the
// job is still claimed by this token (row locked), and the asset is at
// media_generation = ready_generation = the job's generation and 'ready'
// (FOR SHARE, so a concurrent re-queue's FOR UPDATE waits behind this
// write, or this read sees its bump). On a fence failure the caller rolls
// back and marks the job superseded outside the transaction.
func fingerprintFenceTx(ctx context.Context, tx pgx.Tx, key FingerprintJobKey, token uuid.UUID) error {
	var status string
	var claim *uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT status, claim_token FROM media_fingerprint_jobs
		 WHERE media_asset_id = $1 AND media_generation = $2 AND algo_version = $3
		 FOR UPDATE
	`, key.MediaID, key.Generation, key.Algo).Scan(&status, &claim)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrFingerprintClaimLost
	}
	if err != nil {
		return fmt.Errorf("lock fingerprint job: %w", err)
	}
	if status == FingerprintSuperseded {
		// A re-queue already retired this job (bumpMediaGenerationTx); the
		// generation it read is gone whatever the token says.
		return ErrFingerprintSuperseded
	}
	if status != FingerprintClaimed || claim == nil || *claim != token {
		return ErrFingerprintClaimLost
	}
	var gen int64
	var readyGen *int64
	var processing string
	err = tx.QueryRow(ctx, `
		SELECT media_generation, ready_generation, processing_status
		  FROM media_assets WHERE id = $1 FOR SHARE
	`, key.MediaID).Scan(&gen, &readyGen, &processing)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrFingerprintSuperseded // the asset is gone
	}
	if err != nil {
		return fmt.Errorf("read media generation for fence: %w", err)
	}
	if processing != "ready" || readyGen == nil || gen != key.Generation || *readyGen != key.Generation {
		return ErrFingerprintSuperseded
	}
	return nil
}

// StoredFingerprint is one row of copyright_fingerprints.
type StoredFingerprint struct {
	MediaID        uuid.UUID
	Generation     int64
	Algo           int
	InputKind      string // hls_rung | mp4_variant | original
	InputRef       string // rung or variant name, never a URL
	InputETags     map[string]string
	DurationMs     int
	InformativeMs  int
	Frames         []byte
	AnchorSpacingS int
	DurBucket      int
	SupersededAt   *time.Time
	CreatedAt      time.Time
}

// AnchorPosting is one row of the multi-index for one anchor and band.
type AnchorPosting struct {
	BandNo    int
	BandValue uint16
	AnchorMs  int32
	Hash      uint64
}

// StoreFingerprint writes the fingerprint row and its postings in one
// fenced transaction (insert-then-query, plan 9.1). The job stays claimed;
// the lookup and the pair write follow. Both inserts are ON CONFLICT DO
// NOTHING, so a reclaim after a crash between this commit and the pair
// write finds them present and writes nothing twice.
func (s *MediaAssetStore) StoreFingerprint(ctx context.Context, key FingerprintJobKey, token uuid.UUID, fp StoredFingerprint, postings []AnchorPosting) error {
	if fp.InputKind == "" || fp.InputRef == "" {
		return fmt.Errorf("store fingerprint: input kind and ref are required")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin store fingerprint: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fingerprintFenceTx(ctx, tx, key, token); err != nil {
		return err
	}
	etags, err := json.Marshal(fp.InputETags)
	if err != nil {
		return fmt.Errorf("marshal input etags: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO copyright_fingerprints
		       (media_asset_id, media_generation, algo_version, input_kind, input_ref, input_etags,
		        duration_ms, informative_ms, frames, anchor_spacing_s, dur_bucket)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, $10, $11)
		ON CONFLICT (media_asset_id, media_generation, algo_version) DO NOTHING
	`, key.MediaID, key.Generation, key.Algo, fp.InputKind, fp.InputRef, etags,
		fp.DurationMs, fp.InformativeMs, fp.Frames, fp.AnchorSpacingS, fp.DurBucket); err != nil {
		return fmt.Errorf("insert fingerprint: %w", err)
	}
	// Postings in batches of ≤ 5,000 rows (plan 13, backfill safeguards).
	const batch = 5000
	for start := 0; start < len(postings); start += batch {
		end := start + batch
		if end > len(postings) {
			end = len(postings)
		}
		chunk := postings[start:end]
		bandNos := make([]int16, len(chunk))
		values := make([]int32, len(chunk))
		anchors := make([]int32, len(chunk))
		hashes := make([]int64, len(chunk))
		for i, p := range chunk {
			bandNos[i] = int16(p.BandNo)
			values[i] = int32(p.BandValue)
			anchors[i] = p.AnchorMs
			hashes[i] = int64(p.Hash)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO copyright_anchor_postings
			       (algo_version, dur_bucket, band_no, band_value, media_asset_id, media_generation, anchor_ms, hash)
			SELECT $1, $2, b, v, $3, $4, a, h
			  FROM unnest($5::smallint[], $6::int[], $7::int[], $8::bigint[]) AS t(b, v, a, h)
			ON CONFLICT DO NOTHING
		`, key.Algo, fp.DurBucket, key.MediaID, key.Generation, bandNos, values, anchors, hashes); err != nil {
			return fmt.Errorf("insert anchor postings: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit store fingerprint: %w", err)
	}
	return nil
}

// LoadFingerprint reads one stored fingerprint; nil when absent.
func (s *MediaAssetStore) LoadFingerprint(ctx context.Context, mediaID uuid.UUID, generation int64, algo int) (*StoredFingerprint, error) {
	var fp StoredFingerprint
	var etags []byte
	err := s.db.QueryRow(ctx, `
		SELECT media_asset_id, media_generation, algo_version, input_kind, input_ref, input_etags,
		       duration_ms, informative_ms, frames, anchor_spacing_s, dur_bucket, superseded_at, created_at
		  FROM copyright_fingerprints
		 WHERE media_asset_id = $1 AND media_generation = $2 AND algo_version = $3
	`, mediaID, generation, algo).Scan(&fp.MediaID, &fp.Generation, &fp.Algo, &fp.InputKind, &fp.InputRef, &etags,
		&fp.DurationMs, &fp.InformativeMs, &fp.Frames, &fp.AnchorSpacingS, &fp.DurBucket, &fp.SupersededAt, &fp.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load fingerprint %s gen %d: %w", mediaID, generation, err)
	}
	if len(etags) > 0 {
		_ = json.Unmarshal(etags, &fp.InputETags)
	}
	return &fp, nil
}

// ListReadyVideosWithoutFingerprint pages ready videos whose current
// generation has no job row for the current algorithm, oldest first. The
// backfill command feeds these to EnqueueFingerprintJob at low priority.
func (s *MediaAssetStore) ListReadyVideosWithoutFingerprint(ctx context.Context, after time.Time, limit int) ([]uuid.UUID, []time.Time, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.Query(ctx, `
		SELECT m.id, m.created_at
		  FROM media_assets m
		 WHERE m.file_type = 'video'
		   AND m.processing_status = 'ready'
		   AND m.ready_generation = m.media_generation
		   AND m.created_at > $1
		   AND NOT EXISTS (SELECT 1 FROM media_fingerprint_jobs j
		                    WHERE j.media_asset_id = m.id
		                      AND j.media_generation = m.media_generation
		                      AND j.algo_version = $2)
		 ORDER BY m.created_at
		 LIMIT $3
	`, after, FingerprintAlgoVersion, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("list videos for fingerprint backfill: %w", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	var times []time.Time
	for rows.Next() {
		var id uuid.UUID
		var at time.Time
		if err := rows.Scan(&id, &at); err != nil {
			return nil, nil, err
		}
		ids = append(ids, id)
		times = append(times, at)
	}
	return ids, times, rows.Err()
}

// SweepStalePostings deletes postings whose generation is no longer the
// asset's current one (or whose asset is gone), in one bounded batch, and
// superseded fingerprints older than keepSuperseded. Returns rows deleted.
func (s *MediaAssetStore) SweepStalePostings(ctx context.Context, limit int, keepSuperseded time.Duration) (int64, error) {
	if limit <= 0 || limit > 50_000 {
		limit = 5000
	}
	tag, err := s.db.Exec(ctx, `
		DELETE FROM copyright_anchor_postings p
		 WHERE ctid IN (
		   SELECT p2.ctid FROM copyright_anchor_postings p2
		     LEFT JOIN media_assets m ON m.id = p2.media_asset_id
		    WHERE m.id IS NULL OR p2.media_generation < m.media_generation
		    LIMIT $1)
	`, limit)
	if err != nil {
		return 0, fmt.Errorf("sweep stale postings: %w", err)
	}
	n := tag.RowsAffected()
	tag, err = s.db.Exec(ctx, `
		DELETE FROM copyright_fingerprints
		 WHERE superseded_at IS NOT NULL
		   AND superseded_at < NOW() - $1::bigint * INTERVAL '1 millisecond'
	`, millis(keepSuperseded))
	if err != nil {
		return n, fmt.Errorf("sweep superseded fingerprints: %w", err)
	}
	return n + tag.RowsAffected(), nil
}
