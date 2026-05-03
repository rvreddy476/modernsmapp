// Cron-runs store: every background-job invocation writes one row in
// rider_cron_runs (started/finished/status/rows_processed/error_summary).
// The cron framework uses this both as audit trail and as an advisory lock
// to skip re-entry when a previous run is still 'running'.
//
// Spec ref: mopedu/MOPEDU_SPEC.md §15. Sprint 4 schema additions.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrCronRunNotFound is returned when GetCronRun finds no row.
var ErrCronRunNotFound = errors.New("cron_run: not found")

// CronRun is one row in rider_cron_runs.
type CronRun struct {
	ID            uuid.UUID  `json:"id"`
	Job           string     `json:"job"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	Status        string     `json:"status"`
	RowsProcessed int        `json:"rows_processed"`
	ErrorSummary  *string    `json:"error_summary,omitempty"`
}

// StartCronRun inserts a `running` row and returns its id. The cron runner
// closes the row via FinishCronRun once the job returns.
func (s *Store) StartCronRun(ctx context.Context, job string) (uuid.UUID, error) {
	if job == "" {
		return uuid.Nil, fmt.Errorf("cron_run: job required")
	}
	const q = `
        INSERT INTO rider_cron_runs (job, status)
        VALUES ($1, 'running')
        RETURNING id`
	var id uuid.UUID
	if err := s.db.QueryRow(ctx, q, job).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("start cron run: %w", err)
	}
	return id, nil
}

// FinishCronRun closes the row. jobErr=nil flips status='succeeded'; non-nil
// flips status='failed' and records the truncated error summary.
func (s *Store) FinishCronRun(ctx context.Context, id uuid.UUID, rowsProcessed int, jobErr error) error {
	if id == uuid.Nil {
		return fmt.Errorf("cron_run: id required")
	}
	status := "succeeded"
	var summary *string
	if jobErr != nil {
		status = "failed"
		s := truncateErr(jobErr.Error(), 1024)
		summary = &s
	}
	const q = `
        UPDATE rider_cron_runs
        SET finished_at    = NOW(),
            status         = $2,
            rows_processed = $3,
            error_summary  = $4
        WHERE id = $1`
	tag, err := s.db.Exec(ctx, q, id, status, rowsProcessed, summary)
	if err != nil {
		return fmt.Errorf("finish cron run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrCronRunNotFound
	}
	return nil
}

// HasRunningCronRun returns true when the named job has any row in
// status='running' that started within the lookback window. Used as the
// advisory lock so a tick coincidence (or pod restart mid-job) doesn't
// kick off a second invocation.
func (s *Store) HasRunningCronRun(ctx context.Context, job string, within time.Duration) (bool, error) {
	if within <= 0 {
		within = 2 * time.Hour
	}
	const q = `
        SELECT EXISTS (
            SELECT 1 FROM rider_cron_runs
            WHERE job = $1
              AND status = 'running'
              AND started_at >= NOW() - ($2::int * INTERVAL '1 second')
        )`
	var exists bool
	if err := s.db.QueryRow(ctx, q, job, int(within.Seconds())).Scan(&exists); err != nil {
		return false, fmt.Errorf("has running cron run: %w", err)
	}
	return exists, nil
}

// CronRunFilter is the listing filter for ListCronRuns.
type CronRunFilter struct {
	Job    string
	Since  *time.Time
	Limit  int
	Offset int
}

// ListCronRuns returns cron-run rows newest-first matching the filter.
// Bounded at 500 rows. Used by /v1/rider/admin/reports/cron-runs.
func (s *Store) ListCronRuns(ctx context.Context, f CronRunFilter) ([]CronRun, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	q := `
        SELECT id, job, started_at, finished_at, status, rows_processed, error_summary
        FROM rider_cron_runs
        WHERE ($1::text IS NULL OR job = $1)
          AND ($2::timestamptz IS NULL OR started_at >= $2)
        ORDER BY started_at DESC
        LIMIT $3 OFFSET $4`
	var jobPtr *string
	if f.Job != "" {
		jobPtr = &f.Job
	}
	rows, err := s.db.Query(ctx, q, jobPtr, f.Since, f.Limit, f.Offset)
	if err != nil {
		return nil, fmt.Errorf("list cron runs: %w", err)
	}
	defer rows.Close()
	var out []CronRun
	for rows.Next() {
		var c CronRun
		if err := rows.Scan(&c.ID, &c.Job, &c.StartedAt, &c.FinishedAt, &c.Status, &c.RowsProcessed, &c.ErrorSummary); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCronRun returns one row by id.
func (s *Store) GetCronRun(ctx context.Context, id uuid.UUID) (*CronRun, error) {
	const q = `
        SELECT id, job, started_at, finished_at, status, rows_processed, error_summary
        FROM rider_cron_runs
        WHERE id = $1`
	var c CronRun
	row := s.db.QueryRow(ctx, q, id)
	if err := row.Scan(&c.ID, &c.Job, &c.StartedAt, &c.FinishedAt, &c.Status, &c.RowsProcessed, &c.ErrorSummary); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCronRunNotFound
		}
		return nil, err
	}
	return &c, nil
}

// truncateErr trims an error string to at most n bytes, suffixing with
// "...[truncated]" when truncation happened.
func truncateErr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...[truncated]"
}
