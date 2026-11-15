package postgres

// Recording parts (migration 008): one row per LiveKit egress of a stream.
// See service/recording_parts.go for the protocol.

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Recording part states.
const (
	PartStarting  = "starting"
	PartRecording = "recording"
	PartComplete  = "complete"
	PartFailed    = "failed"
)

// RecordingPart is one live_recording_parts row.
type RecordingPart struct {
	StreamID     uuid.UUID
	Part         int
	ObjectKey    string
	EgressID     *string
	State        string
	RecordingURL *string
	DurationMs   int64
	LastError    *string
}

// PartFinish is what an egress_ended reported for one part.
type PartFinish struct {
	EgressID     string
	ObjectKey    string // the key the egress wrote, when it named one
	Complete     bool
	RecordingURL string
	DurationMs   int64
	Error        string
}

// ReservePartParams bound a reservation.
type ReservePartParams struct {
	// KeyFor is the object key of part n.
	KeyFor func(part int) string
	// MaxParts caps the parts of one stream.
	MaxParts int
	// StaleStart is how long a 'starting' reservation may stay without an
	// egress id before it is taken for abandoned (marked failed).
	StaleStart time.Duration
}

// ReserveRecordingPart reserves the stream's next part, under the stream's
// row lock, only when the stream is live, no part is starting (younger than
// StaleStart) or recording, fewer than MaxParts exist, and the stream is not
// a pre-008 recording (egress_id set with no part row). ok=false: nothing to
// start.
func (s *Store) ReserveRecordingPart(ctx context.Context, streamID uuid.UUID, p ReservePartParams) (*RecordingPart, bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	var egressID *string
	err = tx.QueryRow(ctx, `SELECT status, egress_id FROM live_streams WHERE id = $1 FOR UPDATE`, streamID).Scan(&status, &egressID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, ErrNotFound
	}
	if err != nil {
		return nil, false, err
	}
	if status != StatusLive {
		return nil, false, nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE live_recording_parts
		SET state = 'failed', last_error = 'start abandoned', ended_at = NOW()
		WHERE stream_id = $1 AND state = 'starting' AND created_at < NOW() - make_interval(secs => $2)`,
		streamID, p.StaleStart.Seconds()); err != nil {
		return nil, false, err
	}
	var total, active, maxPart int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE state IN ('starting','recording')), COALESCE(MAX(part), 0)
		FROM live_recording_parts WHERE stream_id = $1`, streamID).Scan(&total, &active, &maxPart); err != nil {
		return nil, false, err
	}
	if active > 0 || total >= p.MaxParts || (total == 0 && egressID != nil && *egressID != "") {
		return nil, false, tx.Commit(ctx)
	}
	part := &RecordingPart{StreamID: streamID, Part: maxPart + 1, State: PartStarting}
	part.ObjectKey = p.KeyFor(part.Part)
	if _, err := tx.Exec(ctx, `
		INSERT INTO live_recording_parts (stream_id, part, object_key) VALUES ($1, $2, $3)`,
		streamID, part.Part, part.ObjectKey); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return part, true, nil
}

// AttachRecordingEgress records the egress a reserved part started. Under
// the stream's row lock: when the stream has ended meanwhile (or the
// reservation was taken for abandoned), the part is failed with the id and
// attached=false — the caller stops that egress.
func (s *Store) AttachRecordingEgress(ctx context.Context, streamID uuid.UUID, part int, egressID string) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM live_streams WHERE id = $1 FOR UPDATE`, streamID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if IsTerminal(status) {
		if _, err := tx.Exec(ctx, `
			UPDATE live_recording_parts
			SET state = 'failed', egress_id = $3, last_error = 'stream ended before the recording started', ended_at = NOW()
			WHERE stream_id = $1 AND part = $2 AND state = 'starting'`, streamID, part, egressID); err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE live_recording_parts SET state = 'recording', egress_id = $3
		WHERE stream_id = $1 AND part = $2 AND state = 'starting'`, streamID, part, egressID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, tx.Commit(ctx)
}

// FailRecordingPart fails a reservation whose egress did not start.
func (s *Store) FailRecordingPart(ctx context.Context, streamID uuid.UUID, part int, msg string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE live_recording_parts SET state = 'failed', last_error = NULLIF($3, ''), ended_at = NOW()
		WHERE stream_id = $1 AND part = $2 AND state = 'starting'`, streamID, part, truncate(msg, 500))
	return err
}

// FinishRecordingPart applies an egress_ended to the part it names: by
// egress id, else (a reservation whose id was never stored) by object key.
// A part already complete or failed is left alone (a duplicate or late
// event). matched=false: no part is this egress; hasParts says whether the
// stream has any part (false = a pre-008 recording).
func (s *Store) FinishRecordingPart(ctx context.Context, streamID uuid.UUID, f PartFinish) (matched, hasParts bool, err error) {
	state := PartFailed
	if f.Complete {
		state = PartComplete
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE live_recording_parts
		SET state = $4, egress_id = COALESCE(egress_id, NULLIF($2, '')),
		    recording_url = NULLIF($5, ''), duration_ms = $6, last_error = NULLIF($7, ''), ended_at = NOW()
		WHERE stream_id = $1 AND state IN ('starting','recording')
		  AND ((NULLIF($2, '') IS NOT NULL AND egress_id = $2)
		       OR (egress_id IS NULL AND NULLIF($3, '') IS NOT NULL AND object_key = $3))`,
		streamID, f.EgressID, f.ObjectKey, state, f.RecordingURL, f.DurationMs, truncate(f.Error, 500))
	if err != nil {
		return false, false, err
	}
	if tag.RowsAffected() > 0 {
		return true, true, nil
	}
	err = s.db.QueryRow(ctx, `
		SELECT COALESCE(bool_or((NULLIF($2, '') IS NOT NULL AND egress_id = $2)
		                        OR (NULLIF($3, '') IS NOT NULL AND object_key = $3)), false),
		       COUNT(*) > 0
		FROM live_recording_parts WHERE stream_id = $1`, streamID, f.EgressID, f.ObjectKey).Scan(&matched, &hasParts)
	return matched, hasParts, err
}

