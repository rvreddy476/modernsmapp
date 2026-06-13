package counters

import (
	"context"
	"log/slog"
	"time"
)

// FlushFunc writes the current Redis sum for entityID back to its
// authoritative store (typically PostgreSQL). Called by the Worker
// for each dirty entity. The implementation owns the table + column
// and is the only thing in the system that knows the entityID is a
// UUID, an int, etc.
//
// Return an error to re-mark the entity dirty so it's retried on the
// next tick.
type FlushFunc func(ctx context.Context, entityID string, total int64) error

// Worker periodically drains the dirty set and pushes totals back to
// PG. One Worker per Counter — services typically run several (one per
// counter kind) inside the same process.
type Worker struct {
	counter   *Counter
	flush     FlushFunc
	interval  time.Duration
	batchSize int
	log       *slog.Logger
}

type WorkerOptions struct {
	// Interval is how often the worker wakes up. Default 10s — a few
	// seconds of staleness on the PG snapshot is fine because reads
	// that need realtime accuracy call Counter.Read() directly.
	Interval time.Duration

	// BatchSize caps how many entities one tick will flush. Default 1000.
	// Larger = fewer ticks but a longer tail on a single PG outage.
	BatchSize int

	// Logger is the structured logger. nil → slog.Default.
	Logger *slog.Logger
}

func NewWorker(c *Counter, flush FlushFunc, opts WorkerOptions) *Worker {
	if opts.Interval <= 0 {
		opts.Interval = 10 * time.Second
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 1000
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Worker{
		counter:   c,
		flush:     flush,
		interval:  opts.Interval,
		batchSize: opts.BatchSize,
		log:       opts.Logger,
	}
}

// Start runs the worker on its interval until ctx is cancelled. Call
// in a goroutine — it never returns under normal operation.
func (w *Worker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.Flush(ctx); err != nil {
				w.log.Warn("counter flush failed", "kind", w.counter.cfg.EntityKind, "err", err)
			}
		}
	}
}

// Flush drains a single batch from the dirty set and calls flush for
// each. Failed entities are re-marked dirty so they're retried next
// tick. Exposed so tests + integration sweeps can run a flush on
// demand without waiting for the ticker.
func (w *Worker) Flush(ctx context.Context) error {
	ids, err := w.counter.PopDirty(ctx, w.batchSize)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	var failed []string
	for _, id := range ids {
		total, err := w.counter.Read(ctx, id)
		if err != nil {
			w.log.Warn("counter flush: read failed",
				"kind", w.counter.cfg.EntityKind, "entity_id", id, "err", err)
			failed = append(failed, id)
			continue
		}
		if err := w.flush(ctx, id, total); err != nil {
			w.log.Warn("counter flush: write failed",
				"kind", w.counter.cfg.EntityKind, "entity_id", id, "err", err)
			failed = append(failed, id)
		}
	}
	if len(failed) > 0 {
		if err := w.counter.MarkDirty(ctx, failed...); err != nil {
			w.log.Warn("counter flush: mark-dirty re-queue failed",
				"kind", w.counter.cfg.EntityKind, "count", len(failed), "err", err)
		}
	}
	w.log.Debug("counter flush complete",
		"kind", w.counter.cfg.EntityKind,
		"flushed", len(ids)-len(failed),
		"failed", len(failed))
	return nil
}
