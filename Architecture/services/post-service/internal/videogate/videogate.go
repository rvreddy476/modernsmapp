// Package videogate is the video publish gate's decision and its reconciler
// (reels-1, long-video readiness audit, 2026-11-17).
//
// A video post is created review_status='pending' while its media is still
// transcoding or being scanned, and the MediaTranscodeCompleted consumer
// (internal/consumers/media.go) flips it once a verdict exists. Three ways it
// used to stay pending for ever: media-service unreachable when the post was
// created, the completion consumed before the post's video_metadata row
// existed, and a flip that failed while the consumer committed the offset
// anyway. The consumer now retries; this package adds the backstop: a
// periodic sweep of video posts pending longer than a threshold that reads
// the media's state from media_assets (the table media-service writes, in the
// shared database) and applies the same rule.
//
// The rule (Decide), one place for the consumer and the sweep:
//
//	processing failed / rejected          -> rejected, media failed
//	ready + moderation rejected           -> rejected
//	ready + moderation passed / approved  -> approved
//	anything else (processing, manual_review, scanner failure, asset row
//	missing)                              -> still pending
//
// Every flip is FlipReviewStatusFromPending, a compare-and-set from
// 'pending', so the consumer, the sweep and a moderator can race safely and
// a repeat is a no-op.
package videogate

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Decide is the gate's rule. terminal=false means "still pending, wait".
// mediaFailed is set when the media itself failed, so the creator's video
// row is marked failed rather than left "pending".
func Decide(processingStatus, moderationStatus string) (decision string, terminal, mediaFailed bool) {
	switch {
	case processingStatus == "failed" || processingStatus == "rejected":
		return "rejected", true, true
	case processingStatus == "ready" && moderationStatus == "rejected":
		return "rejected", true, false
	case processingStatus == "ready" && (moderationStatus == "passed" || moderationStatus == "approved"):
		return "approved", true, false
	}
	return "", false, false
}

// Store is what the gate needs from Postgres; *postgres.Store implements it.
type Store interface {
	ListPendingVideoPosts(ctx context.Context, olderThan time.Time, after *postgres.PendingVideoCursor, limit int) ([]postgres.PendingVideoPost, error)
	GetPendingVideoPost(ctx context.Context, postID uuid.UUID) (*postgres.PendingVideoPost, error)
	FlipReviewStatusFromPending(ctx context.Context, postID uuid.UUID, newStatus string) (bool, error)
	MarkVideoMetadataFailed(ctx context.Context, postID uuid.UUID) error
	InsertModerationReview(ctx context.Context, r *postgres.ModerationReview) error
}

// Outcome is what Apply did to one post.
type Outcome int

const (
	StillPending Outcome = iota // no verdict yet
	Approved
	Rejected
	AlreadyDecided // someone else flipped it first
)

// Apply judges one pending post from its media state and flips it. Errors
// are returned, never swallowed: the caller retries (the consumer's retry
// policy, the next sweep). bust, when non-nil, drops the post's cached body.
func Apply(ctx context.Context, st Store, row postgres.PendingVideoPost, bust func(context.Context, uuid.UUID)) (Outcome, error) {
	decision, terminal, mediaFailed := Decide(row.ProcessingStatus, row.ModerationStatus)
	if !terminal {
		return StillPending, nil
	}
	return Flip(ctx, st, row.PostID, decision, mediaFailed, bust)
}

// Flip applies a decision already made. The media-failed mark comes first
// so a retry after a failed flip still records it; the audit row is
// best-effort (the flip has committed, and a retry would not re-flip).
func Flip(ctx context.Context, st Store, postID uuid.UUID, decision string, mediaFailed bool, bust func(context.Context, uuid.UUID)) (Outcome, error) {
	if mediaFailed {
		if err := st.MarkVideoMetadataFailed(ctx, postID); err != nil {
			return StillPending, err
		}
	}
	flipped, err := st.FlipReviewStatusFromPending(ctx, postID, decision)
	if err != nil {
		return StillPending, err
	}
	if !flipped {
		return AlreadyDecided, nil
	}
	confidence := 1.0
	if err := st.InsertModerationReview(ctx, &postgres.ModerationReview{
		ReelID: postID, ReviewerType: "auto", Decision: decision, Confidence: &confidence,
	}); err != nil {
		slog.Warn("video gate: moderation review insert failed", "post_id", postID, "error", err)
	}
	if bust != nil {
		bust(ctx, postID)
	}
	if decision == "approved" {
		return Approved, nil
	}
	return Rejected, nil
}