// ActiveRecordingEgresses lists the egress ids of the stream's parts still
// recording.
func (s *Store) ActiveRecordingEgresses(ctx context.Context, streamID uuid.UUID) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT egress_id FROM live_recording_parts
		WHERE stream_id = $1 AND state = 'recording' AND egress_id IS NOT NULL ORDER BY part`, streamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ListRecordingParts reads a stream's parts in order (tests, ops).
func (s *Store) ListRecordingParts(ctx context.Context, streamID uuid.UUID) ([]RecordingPart, error) {
	rows, err := s.db.Query(ctx, `
		SELECT stream_id, part, object_key, egress_id, state, recording_url, duration_ms, last_error
		FROM live_recording_parts WHERE stream_id = $1 ORDER BY part`, streamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecordingPart
	for rows.Next() {
		var p RecordingPart
		if err := rows.Scan(&p.StreamID, &p.Part, &p.ObjectKey, &p.EgressID, &p.State, &p.RecordingURL, &p.DurationMs, &p.LastError); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// QueueImportParams bound QueueRecordingImport.
type QueueImportParams struct {
	Bucket string
	// URLFor is the recording URL of a key, for a part whose egress named
	// no location.
	URLFor func(key string) string
	// WaitActive is how long after the stream ended a part still starting
	// or recording holds the import back (its egress_ended may be lost).
	WaitActive time.Duration
}

// QueueRecordingImport queues the stream's media import from its longest
// complete part (ties: the earliest), with the recording pointer on the
// stream, in one transaction under the stream's row lock — only once the
// stream is terminal and no part is still starting or recording (or it
// ended more than WaitActive ago). queued=false: not yet, nothing complete,
// or a job already exists (the import is one per stream).
func (s *Store) QueueRecordingImport(ctx context.Context, streamID uuid.UUID, p QueueImportParams) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	var endedLongAgo bool
	err = tx.QueryRow(ctx, `
		SELECT status, COALESCE(ended_at, status_changed_at) < NOW() - make_interval(secs => $2)
		FROM live_streams WHERE id = $1 FOR UPDATE`, streamID, p.WaitActive.Seconds()).Scan(&status, &endedLongAgo)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if !IsTerminal(status) {
		return false, nil
	}
	var queued bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM live_recording_imports WHERE stream_id = $1)`, streamID).Scan(&queued); err != nil {
		return false, err
	}
	if queued {
		return false, nil
	}
	var active int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM live_recording_parts
		WHERE stream_id = $1 AND state IN ('starting','recording')`, streamID).Scan(&active); err != nil {
		return false, err
	}
	if active > 0 && !endedLongAgo {
		return false, nil
	}
	var part RecordingPart
	err = tx.QueryRow(ctx, `
		SELECT part, object_key, recording_url, duration_ms FROM live_recording_parts
		WHERE stream_id = $1 AND state = 'complete'
		ORDER BY duration_ms DESC, part ASC LIMIT 1`, streamID).Scan(&part.Part, &part.ObjectKey, &part.RecordingURL, &part.DurationMs)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	url := ""
	if part.RecordingURL != nil {
		url = *part.RecordingURL
	}
	if url == "" && p.URLFor != nil {
		url = p.URLFor(part.ObjectKey)
	}
	var owner uuid.UUID
	if err := tx.QueryRow(ctx, `
		UPDATE live_streams
		SET recording_url = $2, recording_duration_seconds = $3, updated_at = NOW()
		WHERE id = $1 RETURNING creator_user_id`, streamID, url, int(part.DurationMs/1000)).Scan(&owner); err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO live_recording_imports
		    (stream_id, owner_user_id, bucket, object_key, recording_url, duration_ms)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (stream_id) DO NOTHING`,
		streamID, owner, p.Bucket, part.ObjectKey, url, part.DurationMs)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ListAwaitingImport returns terminal streams with a complete part (made in
// the last `within`) and no import job: the sweeper's backstop for an import
// no webhook or transition queued.
func (s *Store) ListAwaitingImport(ctx context.Context, within time.Duration, limit int) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT p.stream_id
		FROM live_recording_parts p
		JOIN live_streams st ON st.id = p.stream_id
		WHERE p.state = 'complete' AND p.created_at > NOW() - make_interval(secs => $1)
		  AND st.status IN ('ended','failed')
		  AND NOT EXISTS (SELECT 1 FROM live_recording_imports i WHERE i.stream_id = p.stream_id)
		LIMIT $2`, within.Seconds(), limit)
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
