// Package postschedule publishes scheduled posts when they fall due
// (founder, 2026-09-05: the reel studio publishes — or SCHEDULES).
//
// POST /v1/posts with `publish_at` stores the post author-only with
// posts.publish_at set and emits nothing. This worker runs every Interval
// and, for every live post whose publish_at is at or before now, asks the
// service to publish it: one guarded UPDATE flips the row live, moves
// created_at to the publish moment and writes the PostCreated outbox row in
// the same transaction (store.PublishScheduledPost), then the post announces
// itself exactly like a fresh one (mentions, hashtag counters, live
// pub/sub).
//
// Idempotent and safe in every replica: the flip is a compare-and-set on
// publish_at, so two ticks — or a tick and the author's "publish now" —
// racing on one post serialise on the row lock and the loser is a no-op.
// A post that is deleted (deleted_at set) is never published; if it is
// restored after its publish_at, the next tick publishes it.
package postschedule

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Store is what the worker needs from Postgres. *postgres.Store implements
// it; tests substitute a fake.
type Store interface {
	ListDueScheduledPosts(ctx context.Context, now time.Time, limit int) ([]postgres.ScheduledCandidate, error)
}

// Publisher makes one due post live. It must report false — not an error —
// when the post was not scheduled any more, so a concurrent run is a
// no-op. *service.Service implements it (PublishScheduled).
type Publisher interface {
	PublishScheduled(ctx context.Context, postID uuid.UUID, authorID *uuid.UUID, dueOnly bool) (bool, error)
}

// Config is read from the environment by ConfigFromEnv.
type Config struct {
	// Interval is the tick (POST_SCHEDULE_INTERVAL, default 30s).
	Interval time.Duration
	// Batch bounds one tick's work (POST_SCHEDULE_BATCH, default 100).
	Batch int
}

// ConfigFromEnv reads POST_SCHEDULE_INTERVAL.
func ConfigFromEnv() Config {
	cfg := Config{Interval: 30 * time.Second, Batch: 100}
	if v := os.Getenv("POST_SCHEDULE_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.Interval = d
		}
	}
	return cfg
}

// Worker runs the publish loop.
type Worker struct {
	store     Store
	publisher Publisher
	cfg       Config
	now       func() time.Time
	log       *slog.Logger
}

// NewWorker builds a worker. log may be nil.
func NewWorker(store Store, publisher Publisher, cfg Config, log *slog.Logger) *Worker {
	if log == nil {
		log = slog.Default()
	}
	if cfg.Batch <= 0 {
		cfg.Batch = 100
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	return &Worker{store: store, publisher: publisher, cfg: cfg,
		now: func() time.Time { return time.Now().UTC() }, log: log}
}

// WithClock overrides the time source (tests).
func (w *Worker) WithClock(now func() time.Time) *Worker { w.now = now; return w }

// Start ticks until ctx is cancelled. One tick runs immediately so a
// restarted pod does not wait a full interval to resume a backlog.
func (w *Worker) Start(ctx context.Context) {
	w.log.Info("post schedule worker started", "interval", w.cfg.Interval)
	w.Tick(ctx)
	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.Tick(ctx)
		}
	}
}

// TickReport is what one Tick did (tests, logs).
type TickReport struct {
	Published int
	Skipped   int // due when listed, not scheduled any more when flipped
	Failed    int
}

// Tick publishes every due post.
func (w *Worker) Tick(ctx context.Context) TickReport {
	var rep TickReport
	cands, err := w.store.ListDueScheduledPosts(ctx, w.now(), w.cfg.Batch)
	if err != nil {
		w.log.Error("post schedule: list failed", "err", err)
		return rep
	}
	for _, c := range cands {
		if ctx.Err() != nil {
			return rep
		}
		published, err := w.publisher.PublishScheduled(ctx, c.PostID, nil, true)
		if err != nil {
			rep.Failed++
			w.log.Error("post schedule: publish failed", "post_id", c.PostID, "err", err)
			continue
		}
		if !published {
			rep.Skipped++
			continue
		}
		rep.Published++
		w.log.Info("post schedule: published", "event", "post_scheduled_published",
			"post_id", c.PostID, "author_id", c.AuthorID, "publish_at", c.PublishAt)
	}
	return rep
}
