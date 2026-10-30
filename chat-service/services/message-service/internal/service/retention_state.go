package service

import (
	"context"
	"time"
)

// State retention (audit M1, M8; 30 Oct 2026): idempotency keys were never
// deleted, and completed delivery intents kept their full member list
// forever.

type stateRetentionStore interface {
	PurgeExpiredIdempotencyKeys(ctx context.Context, before time.Time, limit int) (int64, error)
	CompactCompletedDeliveryIntents(ctx context.Context, before time.Time, limit int) (int64, error)
}

// StateRetention configures StartStateRetentionWorker.
type StateRetention struct {
	// IntentMemberKeep is how long a completed delivery intent keeps its
	// member list (default 7 days).
	IntentMemberKeep time.Duration
	// Interval between passes (default 15 minutes).
	Interval time.Duration
	// Batch is the most rows one statement touches (default 1000); a pass
	// repeats batches until one comes back short or MaxBatches ran.
	Batch int
	// MaxBatches bounds one pass (default 100), so the first pass over an
	// old backlog is spread over several intervals instead of one long burst.
	MaxBatches int
}

func (r StateRetention) withDefaults() StateRetention {
	if r.IntentMemberKeep <= 0 {
		r.IntentMemberKeep = 7 * 24 * time.Hour
	}
	if r.Interval <= 0 {
		r.Interval = 15 * time.Minute
	}
	if r.Batch <= 0 {
		r.Batch = 1000
	}
	if r.MaxBatches <= 0 {
		r.MaxBatches = 100
	}
	return r
}

// StartStateRetentionWorker purges expired idempotency keys and compacts
// old completed delivery intents, once at start and then every cfg.Interval.
// Safe with several replicas: both statements skip rows another replica has
// locked and are idempotent.
func (s *Service) StartStateRetentionWorker(ctx context.Context, cfg StateRetention) {
	cfg = cfg.withDefaults()
	st, ok := s.convStore.(stateRetentionStore)
	if !ok {
		return
	}
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		s.runStateRetention(ctx, st, cfg, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) runStateRetention(ctx context.Context, st stateRetentionStore, cfg StateRetention, now time.Time) {
	keys, err := drainBatches(ctx, cfg.Batch, cfg.MaxBatches, func() (int64, error) {
		return st.PurgeExpiredIdempotencyKeys(ctx, now, cfg.Batch)
	})
	if err != nil {
		s.log.Error("idempotency key purge failed", "err", err, "deleted", keys)
	} else if keys > 0 {
		s.log.Info("idempotency key purge", "deleted", keys)
	}
	intents, err := drainBatches(ctx, cfg.Batch, cfg.MaxBatches, func() (int64, error) {
		return st.CompactCompletedDeliveryIntents(ctx, now.Add(-cfg.IntentMemberKeep), cfg.Batch)
	})
	if err != nil {
		s.log.Error("delivery intent compaction failed", "err", err, "compacted", intents)
	} else if intents > 0 {
		s.log.Info("delivery intent compaction", "compacted", intents)
	}
}

// drainBatches repeats one batch until it comes back short, an error,
// maxBatches ran, or ctx ends, and returns the total.
func drainBatches(ctx context.Context, batch, maxBatches int, run func() (int64, error)) (int64, error) {
	var total int64
	for i := 0; i < maxBatches && ctx.Err() == nil; i++ {
		n, err := run()
		total += n
		if err != nil {
			return total, err
		}
		if n < int64(batch) {
			break
		}
	}
	return total, nil
}
