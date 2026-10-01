package postgres

// Lifecycle writes (1 Oct 2026). Every status change is a locked
// read-decide-write in ONE transaction: the row is read FOR UPDATE together
// with the age of its current status measured by the DATABASE clock, the
// caller's pure decider picks the next state, and the update, the outbox
// rows it implies and (for admin actions) the audit row commit together or
// not at all. There is no path that writes 'live' without a decider saying
// so, and the deciders only say so on a host track (service/lifecycle.go).

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Stream statuses.
const (
	StatusScheduled    = "scheduled"
	StatusStarting     = "starting"
	StatusLive         = "live"
	StatusReconnecting = "reconnecting"
	StatusEnded        = "ended"
	StatusFailed       = "failed"
)

// IsTerminal reports whether status is ended or failed.
func IsTerminal(status string) bool { return status == StatusEnded || status == StatusFailed }

// ErrStateConflict: the decider refused the change from the row's status.
var ErrStateConflict = errors.New("stream is not in a state that allows this change")

// querier is satisfied by *pgxpool.Pool and pgx.Tx, so one statement can run
// standalone or inside an admin action's transaction.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// OutboxEvent is one live_v2.outbox_events row.
type OutboxEvent struct {
	EventType      string
	PartitionKey   string
	IdempotencyKey string // "" = no dedup
	Payload        []byte // the full EventEnvelope JSON
}

// AuditEntry is one append-only live_admin_audit row.
type AuditEntry struct {
	ActorID    uuid.UUID
	Action     string
	TargetType string
	TargetID   string
	Reason     string
	Detail     []byte // JSON object; nil = {}
}

// Decision is a decider's answer: the next status and, for ended/failed,
// the ended_reason.
type Decision struct {
	To     string
	Reason string
}

// TransitionFunc decides the next state from the locked row and the age of
// its status. ok=false refuses (ErrStateConflict); To == cur.Status is a
// no-op that writes nothing.
type TransitionFunc func(cur *LiveStream, age time.Duration) (d Decision, ok bool)

// EventsFunc returns the outbox rows a committed change implies.
type EventsFunc func(prev, next *LiveStream) ([]OutboxEvent, error)

// TransitionResult is the row before and after. Changed is false on a no-op.
type TransitionResult struct {
	Prev, Next *LiveStream
	Changed    bool
}

// ApplyTransition runs decide on the locked row and applies its decision,
// the events and the optional audit row in one transaction.
func (s *Store) ApplyTransition(ctx context.Context, id uuid.UUID, decide TransitionFunc, events EventsFunc, audit *AuditEntry) (*TransitionResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	res, err := applyTransitionTx(ctx, tx, id, decide, events, audit)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return res, nil
}

func applyTransitionTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, decide TransitionFunc, events EventsFunc, audit *AuditEntry) (*TransitionResult, error) {
	var ageSec float64
	prev, err := scanStreamExtra(tx.QueryRow(ctx,
		`SELECT `+selectColumns+`, EXTRACT(EPOCH FROM (NOW() - status_changed_at))::float8
		 FROM live_streams WHERE id = $1 FOR UPDATE`, id), &ageSec)
	if err != nil {
		return nil, err
	}
	d, ok := decide(prev, time.Duration(ageSec*float64(time.Second)))
	if !ok {
		return nil, ErrStateConflict
	}
	if d.To == prev.Status {
		if audit != nil {
			if err := insertAudit(ctx, tx, *audit); err != nil {
				return nil, err
			}
		}
		return &TransitionResult{Prev: prev, Next: prev}, nil
	}
	var reason *string
	if IsTerminal(d.To) {
		if d.Reason == "" {
			return nil, fmt.Errorf("transition to %s needs an ended_reason", d.To)
		}
		r := d.Reason
		reason = &r
	}
	next, err := scanStream(tx.QueryRow(ctx, `
		UPDATE live_streams SET
		    status = $2,
		    ended_reason = $3,
		    status_changed_at = NOW(),
		    started_at = CASE WHEN $2 = 'live' THEN COALESCE(started_at, NOW()) ELSE started_at END,
		    ended_at = CASE WHEN $2 IN ('ended','failed') THEN COALESCE(ended_at, NOW())
		                    WHEN $2 = 'starting' THEN NULL
		                    ELSE ended_at END,
		    viewer_count = CASE WHEN $2 IN ('ended','failed') THEN 0 ELSE viewer_count END,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING `+selectColumns, id, d.To, reason))
	if err != nil {
		return nil, err
	}
	if IsTerminal(d.To) {
		if _, err := tx.Exec(ctx, `DELETE FROM live_stream_presence WHERE stream_id = $1`, id); err != nil {
			return nil, err
		}
	}
	if events != nil {
		evs, err := events(prev, next)
		if err != nil {
			return nil, err
		}
		for _, e := range evs {
			if err := enqueueOutbox(ctx, tx, e); err != nil {
				return nil, err
			}
		}
	}
	if audit != nil {
		if err := insertAudit(ctx, tx, *audit); err != nil {
			return nil, err
		}
	}
	return &TransitionResult{Prev: prev, Next: next, Changed: true}, nil
}

