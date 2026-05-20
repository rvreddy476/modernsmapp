// Phase 6.1 — durable fulfillment job queue. Replaces fire-and-forget
// `go s.fulfillPaidOrder()` goroutines that die on service restart.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// FulfillmentJob is one row in fulfillment_jobs. Payload semantics are
// owned by the dispatcher (kind => handler).
type FulfillmentJob struct {
	ID         int64           `db:"id"`
	Kind       string          `db:"kind"`
	Payload    json.RawMessage `db:"payload"`
	Status     string          `db:"status"`
	Attempts   int             `db:"attempts"`
	NextRunAt  time.Time       `db:"next_run_at"`
	LastError  *string         `db:"last_error"`
	CreatedAt  time.Time       `db:"created_at"`
	StartedAt  *time.Time      `db:"started_at"`
	Completed  *time.Time      `db:"completed_at"`
	DeadLetter *time.Time      `db:"dead_letter_at"`
}

// EnqueueJob inserts a new job. Caller passes a tx so the enqueue rides
// the same transaction as the domain write (the whole point of an outbox).
func (s *Store) EnqueueJob(ctx context.Context, tx pgx.Tx, kind string, payload []byte) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO fulfillment_jobs (kind, payload, status, next_run_at)
		VALUES ($1, $2, 'pending', NOW())`, kind, payload)
	return err
}

// EnqueueJobPool is the non-tx variant for places where the calling code
// doesn't open its own transaction (e.g. payments-consumer event handler).
// Less safe than EnqueueJob — the work may run before the caller's other
// writes commit. Prefer EnqueueJob unless the caller has only one write.
func (s *Store) EnqueueJobPool(ctx context.Context, kind string, payload []byte) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO fulfillment_jobs (kind, payload, status, next_run_at)
		VALUES ($1, $2, 'pending', NOW())`, kind, payload)
	return err
}

// ClaimNextJob atomically picks the next due job and marks it processing.
// FOR UPDATE SKIP LOCKED lets multiple worker pods cooperate without
// double-claiming a row. Returns nil + nil error when the queue is empty.
func (s *Store) ClaimNextJob(ctx context.Context) (*FulfillmentJob, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var j FulfillmentJob
	err = tx.QueryRow(ctx, `
		SELECT id, kind, payload, status, attempts, next_run_at,
		       last_error, created_at, started_at, completed_at, dead_letter_at
		FROM fulfillment_jobs
		WHERE status = 'pending' AND next_run_at <= NOW()
		ORDER BY next_run_at ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED`).Scan(
		&j.ID, &j.Kind, &j.Payload, &j.Status, &j.Attempts, &j.NextRunAt,
		&j.LastError, &j.CreatedAt, &j.StartedAt, &j.Completed, &j.DeadLetter,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE fulfillment_jobs
		SET status = 'processing', started_at = NOW(), attempts = attempts + 1
		WHERE id = $1`, j.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	j.Status = "processing"
	j.Attempts++
	return &j, nil
}

// CompleteJob marks a successfully-handled job as done.
func (s *Store) CompleteJob(ctx context.Context, id int64) error {
	_, err := s.db.Exec(ctx, `
		UPDATE fulfillment_jobs
		SET status = 'done', completed_at = NOW(), last_error = NULL
		WHERE id = $1`, id)
	return err
}

// FailJob bumps the next_run_at by the backoff and records the error. If
// the worker decides we're past the retry budget, it calls DeadLetterJob
// instead.
func (s *Store) FailJob(ctx context.Context, id int64, err error, retryAfter time.Duration) error {
	_, dbErr := s.db.Exec(ctx, `
		UPDATE fulfillment_jobs
		SET status = 'pending',
		    next_run_at = NOW() + ($2::interval),
		    last_error = $3
		WHERE id = $1`, id, retryAfter.String(), err.Error())
	return dbErr
}

// DeadLetterJob is the terminal failure path — the worker stops retrying
// and an admin must inspect / replay.
func (s *Store) DeadLetterJob(ctx context.Context, id int64, err error) error {
	_, dbErr := s.db.Exec(ctx, `
		UPDATE fulfillment_jobs
		SET status = 'dead', dead_letter_at = NOW(), last_error = $2
		WHERE id = $1`, id, err.Error())
	return dbErr
}

// CountStuckProcessing counts rows that have been in 'processing' for
// longer than the supplied threshold — useful for ops health checks (a
// crashed worker leaves rows stuck without releasing them).
func (s *Store) CountStuckProcessing(ctx context.Context, olderThan time.Duration) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM fulfillment_jobs
		WHERE status = 'processing' AND started_at < NOW() - ($1::interval)`,
		olderThan.String()).Scan(&n)
	return n, err
}

// RecoverStuckProcessing flips back-to-pending any job that's been in
// processing longer than the threshold. Workers should run this on boot
// + periodically so a crashed pod doesn't strand its claimed work.
func (s *Store) RecoverStuckProcessing(ctx context.Context, olderThan time.Duration) (int, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE fulfillment_jobs
		SET status = 'pending',
		    next_run_at = NOW(),
		    started_at = NULL
		WHERE status = 'processing' AND started_at < NOW() - ($1::interval)`,
		olderThan.String())
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// ListDeadLetterJobs returns the dead-letter queue for admin inspection.
func (s *Store) ListDeadLetterJobs(ctx context.Context, limit int) ([]*FulfillmentJob, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, kind, payload, status, attempts, next_run_at,
		       last_error, created_at, started_at, completed_at, dead_letter_at
		FROM fulfillment_jobs
		WHERE status = 'dead'
		ORDER BY dead_letter_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*FulfillmentJob
	for rows.Next() {
		var j FulfillmentJob
		if err := rows.Scan(&j.ID, &j.Kind, &j.Payload, &j.Status, &j.Attempts, &j.NextRunAt,
			&j.LastError, &j.CreatedAt, &j.StartedAt, &j.Completed, &j.DeadLetter); err != nil {
			return nil, err
		}
		out = append(out, &j)
	}
	return out, rows.Err()
}

// ─── Phase 6.2 — inventory reservation expiry ────────────────

// ExpireInventoryReservations releases reservations whose expires_at has
// passed. Returns the number of rows freed. Idempotent — safe to run on
// a cron / periodic worker.
func (s *Store) ExpireInventoryReservations(ctx context.Context) (int, error) {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM inventory_reservations
		WHERE expires_at <= NOW() AND order_id IS NULL`)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
