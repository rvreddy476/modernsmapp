// Package actionrecheck is the daily safety recheck of Action-button links
// (pinned contract, 23 Oct 2026): every live link whose Web Risk verdict is
// older than 24 h is looked up again, and a link that has turned unsafe is
// blocked — the button disappears for viewers, its /l/ link stops
// resolving, and the creator sees "Link blocked: unsafe".
//
// Safe in every replica at once, the way postschedule is: the store claims
// a bounded batch with FOR UPDATE SKIP LOCKED and a claim deadline
// (store/postgres/action_links.go ClaimActionLinksForRecheck), so two
// workers never check the same link and a failed lookup waits out its
// claim before it is retried.
package actionrecheck

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/atpost/post-service/internal/service"
)

// Rechecker runs one bounded batch. *service.Service implements it.
type Rechecker interface {
	RecheckActionLinks(ctx context.Context, batch int) (service.ActionRecheckReport, error)
}

// Config is read by ConfigFromEnv.
type Config struct {
	// Interval is the tick (ACTION_LINK_RECHECK_INTERVAL, default 15m).
	Interval time.Duration
	// Batch bounds one claim (ACTION_LINK_RECHECK_BATCH, default 200).
	Batch int
	// MaxBatches bounds one tick (default 10), so a backlog drains over
	// several ticks instead of holding the checker's quota in one burst.
	MaxBatches int
}

// ConfigFromEnv reads ACTION_LINK_RECHECK_INTERVAL and _BATCH.
func ConfigFromEnv() Config {
	cfg := Config{Interval: 15 * time.Minute, Batch: 200, MaxBatches: 10}
	if v := os.Getenv("ACTION_LINK_RECHECK_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.Interval = d
		}
	}
	if v := os.Getenv("ACTION_LINK_RECHECK_BATCH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			cfg.Batch = n
		}
	}
	return cfg
}

// Worker runs the recheck loop.
type Worker struct {
	r   Rechecker
	cfg Config
	log *slog.Logger
}

// NewWorker builds a worker. log may be nil.
func NewWorker(r Rechecker, cfg Config, log *slog.Logger) *Worker {
	if log == nil {
		log = slog.Default()
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 15 * time.Minute
	}
	if cfg.Batch <= 0 {
		cfg.Batch = 200
	}
	if cfg.MaxBatches <= 0 {
		cfg.MaxBatches = 10
	}
	return &Worker{r: r, cfg: cfg, log: log}
}

// Start ticks until ctx is cancelled; one tick runs at once.
func (w *Worker) Start(ctx context.Context) {
	w.log.Info("action link recheck worker started", "interval", w.cfg.Interval, "batch", w.cfg.Batch)
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

// Tick drains due links, at most MaxBatches batches.
func (w *Worker) Tick(ctx context.Context) service.ActionRecheckReport {
	var total service.ActionRecheckReport
	for i := 0; i < w.cfg.MaxBatches && ctx.Err() == nil; i++ {
		rep, err := w.r.RecheckActionLinks(ctx, w.cfg.Batch)
		if err != nil {
			w.log.Error("action link recheck: claim failed", "err", err)
			break
		}
		total.Checked += rep.Checked
		total.Blocked += rep.Blocked
		total.Unavailable += rep.Unavailable
		// A short batch means nothing else is due; a batch that only failed
		// lookups means the checker is down: wait for the next tick.
		if rep.Checked+rep.Unavailable < w.cfg.Batch || rep.Checked == 0 {
			break
		}
	}
	if total.Checked > 0 || total.Unavailable > 0 {
		w.log.Info("action link recheck", "checked", total.Checked, "blocked", total.Blocked, "unavailable", total.Unavailable)
	}
	return total
}
