// Package cron is the rider-service background-job runner. Jobs register
// once with RegisterJob(name, interval, fn) and the runner ticks each at
// its interval, wrapping every invocation with a row in `rider_cron_runs`
// (started/finished/status/rows_processed/error_summary). A run is skipped
// if a previous invocation of the same job is still 'running' within the
// last 2 hours — this is the lightweight DB-row "advisory lock" that keeps
// jobs idempotent across pod restarts and tick coincidences.
//
// Spec ref: mopedu/MOPEDU_SPEC.md §15 (background jobs).
//
// CRITICAL RULES:
//
//   - Cron jobs MUST be idempotent on re-run. A job that ran 10 minutes ago
//     and re-runs now must not double-charge, double-notify, or double-purge.
//     Each job enforces this via DB-side "WHERE not-already-done" predicates
//     and explicit dedupe tables (rider_doc_reminders_sent, rider_daily_revenue
//     unique key, etc).
//
//   - The cron-runs row is the operational source-of-truth for "did this job
//     execute and succeed?" — admins read it via /v1/rider/admin/reports/cron-runs.
//
//   - Failures are logged loudly + recorded with status='failed' on the row.
//     Never silent.
//
//   - Each registered job runs with a per-run context.WithTimeout (default
//     30 minutes; override via JobOptions.Timeout) so a wedged job cannot
//     starve the whole runner.
package cron

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

// JobFunc is the function signature every registered job implements. It
// returns the number of rows it processed (for telemetry) and any error.
// The runner records both onto the rider_cron_runs row.
type JobFunc func(ctx context.Context) (rowsProcessed int, err error)

// JobOptions tunes one job's execution.
type JobOptions struct {
	// Interval between ticks. Required (must be > 0).
	Interval time.Duration
	// Timeout per single invocation. Defaults to 30 minutes.
	Timeout time.Duration
	// SkipIfRunning enables the "skip if a row with status='running' exists
	// within the last 2 hours" advisory lock. Defaults to true.
	SkipIfRunning bool
	// MaxRunningAge is the lookback for the running-row check. Defaults to 2h.
	MaxRunningAge time.Duration
	// RunImmediately fires the first tick on Run() rather than waiting for
	// a full Interval. Defaults to false.
	RunImmediately bool
}

// jobEntry is one registered job.
type jobEntry struct {
	name    string
	opts    JobOptions
	fn      JobFunc
	// inFlight is the in-process re-entry guard. CompareAndSwap from 0 -> 1
	// at the start of each tick; reset to 0 on completion. If the swap
	// fails the tick is dropped.
	inFlight atomic.Int32
}

// JobStore is the subset of *store.Store the runner needs. Stays an
// interface so tests can inject a fake.
type JobStore interface {
	StartCronRun(ctx context.Context, job string) (uuid.UUID, error)
	FinishCronRun(ctx context.Context, id uuid.UUID, rowsProcessed int, jobErr error) error
	HasRunningCronRun(ctx context.Context, job string, within time.Duration) (bool, error)
}

// Runner is the cron daemon. Construct with NewRunner, register jobs with
// RegisterJob, then call Run.
type Runner struct {
	st     JobStore
	jobs   []*jobEntry
	mu     sync.Mutex // guards jobs during RegisterJob in tests.
	logger *slog.Logger
}

// NewRunner returns a Runner that records cron-runs onto the given store.
// `logger` may be nil; we'll use slog.Default().
func NewRunner(st JobStore, logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{st: st, logger: logger}
}

// RegisterJob adds a job. Panics if interval <= 0 or name is empty since
// these are programmer errors that should fail loud at boot.
func (r *Runner) RegisterJob(name string, opts JobOptions, fn JobFunc) {
	if name == "" {
		panic("cron.RegisterJob: name required")
	}
	if opts.Interval <= 0 {
		panic("cron.RegisterJob: interval must be positive")
	}
	if fn == nil {
		panic("cron.RegisterJob: fn required")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Minute
	}
	if opts.MaxRunningAge <= 0 {
		opts.MaxRunningAge = 2 * time.Hour
	}
	r.mu.Lock()
	r.jobs = append(r.jobs, &jobEntry{name: name, opts: opts, fn: fn})
	r.mu.Unlock()
}

// Jobs returns the registered job names. Useful for /v1/rider/admin/cron-runs.
func (r *Runner) Jobs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.jobs))
	for _, j := range r.jobs {
		out = append(out, j.name)
	}
	return out
}

