package service

import (
	"context"
	"time"
)

// Retention (field encryption at rest, 22 Oct 2026): published outbox rows
// are a copy of every message's text and preview, and were never deleted.

// outboxPruner is postgres.ConversationStore.PruneOutboxEvents.
type outboxPruner interface {
	PruneOutboxEvents(ctx context.Context, publishedBefore time.Time, limit int) (int64, error)
}

// OutboxRetention configures StartOutboxRetentionWorker.
type OutboxRetention struct {
	// Keep is how long a published row stays (default 7 days).
	Keep time.Duration
	// Interval between passes (default 1 hour).
	Interval time.Duration
	// Batch is the most rows one DELETE removes (default 1000); a pass
	// repeats batches until one comes back short.
	Batch int
}

func (r OutboxRetention) withDefaults() OutboxRetention {
	if r.Keep <= 0 {
		r.Keep = 7 * 24 * time.Hour
	}
	if r.Interval <= 0 {
		r.Interval = time.Hour
	}
	if r.Batch <= 0 {
		r.Batch = 1000
	}
	return r
}

// StartOutboxRetentionWorker deletes outbox rows published longer ago than
// cfg.Keep, once per cfg.Interval and once at start. Safe with several
// replicas: the DELETEs are idempotent.
func (s *Service) StartOutboxRetentionWorker(ctx context.Context, cfg OutboxRetention) {
	cfg = cfg.withDefaults()
	pruner, ok := s.convStore.(outboxPruner)
	if !ok {
		return
	}
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		if n, err := s.pruneOutbox(ctx, pruner, cfg, time.Now()); err != nil {
			s.log.Error("outbox retention pass failed", "err", err, "deleted", n)
		} else if n > 0 {
			s.log.Info("outbox retention pass", "deleted", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) pruneOutbox(ctx context.Context, pruner outboxPruner, cfg OutboxRetention, now time.Time) (int64, error) {
	cutoff := now.Add(-cfg.Keep)
	var total int64
	for ctx.Err() == nil {
		n, err := pruner.PruneOutboxEvents(ctx, cutoff, cfg.Batch)
		total += n
		if err != nil {
			return total, err
		}
		if n < int64(cfg.Batch) {
			break
		}
	}
	return total, nil
}