// String names an outcome for logs and metrics.
func (o Outcome) String() string {
	switch o {
	case Approved:
		return "approved"
	case Rejected:
		return "rejected"
	case AlreadyDecided:
		return "already_decided"
	}
	return "still_pending"
}

// ReconcileOne re-judges one post if it is a pending video post (the create
// path's immediate second look). Best-effort: an error is logged, the sweep
// is the backstop.
func ReconcileOne(ctx context.Context, st Store, postID uuid.UUID, bust func(context.Context, uuid.UUID)) Outcome {
	row, err := st.GetPendingVideoPost(ctx, postID)
	if err != nil || row == nil {
		if err != nil {
			slog.Warn("video gate: re-check after create failed", "post_id", postID, "error", err)
		}
		return StillPending
	}
	out, err := Apply(ctx, st, *row, bust)
	if err != nil {
		slog.Warn("video gate: re-check after create failed", "post_id", postID, "error", err)
	}
	return out
}

// ── the sweep ──────────────────────────────────────────────────────────────

var sweepCounter = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "post_video_gate_sweep_posts_total",
	Help: "Pending video posts the review-gate reconciler looked at, by outcome (approved, rejected, still_pending, already_decided, error).",
}, []string{"outcome"})

// oldestPendingGauge (lv-21): how long the oldest video post the sweep found
// still pending has been waiting since it was created, published once per
// full pass over the pending list (the list is in created_at order, so the
// first still-pending row of a pass is the oldest). 0 when none is left.
// Posts younger than MinAge are the consumer's and are not seen. Every
// replica sweeps the same rows: alert on max.
var oldestPendingGauge = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "post_video_gate_oldest_pending_seconds",
	Help: "Age of the oldest video post still pending its review gate at the end of the last full sweep pass; 0 when none.",
})

func init() {
	for _, o := range []string{"approved", "rejected", "still_pending", "already_decided", "error"} {
		sweepCounter.WithLabelValues(o)
	}
}

// Config is read from the environment by ConfigFromEnv.
type Config struct {
	Interval time.Duration // VIDEO_GATE_SWEEP_INTERVAL, default 1m
	MinAge   time.Duration // VIDEO_GATE_SWEEP_MIN_AGE, default 5m: younger posts are the consumer's
	Batch    int           // VIDEO_GATE_SWEEP_BATCH, default 100
}

// ConfigFromEnv reads the three settings; a bad value keeps the default.
func ConfigFromEnv() Config {
	cfg := Config{Interval: time.Minute, MinAge: 5 * time.Minute, Batch: 100}
	if d, err := time.ParseDuration(os.Getenv("VIDEO_GATE_SWEEP_INTERVAL")); err == nil && d > 0 {
		cfg.Interval = d
	}
	if d, err := time.ParseDuration(os.Getenv("VIDEO_GATE_SWEEP_MIN_AGE")); err == nil && d > 0 {
		cfg.MinAge = d
	}
	if n, err := strconv.Atoi(os.Getenv("VIDEO_GATE_SWEEP_BATCH")); err == nil && n > 0 {
		cfg.Batch = n
	}
	return cfg
}

// Sweeper is the periodic reconciler. One per replica is safe: every flip is
// a compare-and-set.
type Sweeper struct {
	store  Store
	cfg    Config
	bust   func(context.Context, uuid.UUID)
	now    func() time.Time
	cursor *postgres.PendingVideoCursor
	// oldest is the created_at of the first row of this pass still pending
	// after it was judged (zero: none yet); published at the pass's end.
	oldest time.Time
}

