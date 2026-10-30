package service

import (
	"context"
	"log/slog"
	"time"
)

// SessionPurger deletes dead sessions in bounded batches (store.PurgeDeadSessions).
type SessionPurger interface {
	PurgeDeadSessions(ctx context.Context, retention time.Duration, limit int) (int64, error)
}

// SessionJanitor removes auth.sessions rows that expired or were revoked more
// than Retention ago (M2). The table gained one row per login and was never
// cleaned. Each tick deletes at most MaxBatches*BatchSize rows, so a large
// backlog drains over several ticks rather than in one long-running DELETE.
type SessionJanitor struct {
	store      SessionPurger
	log        *slog.Logger
	Interval   time.Duration
	Retention  time.Duration
	BatchSize  int
	MaxBatches int
}

// Defaults: hourly, keep dead rows 30 days (the security screen and data
// export can still show recent sign-outs), 1000 rows per statement, at most
// 20 statements per tick.
func NewSessionJanitor(store SessionPurger, log *slog.Logger) *SessionJanitor {
	if log == nil {
		log = slog.Default()
	}
	return &SessionJanitor{
		store: store, log: log,
		Interval: time.Hour, Retention: 30 * 24 * time.Hour,
		BatchSize: 1000, MaxBatches: 20,
	}
}

// Start runs a sweep every Interval until ctx ends.
func (j *SessionJanitor) Start(ctx context.Context) {
	t := time.NewTicker(j.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			j.Sweep(ctx)
		}
	}
}

// Sweep deletes dead sessions until a batch comes back short or MaxBatches
// is reached. Returns the number deleted.
func (j *SessionJanitor) Sweep(ctx context.Context) int64 {
	var total int64
	for i := 0; i < j.MaxBatches; i++ {
		if ctx.Err() != nil {
			break
		}
		n, err := j.store.PurgeDeadSessions(ctx, j.Retention, j.BatchSize)
		if err != nil {
			j.log.Warn("session janitor: purge failed", "err", err)
			break
		}
		total += n
		if n < int64(j.BatchSize) {
			break
		}
	}
	if total > 0 {
		j.log.Info("session janitor: dead sessions purged", "rows", total)
	}
	return total
}
