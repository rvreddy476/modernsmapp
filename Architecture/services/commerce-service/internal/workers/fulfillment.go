// Package workers — Phase 6.1 background workers. Owns the polling
// loop that drains the fulfillment_jobs queue and dispatches to the
// service-level handlers.
package workers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// FulfillmentDispatcher is the service-level subset the worker needs.
// Lets us write the worker against an interface for testing + keeps a
// circular import (workers -> service -> store) from forming.
type FulfillmentDispatcher interface {
	FulfillPaidOrderJob(ctx context.Context, orderID uuid.UUID) error
	ProcessReturnApprovedJob(ctx context.Context, returnID uuid.UUID) error
}

// FulfillmentWorker drains fulfillment_jobs. Multiple instances can run
// in parallel — FOR UPDATE SKIP LOCKED in ClaimNextJob means they won't
// race for the same row.
type FulfillmentWorker struct {
	store      *postgres.Store
	dispatcher FulfillmentDispatcher
	maxAttempts int
	pollEvery   time.Duration
	stuckAfter  time.Duration
}

// NewFulfillmentWorker constructs a worker with sane defaults: 5 retries
// before dead-letter, 1-second poll, 5-minute stuck-job recovery.
func NewFulfillmentWorker(store *postgres.Store, dispatcher FulfillmentDispatcher) *FulfillmentWorker {
	return &FulfillmentWorker{
		store:       store,
		dispatcher:  dispatcher,
		maxAttempts: 5,
		pollEvery:   1 * time.Second,
		stuckAfter:  5 * time.Minute,
	}
}

// Run blocks until ctx is cancelled. On boot it sweeps any
// processing rows the previous instance left orphaned, then enters the
// claim-dispatch loop.
func (w *FulfillmentWorker) Run(ctx context.Context) {
	if n, err := w.store.RecoverStuckProcessing(ctx, w.stuckAfter); err != nil {
		slog.Error("fulfillment worker: recover stuck failed", "error", err)
	} else if n > 0 {
		slog.Warn("fulfillment worker: recovered stuck jobs", "count", n)
	}

	stuckTicker := time.NewTicker(w.stuckAfter)
	defer stuckTicker.Stop()
	pollTicker := time.NewTicker(w.pollEvery)
	defer pollTicker.Stop()

	slog.Info("fulfillment worker started")
	for {
		select {
		case <-ctx.Done():
			slog.Info("fulfillment worker stopped")
			return
		case <-stuckTicker.C:
			if n, err := w.store.RecoverStuckProcessing(ctx, w.stuckAfter); err == nil && n > 0 {
				slog.Warn("fulfillment worker: periodic stuck recovery", "count", n)
			}
		case <-pollTicker.C:
			w.drain(ctx)
		}
	}
}

// drain keeps claiming jobs until the queue is empty or ctx fires.
func (w *FulfillmentWorker) drain(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		job, err := w.store.ClaimNextJob(ctx)
		if err != nil {
			slog.Error("fulfillment worker: claim failed", "error", err)
			return
		}
		if job == nil {
			return // queue empty
		}
		w.handle(ctx, job)
	}
}

// handle runs the right dispatcher method based on Kind. Errors are
// recorded; the row is retried until maxAttempts then dead-lettered.
func (w *FulfillmentWorker) handle(ctx context.Context, job *postgres.FulfillmentJob) {
	var execErr error
	switch job.Kind {
	case "fulfill_paid_order":
		var p struct {
			OrderID uuid.UUID `json:"order_id"`
		}
		if err := json.Unmarshal(job.Payload, &p); err != nil {
			execErr = err
			break
		}
		execErr = w.dispatcher.FulfillPaidOrderJob(ctx, p.OrderID)
	case "process_return_approved":
		var p struct {
			ReturnID uuid.UUID `json:"return_id"`
		}
		if err := json.Unmarshal(job.Payload, &p); err != nil {
			execErr = err
			break
		}
		execErr = w.dispatcher.ProcessReturnApprovedJob(ctx, p.ReturnID)
	default:
		execErr = errors.New("unknown job kind: " + job.Kind)
	}

	if execErr == nil {
		if err := w.store.CompleteJob(ctx, job.ID); err != nil {
			slog.Warn("fulfillment worker: mark complete failed", "job_id", job.ID, "error", err)
		}
		return
	}

	if job.Attempts >= w.maxAttempts {
		slog.Error("fulfillment worker: job dead-lettered",
			"job_id", job.ID, "kind", job.Kind, "attempts", job.Attempts, "error", execErr)
		if err := w.store.DeadLetterJob(ctx, job.ID, execErr); err != nil {
			slog.Warn("fulfillment worker: dead-letter persist failed", "job_id", job.ID, "error", err)
		}
		return
	}

	// Exponential backoff: 5s * 2^(attempts-1). Capped at 5 minutes.
	backoff := time.Duration(5<<uint(job.Attempts-1)) * time.Second
	if backoff > 5*time.Minute {
		backoff = 5 * time.Minute
	}
	slog.Warn("fulfillment worker: job failed, scheduling retry",
		"job_id", job.ID, "kind", job.Kind, "attempts", job.Attempts,
		"backoff", backoff, "error", execErr)
	if err := w.store.FailJob(ctx, job.ID, execErr, backoff); err != nil {
		slog.Error("fulfillment worker: failure persist failed", "job_id", job.ID, "error", err)
	}
}
