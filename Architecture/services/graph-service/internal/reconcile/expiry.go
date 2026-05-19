package reconcile

import (
	"context"
	"log/slog"
	"time"

	"github.com/atpost/graph-service/internal/store"
)

// ConnectionRequestSweeper periodically flips pending connection requests
// that have passed their 30-day expires_at to the 'expired' status
// (messaging/privacy spec v2 §8.3).
type ConnectionRequestSweeper struct {
	store *store.Store
}

func NewConnectionRequestSweeper(s *store.Store) *ConnectionRequestSweeper {
	return &ConnectionRequestSweeper{store: s}
}

// Start runs the sweeper hourly until ctx is cancelled (call in a goroutine).
func (r *ConnectionRequestSweeper) Start(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := r.store.ExpireStaleConnectionRequests(ctx)
			if err != nil {
				slog.Error("sweeper: connection-request expiry failed", "error", err)
				continue
			}
			if n > 0 {
				slog.Info("sweeper: expired stale connection requests", "count", n)
			}
		}
	}
}