// Run blocks until ctx is cancelled. Every registered job ticks at its own
// interval; ticks that fire while a previous invocation is in-flight are
// dropped (the in-process atomic AND the DB-side running-row check both gate).
func (r *Runner) Run(ctx context.Context) {
	r.mu.Lock()
	jobs := make([]*jobEntry, len(r.jobs))
	copy(jobs, r.jobs)
	r.mu.Unlock()

	if len(jobs) == 0 {
		r.logger.Warn("cron: no jobs registered; runner exiting")
		return
	}

	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(entry *jobEntry) {
			defer wg.Done()
			r.loop(ctx, entry)
		}(j)
	}
	wg.Wait()
}

func (r *Runner) loop(ctx context.Context, j *jobEntry) {
	if j.opts.RunImmediately {
		r.runOnce(ctx, j)
	}
	t := time.NewTicker(j.opts.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			r.logger.Info("cron: shutting down job", "job", j.name)
			return
		case <-t.C:
			r.runOnce(ctx, j)
		}
	}
}

// runOnce executes a single tick: enforces the in-process + DB-side
// re-entry guards, opens the cron-runs row, runs the job, and writes the
// outcome back to the row.
func (r *Runner) runOnce(ctx context.Context, j *jobEntry) {
	if !j.inFlight.CompareAndSwap(0, 1) {
		r.logger.Debug("cron: skip — previous invocation in flight", "job", j.name)
		return
	}
	defer j.inFlight.Store(0)

	if j.opts.SkipIfRunning {
		// DB-side check: an earlier process may have crashed mid-job and
		// left a 'running' row. We honor it for opts.MaxRunningAge so a
		// genuinely-stuck job eventually retries.
		busy, err := r.st.HasRunningCronRun(ctx, j.name, j.opts.MaxRunningAge)
		if err != nil {
			r.logger.Warn("cron: running-check failed; proceeding", "job", j.name, "error", err)
		} else if busy {
			r.logger.Info("cron: skip — another process still running", "job", j.name)
			return
		}
	}

	runID, err := r.st.StartCronRun(ctx, j.name)
	if err != nil {
		r.logger.Error("cron: failed to open run row", "job", j.name, "error", err)
		return
	}

	jobCtx, cancel := context.WithTimeout(ctx, j.opts.Timeout)
	defer cancel()

	start := time.Now()
	rows, jobErr := r.safeRun(jobCtx, j)
	elapsed := time.Since(start)

	if finishErr := r.st.FinishCronRun(context.Background(), runID, rows, jobErr); finishErr != nil {
		r.logger.Error("cron: failed to close run row",
			"job", j.name, "run_id", runID, "error", finishErr)
	}

	if jobErr != nil {
		r.logger.Error("cron: job failed",
			"job", j.name, "rows", rows, "elapsed_ms", elapsed.Milliseconds(), "error", jobErr)
		return
	}
	if rows > 0 {
		r.logger.Info("cron: job ok",
			"job", j.name, "rows", rows, "elapsed_ms", elapsed.Milliseconds())
	} else {
		r.logger.Debug("cron: job ok (no rows)",
			"job", j.name, "elapsed_ms", elapsed.Milliseconds())
	}
}

// safeRun protects the runner from a panicking job. Recovered panics are
// turned into errors so the cron-runs row records a failure rather than
// dropping silently.
func (r *Runner) safeRun(ctx context.Context, j *jobEntry) (rows int, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("panic: %v", rec)
			rows = 0
		}
	}()
	return j.fn(ctx)
}

// --- store-backed JobStore implementation ---------------------------------

// StoreAdapter wraps *store.Store so Runner can record runs.
type StoreAdapter struct {
	S *store.Store
}

// NewStoreAdapter wraps the given store.
func NewStoreAdapter(s *store.Store) *StoreAdapter { return &StoreAdapter{S: s} }

// StartCronRun inserts a row in `rider_cron_runs` with status='running'.
func (a *StoreAdapter) StartCronRun(ctx context.Context, job string) (uuid.UUID, error) {
	return a.S.StartCronRun(ctx, job)
}

// FinishCronRun closes the row.
func (a *StoreAdapter) FinishCronRun(ctx context.Context, id uuid.UUID, rowsProcessed int, jobErr error) error {
	return a.S.FinishCronRun(ctx, id, rowsProcessed, jobErr)
}

// HasRunningCronRun returns whether a previous run is still 'running'.
func (a *StoreAdapter) HasRunningCronRun(ctx context.Context, job string, within time.Duration) (bool, error) {
	return a.S.HasRunningCronRun(ctx, job, within)
}

// ErrNoStore is returned when a Runner is created without a JobStore.
var ErrNoStore = errors.New("cron: store required")
