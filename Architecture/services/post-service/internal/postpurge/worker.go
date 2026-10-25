// Package postpurge is the HARD half of post deletion (founder decision,
// 2026-09-04: soft first, hard later).
//
// DELETE /v1/posts/{id} only sets posts.deleted_at. This worker runs every
// Interval and, for every post whose deleted_at is older than After:
//
//  1. erases the post's rows in ONE transaction (store.PurgePost), which
//     also queues each media asset the post was the LAST post to reference
//     into post_purge_media and emits PostPurged on the outbox;
//  2. drains post_purge_media by asking media-service to delete each asset
//     (DELETE /v1/media/internal/{id} with {"referrer":"post",...});
//     media-service re-checks its own references and deletes the row plus
//     every object under the asset's key prefix.
//
// Step 2 is durable: a media-service failure defers the queue row with
// backoff and the next tick retries it. Both steps are idempotent and safe
// to run in every replica (PurgePost locks the row; a post that is already
// gone or was restored is skipped).
//
// Two kinds of overdue post are not purged (migration 065):
//
//   - a post under an active case restriction (a copyright hold) is
//     evidence. The scan skips it and PurgePost refuses it with
//     postgres.ErrPurgeHeld; the worker counts them every tick (gauge
//     post_purge_held_posts, a log line when the number changes). The tick
//     after the case releases its hold purges the post as usual;
//   - a post whose purge transaction failed backs off in
//     post_purge_failures (2^attempts minutes, capped at a day) instead of
//     failing again on every tick.
package postpurge

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	heldGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "atpost", Subsystem: "post_service",
		Name: "post_purge_held_posts",
		Help: "Soft-deleted posts past the purge window that are not purged because a case holds them.",
	})
	failedCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "atpost", Subsystem: "post_service",
		Name: "post_purge_failures_total",
		Help: "Post purge transactions that failed and were backed off.",
	})
)

// Store is what the worker needs from Postgres. *postgres.Store implements
// it; tests substitute a fake.
type Store interface {
	ListPurgeablePosts(ctx context.Context, before time.Time, limit int) ([]postgres.PurgeCandidate, error)
	CountHeldPurgeablePosts(ctx context.Context, before time.Time) (int, error)
	PurgePost(ctx context.Context, postID uuid.UUID) ([]postgres.PurgeMediaItem, error)
	DeferPurgePost(ctx context.Context, postID uuid.UUID, reason string) (int, time.Time, error)
	PendingPurgeMedia(ctx context.Context, limit int) ([]postgres.PurgeMediaItem, error)
	ResolvePurgeMedia(ctx context.Context, mediaID, postID uuid.UUID) error
	DeferPurgeMedia(ctx context.Context, mediaID, postID uuid.UUID, reason string) error
}

// MediaDeleter asks media-service to delete one asset on behalf of the
// purged post. It must return nil when the asset is already gone or when
// media-service refused because another referrer still holds it — both
// mean the queue row is done. *service.Service implements it.
type MediaDeleter interface {
	DeleteMediaForPurgedPost(ctx context.Context, mediaID, postID uuid.UUID) error
}

// ClaimReleaser releases the media-service claims a purged post held (its
// cover, kind post_cover). Optional: a MediaDeleter that also implements it
// (*service.Service does) is asked after every purge. Best effort here —
// DeleteMediaForPurgedPost repeats it durably for every queued asset, and
// cmd/media-claims repairs a cover the queue does not carry (one another
// post still uses).
type ClaimReleaser interface {
	ReleasePurgedPostClaims(ctx context.Context, postID uuid.UUID) error
}

// releaseClaims asks the deleter to release the purged post's claims.
func (w *Worker) releaseClaims(ctx context.Context, postID uuid.UUID) {
	r, ok := w.media.(ClaimReleaser)
	if !ok {
		return
	}
	if err := r.ReleasePurgedPostClaims(ctx, postID); err != nil {
		w.log.Warn("post purge: media claims not released; run media-claims", "post_id", postID, "err", err)
	}
}

// Config is read from the environment by ConfigFromEnv.
type Config struct {
	// After is the "Recently deleted" window (POST_PURGE_AFTER, default
	// 720h). The dev compose sets 2m so the whole lifecycle is testable.
	After time.Duration
	// Interval is the tick (POST_PURGE_INTERVAL, default 5m).
	Interval time.Duration
	// Batch bounds one tick's work (POST_PURGE_BATCH, default 100).
	Batch int
}

// ConfigFromEnv reads POST_PURGE_AFTER / POST_PURGE_INTERVAL / POST_PURGE_BATCH.
func ConfigFromEnv() Config {
	cfg := Config{After: 720 * time.Hour, Interval: 5 * time.Minute, Batch: 100}
	if v := os.Getenv("POST_PURGE_AFTER"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.After = d
		}
	}
	if v := os.Getenv("POST_PURGE_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.Interval = d
		}
	}
	return cfg
}

// Worker runs the purge loop.
type Worker struct {
	store Store
	media MediaDeleter
	cfg   Config
	now   func() time.Time
	log   *slog.Logger
	// lastHeld is the held count last logged (-1: never), so the log line
	// fires when the number changes, not on every tick.
	lastHeld int
}