// enqueueOutbox is commerce-service's EnqueueOutboxEvent against
// live_v2.outbox_events: same columns, same partial-unique idempotency key.
func enqueueOutbox(ctx context.Context, q querier, e OutboxEvent) error {
	var idemp any
	if e.IdempotencyKey != "" {
		idemp = e.IdempotencyKey
	}
	_, err := q.Exec(ctx, `
		INSERT INTO live_v2.outbox_events (event_type, partition_key, payload, idempotency_key)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING`,
		e.EventType, e.PartitionKey, e.Payload, idemp)
	return err
}

func insertAudit(ctx context.Context, q querier, a AuditEntry) error {
	detail := a.Detail
	if len(detail) == 0 {
		detail = []byte("{}")
	}
	_, err := q.Exec(ctx, `
		INSERT INTO live_admin_audit (actor_id, action, target_type, target_id, reason, detail)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		a.ActorID, a.Action, a.TargetType, a.TargetID, a.Reason, detail)
	return err
}

// SetEgressID records the egress job once (the first live transition).
func (s *Store) SetEgressID(ctx context.Context, id uuid.UUID, egressID string) error {
	if egressID == "" {
		return nil
	}
	_, err := s.db.Exec(ctx,
		`UPDATE live_streams SET egress_id = $2, updated_at = NOW() WHERE id = $1 AND egress_id IS NULL`,
		id, egressID)
	return err
}

// RecordingImport is one live_recording_imports job.
type RecordingImport struct {
	StreamID         uuid.UUID
	OwnerUserID      uuid.UUID
	Bucket           string
	ObjectKey        string
	RecordingURL     string
	DurationMs       int64
	State            string // pending | done | failed
	MediaID          *uuid.UUID
	ProcessingStatus *string
	Attempts         int
	LastError        *string
	DoneAt           *time.Time
	// Age is how long ago the job was queued (database clock).
	Age time.Duration
}

// Import job states.
const (
	ImportPending = "pending"
	ImportDone    = "done"
	ImportFailed  = "failed"
)

// SetRecording stores the recording pointer on the stream and queues its
// media import, in one transaction. A second egress_ended for the stream
// keeps the first job (the import is one per stream).
func (s *Store) SetRecording(ctx context.Context, id uuid.UUID, url string, durationSec int, job RecordingImport) (*LiveStream, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	st, err := scanStream(tx.QueryRow(ctx, `
		UPDATE live_streams
		SET recording_url = $2, recording_duration_seconds = $3, updated_at = NOW()
		WHERE id = $1
		RETURNING `+selectColumns, id, url, durationSec))
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO live_recording_imports
		    (stream_id, owner_user_id, bucket, object_key, recording_url, duration_ms)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (stream_id) DO NOTHING`,
		id, st.CreatorUserID, job.Bucket, job.ObjectKey, url, job.DurationMs); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return st, nil
}

const importColumns = `stream_id, owner_user_id, bucket, object_key, recording_url, duration_ms,
		          state, media_id, processing_status, attempts, last_error, done_at,
		          EXTRACT(EPOCH FROM (NOW() - created_at))::float8`