// NewSweeper builds a sweeper; bust may be nil.
func NewSweeper(st Store, cfg Config, bust func(context.Context, uuid.UUID)) *Sweeper {
	if cfg.Batch <= 0 {
		cfg.Batch = 100
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Minute
	}
	if cfg.MinAge <= 0 {
		cfg.MinAge = 5 * time.Minute
	}
	return &Sweeper{store: st, cfg: cfg, bust: bust, now: func() time.Time { return time.Now().UTC() }}
}

// WithClock overrides the time source (tests).
func (w *Sweeper) WithClock(now func() time.Time) *Sweeper { w.now = now; return w }

// Start ticks until ctx is cancelled.
func (w *Sweeper) Start(ctx context.Context) {
	slog.Info("video gate sweeper started", "interval", w.cfg.Interval, "min_age", w.cfg.MinAge)
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

// Report is what one Tick did.
type Report struct {
	Swept, Approved, Rejected, StillPending, AlreadyDecided, Failed int
}

// Tick judges one bounded batch, continuing after the last post the previous
// tick saw and wrapping to the start when the list runs out.
func (w *Sweeper) Tick(ctx context.Context) Report {
	var rep Report
	if w.cursor == nil {
		w.oldest = time.Time{} // a new pass over the pending list
	}
	rows, err := w.store.ListPendingVideoPosts(ctx, w.now().Add(-w.cfg.MinAge), w.cursor, w.cfg.Batch)
	if err != nil {
		slog.Error("video gate sweep: list failed", "error", err)
		sweepCounter.WithLabelValues("error").Inc()
		return rep
	}
	passEnds := len(rows) < w.cfg.Batch
	defer func() {
		if passEnds && ctx.Err() == nil {
			w.publishOldestPending()
		}
	}()
	if len(rows) < w.cfg.Batch {
		w.cursor = nil // reached the end: the next tick starts over
	} else {
		last := rows[len(rows)-1]
		w.cursor = &postgres.PendingVideoCursor{CreatedAt: last.CreatedAt, PostID: last.PostID}
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return rep
		}
		rep.Swept++
		out, err := Apply(ctx, w.store, row, w.bust)
		if err != nil {
			rep.Failed++
			sweepCounter.WithLabelValues("error").Inc()
			w.notePending(row.CreatedAt)
			slog.Error("video gate sweep: apply failed", "post_id", row.PostID, "error", err)
			continue
		}
		switch out {
		case Approved:
			rep.Approved++
			sweepCounter.WithLabelValues("approved").Inc()
		case Rejected:
			rep.Rejected++
			sweepCounter.WithLabelValues("rejected").Inc()
		case AlreadyDecided:
			rep.AlreadyDecided++
			sweepCounter.WithLabelValues("already_decided").Inc()
		default:
			rep.StillPending++
			sweepCounter.WithLabelValues("still_pending").Inc()
			w.notePending(row.CreatedAt)
		}
		if out == Approved || out == Rejected {
			slog.Info("video gate sweep: pending post resolved", "post_id", row.PostID,
				"media_id", row.MediaID, "outcome", out, "processing_status", row.ProcessingStatus,
				"moderation_status", row.ModerationStatus)
		}
	}
	return rep
}

// notePending records a row left pending; rows come oldest first, so the
// first one of a pass is the oldest.
func (w *Sweeper) notePending(createdAt time.Time) {
	if w.oldest.IsZero() {
		w.oldest = createdAt
	}
}

// publishOldestPending sets the gauge at the end of a pass.
func (w *Sweeper) publishOldestPending() {
	if w.oldest.IsZero() {
		oldestPendingGauge.Set(0)
		return
	}
	age := w.now().Sub(w.oldest).Seconds()
	if age < 0 {
		age = 0
	}
	oldestPendingGauge.Set(age)
}
