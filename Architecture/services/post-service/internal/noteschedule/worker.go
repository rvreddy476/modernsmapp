// Package noteschedule publishes scheduled Echo notes when they fall due
// (docs/designs/echo-v2-contract.md §3).
//
// Each tick asks the service for one pass (service.PublishDueNotes): claim
// the due notes with FOR UPDATE SKIP LOCKED, leasing each for ten minutes,
// then publish them one by one. Safe on every replica: two replicas
// claiming at once take disjoint notes, and the publish itself is a
// compare-and-set on publish_at, so no note is announced twice. Publishing
// re-checks standing and Premium; a refusal keeps the note scheduled with
// the reason, retried hourly for 24 hours, then failed.
//
// The ordinary post schedule worker (internal/postschedule) never takes a
// note.
package noteschedule

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/atpost/post-service/internal/service"
)

// Publisher runs one pass; *service.Service implements it.
type Publisher interface {
	PublishDueNotes(ctx context.Context, limit int) (service.NotePublishReport, error)
}

// Config is read by ConfigFromEnv.
type Config struct {
	Interval time.Duration // NOTE_SCHEDULE_INTERVAL, default 30s
	Batch    int           // NOTE_SCHEDULE_BATCH, default 50
}

// ConfigFromEnv reads the two variables; a bad value keeps the default.
func ConfigFromEnv() Config {
	cfg := Config{Interval: 30 * time.Second, Batch: 50}
	if v := os.Getenv("NOTE_SCHEDULE_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.Interval = d
		}
	}
	return cfg
}

// Worker runs the loop.
type Worker struct {
	pub Publisher
	cfg Config
	log *slog.Logger
}

// NewWorker builds a worker; log may be nil.
func NewWorker(pub Publisher, cfg Config, log *slog.Logger) *Worker {
	if log == nil {
		log = slog.Default()
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.Batch <= 0 {
		cfg.Batch = 50
	}
	return &Worker{pub: pub, cfg: cfg, log: log}
}

// Start ticks until ctx is cancelled, once immediately.
func (w *Worker) Start(ctx context.Context) {
	w.log.Info("note schedule worker started", "interval", w.cfg.Interval)
	w.Tick(ctx)
	t := time.NewTicker(w.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.Tick(ctx)
		}
	}
}

// Tick runs passes until one claims less than a full batch.
func (w *Worker) Tick(ctx context.Context) service.NotePublishReport {
	var total service.NotePublishReport
	for ctx.Err() == nil {
		rep, err := w.pub.PublishDueNotes(ctx, w.cfg.Batch)
		total.Claimed += rep.Claimed
		total.Published += rep.Published
		total.Deferred += rep.Deferred
		if err != nil {
			w.log.Error("note schedule: pass failed", "err", err)
			break
		}
		if rep.Claimed < w.cfg.Batch {
			break
		}
	}
	if total.Claimed > 0 {
		w.log.Info("note schedule: tick", "claimed", total.Claimed, "published", total.Published, "deferred", total.Deferred)
	}
	return total
}