func scanImport(row pgx.Row) (RecordingImport, error) {
	var j RecordingImport
	var ageSec float64
	err := row.Scan(&j.StreamID, &j.OwnerUserID, &j.Bucket, &j.ObjectKey, &j.RecordingURL, &j.DurationMs,
		&j.State, &j.MediaID, &j.ProcessingStatus, &j.Attempts, &j.LastError, &j.DoneAt, &ageSec)
	j.Age = time.Duration(ageSec * float64(time.Second))
	return j, err
}

// ClaimDueImports leases up to limit due pending jobs for lease (pushing
// next_attempt_at forward), so concurrent sweepers do not call the importer
// for the same job at once.
func (s *Store) ClaimDueImports(ctx context.Context, limit int, lease time.Duration) ([]RecordingImport, error) {
	rows, err := s.db.Query(ctx, `
		UPDATE live_recording_imports SET next_attempt_at = NOW() + make_interval(secs => $2)
		WHERE stream_id IN (
		    SELECT stream_id FROM live_recording_imports
		    WHERE state = 'pending' AND next_attempt_at <= NOW()
		    ORDER BY next_attempt_at
		    LIMIT $1
		    FOR UPDATE SKIP LOCKED)
		RETURNING `+importColumns, limit, lease.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecordingImport
	for rows.Next() {
		j, err := scanImport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// GetImport reads one job (tests, ops).
func (s *Store) GetImport(ctx context.Context, streamID uuid.UUID) (RecordingImport, error) {
	j, err := scanImport(s.db.QueryRow(ctx, `SELECT `+importColumns+` FROM live_recording_imports WHERE stream_id = $1`, streamID))
	if errors.Is(err, pgx.ErrNoRows) {
		return j, ErrNotFound
	}
	return j, err
}

// CompleteImport records the READY media id and enqueues vod_ready in the
// same transaction. A job no longer pending is left alone (and nothing is
// enqueued).
func (s *Store) CompleteImport(ctx context.Context, streamID, mediaID uuid.UUID, events func(*LiveStream, RecordingImport) ([]OutboxEvent, error)) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	j, err := scanImport(tx.QueryRow(ctx, `
		UPDATE live_recording_imports
		SET state = 'done', media_id = $2, processing_status = 'ready', done_at = NOW(),
		    attempts = attempts + 1, last_error = NULL
		WHERE stream_id = $1 AND state = 'pending'
		RETURNING `+importColumns, streamID, mediaID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	st, err := scanStream(tx.QueryRow(ctx, `SELECT `+selectColumns+` FROM live_streams WHERE id = $1`, streamID))
	if err != nil {
		return err
	}
	evs, err := events(st, j)
	if err != nil {
		return err
	}
	for _, e := range evs {
		if err := enqueueOutbox(ctx, tx, e); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// RetryImport records an attempt that did not finish the job (an error, or
// an asset still processing) and schedules the next one.
func (s *Store) RetryImport(ctx context.Context, streamID uuid.UUID, mediaID *uuid.UUID, processingStatus, msg string, retryIn time.Duration) error {
	_, err := s.db.Exec(ctx, `
		UPDATE live_recording_imports
		SET attempts = attempts + 1, last_error = NULLIF($4, ''),
		    media_id = COALESCE($2, media_id), processing_status = COALESCE(NULLIF($3, ''), processing_status),
		    next_attempt_at = NOW() + make_interval(secs => $5)
		WHERE stream_id = $1 AND state = 'pending'`, streamID, mediaID, processingStatus, truncate(msg, 500), retryIn.Seconds())
	return err
}

// TerminateImport ends the job without vod_ready (refused import, a failed /
// rejected / deleted asset, or out of time).
func (s *Store) TerminateImport(ctx context.Context, streamID uuid.UUID, mediaID *uuid.UUID, processingStatus, msg string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE live_recording_imports
		SET state = 'failed', attempts = attempts + 1, last_error = NULLIF($4, ''), done_at = NOW(),
		    media_id = COALESCE($2, media_id), processing_status = COALESCE(NULLIF($3, ''), processing_status)
		WHERE stream_id = $1 AND state = 'pending'`, streamID, mediaID, processingStatus, truncate(msg, 500))
	return err
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ListDueForTimeout returns streams whose current status outlived its
// limit by the DATABASE clock: 'starting' older than startTimeout,
// 'reconnecting' older than grace. The transition re-checks under lock.
func (s *Store) ListDueForTimeout(ctx context.Context, startTimeout, grace time.Duration, limit int) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id FROM live_streams
		WHERE (status = 'starting' AND status_changed_at < NOW() - make_interval(secs => $1))
		   OR (status = 'reconnecting' AND status_changed_at < NOW() - make_interval(secs => $2))
		ORDER BY status_changed_at
		LIMIT $3`, startTimeout.Seconds(), grace.Seconds(), limit)
	if err != nil {
		return nil, err
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

// ListByStatuses returns streams in any of statuses, most recent status
// change first, with the count of open reports on each.
func (s *Store) ListByStatuses(ctx context.Context, statuses []string, limit int) ([]*AdminStream, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+selectColumns+`,
		       (SELECT COUNT(*) FROM live_reports r WHERE r.stream_id = live_streams.id AND r.status = 'open')::int
		FROM live_streams
		WHERE status = ANY($1)
		ORDER BY status_changed_at DESC, id DESC
		LIMIT $2`, statuses, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*AdminStream, 0)
	for rows.Next() {
		var open int
		st, err := scanStreamExtra(rows, &open)
		if err != nil {
			return nil, err
		}
		out = append(out, &AdminStream{LiveStream: *st, OpenReports: open})
	}
	return out, rows.Err()
}

// AdminStream is a stream row as the admin console lists it.
type AdminStream struct {
	LiveStream
	OpenReports int `json:"open_reports"`
}

// ApplyPresence records a participant join (present=true) or leave and
// recomputes the stream's viewer_count — present identities EXCLUDING the
// host — and viewer_peak = max(peak, count), in one transaction. A
// duplicated webhook is idempotent (one presence row per identity). Ended
// and failed streams keep their final numbers.
func (s *Store) ApplyPresence(ctx context.Context, streamID, userID uuid.UUID, present bool) (st *LiveStream, changed bool, err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	cur, err := scanStream(tx.QueryRow(ctx,
		`SELECT `+selectColumns+` FROM live_streams WHERE id = $1 FOR UPDATE`, streamID))
	if err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO live_stream_presence (stream_id, user_id, present, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (stream_id, user_id) DO UPDATE SET present = EXCLUDED.present, updated_at = NOW()`,
		streamID, userID, present); err != nil {
		return nil, false, err
	}
	if userID != cur.CreatorUserID {
		evt := "leave"
		if present {
			evt = "join"
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO live_viewer_events (stream_id, user_id, event_type) VALUES ($1, $2, $3)`,
			streamID, userID, evt); err != nil {
			return nil, false, err
		}
	}
	if IsTerminal(cur.Status) {
		if err := tx.Commit(ctx); err != nil {
			return nil, false, err
		}
		return cur, false, nil
	}
	next, err := scanStream(tx.QueryRow(ctx, `
		WITH c AS (
		    SELECT COUNT(*)::int AS n FROM live_stream_presence
		    WHERE stream_id = $1 AND present AND user_id <> $2
		)
		UPDATE live_streams SET
		    viewer_count = c.n,
		    viewer_peak = GREATEST(viewer_peak, c.n),
		    updated_at = NOW()
		FROM c
		WHERE id = $1
		RETURNING `+selectColumns, streamID, cur.CreatorUserID))
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return next, next.ViewerCount != cur.ViewerCount, nil
}

// WebhookSeen reports whether a LiveKit event id was already applied.
func (s *Store) WebhookSeen(ctx context.Context, eventID string) (bool, error) {
	var seen bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM live_webhook_events WHERE event_id = $1)`, eventID).Scan(&seen)
	return seen, err
}

// MarkWebhook records an applied event id.
func (s *Store) MarkWebhook(ctx context.Context, eventID, event string) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO live_webhook_events (event_id, event) VALUES ($1, $2) ON CONFLICT (event_id) DO NOTHING`,
		eventID, event)
	return err
}

// PruneWebhookEvents drops ids older than keep (LiveKit retries for
// minutes, not days).
func (s *Store) PruneWebhookEvents(ctx context.Context, keep time.Duration) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM live_webhook_events WHERE received_at < NOW() - make_interval(secs => $1)`, keep.Seconds())
	return err
}
