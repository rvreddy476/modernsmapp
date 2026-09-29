package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// The pair outbox's relay side (plan 6.1, "Pair relay"). Rows are claimed
// under a lease by pushing next_attempt_at forward, so two relay replicas
// never publish the same row in one lease window; a crash between publish
// and mark republishes the same event_id after the lease, and the consumer
// inbox collapses it.

// PairOutboxRow is one claimed row.
type PairOutboxRow struct {
	EventID      uuid.UUID
	PairID       uuid.UUID
	PairRevision int64
	EventType    string
	Payload      json.RawMessage
	Attempts     int
	CreatedAt    time.Time
}

// ClaimPairEvents takes up to limit due rows and leases them for `lease`.
func (s *MediaAssetStore) ClaimPairEvents(ctx context.Context, limit int, lease time.Duration) ([]PairOutboxRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		UPDATE copyright_pair_outbox o
		   SET next_attempt_at = NOW() + $2::bigint * INTERVAL '1 millisecond'
		 WHERE o.event_id IN (
		       SELECT event_id FROM copyright_pair_outbox
		        WHERE published_at IS NULL AND next_attempt_at <= NOW()
		        ORDER BY next_attempt_at
		        LIMIT $1
		        FOR UPDATE SKIP LOCKED)
		 RETURNING o.event_id, o.pair_id, o.pair_revision, o.event_type, o.payload, o.attempts, o.created_at
	`, limit, millis(lease))
	if err != nil {
		return nil, fmt.Errorf("claim pair events: %w", err)
	}
	defer rows.Close()
	var out []PairOutboxRow
	for rows.Next() {
		var r PairOutboxRow
		if err := rows.Scan(&r.EventID, &r.PairID, &r.PairRevision, &r.EventType, &r.Payload, &r.Attempts, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan pair event: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkPairEventPublished records the publish.
func (s *MediaAssetStore) MarkPairEventPublished(ctx context.Context, eventID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE copyright_pair_outbox
		   SET published_at = COALESCE(published_at, NOW()), attempts = attempts + 1, last_error = NULL
		 WHERE event_id = $1
	`, eventID)
	if err != nil {
		return fmt.Errorf("mark pair event published: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("mark pair event published: event %s not found", eventID)
	}
	return nil
}

// RecordPairEventFailure counts the attempt, stores the cause and schedules
// the retry after backoff. Returns the new attempt count.
func (s *MediaAssetStore) RecordPairEventFailure(ctx context.Context, eventID uuid.UUID, cause error, backoff time.Duration) (int, error) {
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	if len(msg) > 2000 {
		msg = msg[:2000]
	}
	var attempts int
	err := s.db.QueryRow(ctx, `
		UPDATE copyright_pair_outbox
		   SET attempts = attempts + 1, last_error = $2,
		       next_attempt_at = NOW() + $3::bigint * INTERVAL '1 millisecond'
		 WHERE event_id = $1 AND published_at IS NULL
		 RETURNING attempts
	`, eventID, msg, millis(backoff)).Scan(&attempts)
	if err != nil {
		return 0, fmt.Errorf("record pair event failure: %w", err)
	}
	return attempts, nil
}

// PairOutboxStats is what the relay exports.
type PairOutboxStats struct {
	Pending             int
	OldestPendingAgeS   float64
	StuckPastAttempts   int
	PublishedLastMinute int
}

// PairOutboxStats reads the counters. alertAttempts is the "alert after N
// attempts" threshold.
func (s *MediaAssetStore) PairOutboxStats(ctx context.Context, alertAttempts int) (PairOutboxStats, error) {
	var st PairOutboxStats
	var oldest *float64
	err := s.db.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE published_at IS NULL),
		       EXTRACT(EPOCH FROM (NOW() - MIN(created_at) FILTER (WHERE published_at IS NULL))),
		       count(*) FILTER (WHERE published_at IS NULL AND attempts >= $1),
		       count(*) FILTER (WHERE published_at > NOW() - INTERVAL '1 minute')
		  FROM copyright_pair_outbox
		 WHERE published_at IS NULL OR published_at > NOW() - INTERVAL '1 minute'
	`, alertAttempts).Scan(&st.Pending, &oldest, &st.StuckPastAttempts, &st.PublishedLastMinute)
	if err != nil {
		return st, fmt.Errorf("pair outbox stats: %w", err)
	}
	if oldest != nil {
		st.OldestPendingAgeS = *oldest
	}
	return st, nil
}