// NewWorker builds a worker. log may be nil.
func NewWorker(store Store, media MediaDeleter, cfg Config, log *slog.Logger) *Worker {
	if log == nil {
		log = slog.Default()
	}
	if cfg.Batch <= 0 {
		cfg.Batch = 100
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Minute
	}
	if cfg.After <= 0 {
		cfg.After = 720 * time.Hour
	}
	return &Worker{store: store, media: media, cfg: cfg, now: func() time.Time { return time.Now().UTC() }, log: log, lastHeld: -1}
}

// reportHeld counts the overdue posts a case holds, sets the gauge, and logs
// when the number changes.
func (w *Worker) reportHeld(ctx context.Context, cutoff time.Time, rep *TickReport) {
	held, err := w.store.CountHeldPurgeablePosts(ctx, cutoff)
	if err != nil {
		w.log.Error("post purge: held count failed", "err", err)
		return
	}
	rep.Held = held
	heldGauge.Set(float64(held))
	switch {
	case held == w.lastHeld:
	case held > 0:
		w.log.Warn("post purge: overdue posts held by a case are not purged", "event", "post_purge_held",
			"held", held)
	case w.lastHeld > 0:
		w.log.Info("post purge: no overdue post is held any more", "event", "post_purge_held", "held", 0)
	}
	w.lastHeld = held
}

// WithClock overrides the time source (tests).
func (w *Worker) WithClock(now func() time.Time) *Worker { w.now = now; return w }

// Start ticks until ctx is cancelled. One tick runs immediately so a
// restarted pod does not wait a full interval to resume a backlog.
func (w *Worker) Start(ctx context.Context) {
	w.log.Info("post purge worker started", "after", w.cfg.After, "interval", w.cfg.Interval)
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
	Purged int
	// Held is the number of overdue posts a case holds (skipped, not failed).
	Held int
	// Failed is the number of purges that failed this tick and were backed off.
	Failed        int
	MediaQueued   int
	MediaDeleted  int
	MediaDeferred int
}

// Tick purges every due post, then drains the media queue.
func (w *Worker) Tick(ctx context.Context) TickReport {
	var rep TickReport
	cutoff := w.now().Add(-w.cfg.After)
	w.reportHeld(ctx, cutoff, &rep)
	cands, err := w.store.ListPurgeablePosts(ctx, cutoff, w.cfg.Batch)
	if err != nil {
		w.log.Error("post purge: list failed", "err", err)
	}
	for _, c := range cands {
		if ctx.Err() != nil {
			return rep
		}
		queued, err := w.store.PurgePost(ctx, c.PostID)
		if errors.Is(err, postgres.ErrPurgeHeld) {
			// A hold placed after the scan. Skipped like the ones the scan
			// left out; the next tick's count includes it.
			w.log.Info("post purge: skipped, held by a case", "post_id", c.PostID)
			continue
		}
		if err != nil {
			rep.Failed++
			failedCounter.Inc()
			attempts, next, derr := w.store.DeferPurgePost(ctx, c.PostID, err.Error())
			if derr != nil {
				w.log.Error("post purge: purge failed, and recording the failure failed too",
					"post_id", c.PostID, "err", err, "defer_err", derr)
				continue
			}
			w.log.Error("post purge: purge failed, backing off", "post_id", c.PostID,
				"attempt", attempts, "next_attempt_at", next, "err", err)
			continue
		}
		rep.Purged++
		rep.MediaQueued += len(queued)
		w.releaseClaims(ctx, c.PostID)
		w.log.Info("post purged", "event", "post_purged", "post_id", c.PostID, "author_id", c.AuthorID,
			"deleted_at", c.DeletedAt, "media_queued", len(queued))
	}

	items, err := w.store.PendingPurgeMedia(ctx, w.cfg.Batch)
	if err != nil {
		w.log.Error("post purge: media queue list failed", "err", err)
		return rep
	}
	for _, it := range items {
		if ctx.Err() != nil {
			return rep
		}
		if err := w.media.DeleteMediaForPurgedPost(ctx, it.MediaID, it.PostID); err != nil {
			rep.MediaDeferred++
			w.log.Warn("post purge: media delete deferred", "media_id", it.MediaID, "post_id", it.PostID,
				"attempt", it.Attempts+1, "err", err)
			if derr := w.store.DeferPurgeMedia(ctx, it.MediaID, it.PostID, err.Error()); derr != nil {
				w.log.Error("post purge: defer failed", "media_id", it.MediaID, "err", derr)
			}
			continue
		}
		if err := w.store.ResolvePurgeMedia(ctx, it.MediaID, it.PostID); err != nil {
			w.log.Error("post purge: resolve failed", "media_id", it.MediaID, "err", err)
			continue
		}
		rep.MediaDeleted++
		w.log.Info("post purge: media released", "event", "post_media_purged",
			"media_id", it.MediaID, "post_id", it.PostID, "owner_id", it.OwnerID)
	}
	return rep
}
