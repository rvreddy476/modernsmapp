package service

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/atpost/feed-service/internal/store/scylla"
)

// echo_notes pruning (migration 010, 2026-11-13).
//
// echo_notes grows by one row per note forever unless something removes it.
// A row is no longer useful once
//
//   - its note is older than the timeline retention (360 days): no
//     notes_timeline row holds it, Following's pull reads are bounded by the
//     same window, and For You only looks back days; or
//   - its note has been deleted for longer than post-service's restore
//     window (POST_PURGE_AFTER, 30 days): the purge worker has hard-deleted
//     the post, so no PostRestored can ever need the row again.
//
// The pruner deletes such rows in bounded batches on a timer. Every batch is
// an idempotent DELETE, so running it in every replica at once is safe:
// replicas only share the work.

const (
	// defaultEchoPruneInterval is how often a pass runs.
	defaultEchoPruneInterval = time.Hour
	// defaultEchoPruneBatch bounds one DELETE.
	defaultEchoPruneBatch = 1000
	// defaultEchoPruneMaxBatches bounds one pass, so a large backlog is
	// worked off over several passes instead of one long one.
	defaultEchoPruneMaxBatches = 50
	// defaultEchoDeletedRetention is post-service's 30-day restore window
	// plus a day, so a restore at the window's last minute still finds
	// its row.
	defaultEchoDeletedRetention = 31 * 24 * time.Hour
)

// echoNotesPruneStore is the MetaStore slice the pruner uses.
type echoNotesPruneStore interface {
	PruneEchoNotes(ctx context.Context, createdBefore, deletedBefore time.Time, batch int) (int64, error)
}

// EchoNotesPruner removes echo_notes rows past their use (see above).
type EchoNotesPruner struct {
	store            echoNotesPruneStore
	interval         time.Duration
	batch            int
	maxBatches       int
	retention        time.Duration // created_at older than this goes
	deletedRetention time.Duration // deleted longer than this goes
	now              func() time.Time
}

// NewEchoNotesPruner returns a pruner with the defaults;
// ECHO_NOTES_PRUNE_INTERVAL and ECHO_NOTES_DELETED_RETENTION (Go durations)
// override the interval and the deleted-row window.
func NewEchoNotesPruner(store echoNotesPruneStore) *EchoNotesPruner {
	p := &EchoNotesPruner{
		store: store, interval: defaultEchoPruneInterval, batch: defaultEchoPruneBatch,
		maxBatches: defaultEchoPruneMaxBatches, retention: scylla.TimelineRetention,
		deletedRetention: defaultEchoDeletedRetention, now: time.Now,
	}
	if d, err := time.ParseDuration(os.Getenv("ECHO_NOTES_PRUNE_INTERVAL")); err == nil && d > 0 {
		p.interval = d
	}
	if d, err := time.ParseDuration(os.Getenv("ECHO_NOTES_DELETED_RETENTION")); err == nil && d >= 24*time.Hour {
		p.deletedRetention = d // never under a day: a restore must find its row
	}
	return p
}

// Start runs a pass now and then every interval until ctx ends.
func (p *EchoNotesPruner) Start(ctx context.Context) {
	if p.store == nil {
		return
	}
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		if n, err := p.RunOnce(ctx); err != nil {
			log.Printf("echo: echo_notes prune stopped after %d row(s): %v", n, err)
		} else if n > 0 {
			log.Printf("echo: echo_notes pruned %d row(s)", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RunOnce is one pass: batches until one comes back short, maxBatches is
// reached, the store fails or ctx ends. Returns the rows deleted.
func (p *EchoNotesPruner) RunOnce(ctx context.Context) (int64, error) {
	now := p.now()
	createdBefore := now.Add(-p.retention)
	deletedBefore := now.Add(-p.deletedRetention)
	var total int64
	for i := 0; i < p.maxBatches; i++ {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := p.store.PruneEchoNotes(ctx, createdBefore, deletedBefore, p.batch)
		if err != nil {
			return total, err
		}
		total += n
		if n < int64(p.batch) {
			return total, nil
		}
	}
	return total, nil
}
