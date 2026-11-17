package purge

import (
	"context"

	"github.com/atpost/analytics-service/internal/model"
	"github.com/google/uuid"
)

// analytics-service's erase for user.purge_requested (reels-8). Three
// stores, in this order:
//
//  1. Scylla watch_sessions and viewer_history. The watch_sessions rows are
//     found through the keys PostgreSQL holds, so this runs while those
//     keys still exist.
//  2. PostgreSQL: every PurgeDelete row in postgres.PurgePlan.
//  3. Redis: the viewer's personalization signal keys, after the rows the
//     warmer rebuilds them from are gone.
//
// Each step is idempotent and the first error stops the erase, so the
// handler sends no ack and the consumer redelivers: a retry re-reads the
// keys PostgreSQL still holds and finishes the job.

// sessionKeyPage bounds one page of watch-session keys.
const sessionKeyPage = 1000

// PGStore is the PostgreSQL slice (postgres.Store).
type PGStore interface {
	WatchSessionKeys(ctx context.Context, userID uuid.UUID, after model.WatchSessionKey, limit int) ([]model.WatchSessionKey, error)
	PurgeUser(ctx context.Context, userID uuid.UUID) error
}

// WatchStore is the Scylla slice (scylla.WatchStore).
type WatchStore interface {
	DeleteWatchSessions(ctx context.Context, keys []model.WatchSessionKey) error
	DeleteViewerHistory(ctx context.Context, viewerID uuid.UUID) error
}

// SignalStore is the Redis slice (personalization.Warmer).
type SignalStore interface {
	DeleteViewerSignals(ctx context.Context, userID uuid.UUID) error
}

// StoreEraser satisfies Eraser.
type StoreEraser struct {
	pg      PGStore
	watch   WatchStore
	signals SignalStore
}

// NewEraser builds the composite eraser. pg is required; watch and signals
// may be nil when the service runs without Scylla or Redis (nothing was
// written there). Pass an untyped nil, never a typed nil pointer.
func NewEraser(pg PGStore, watch WatchStore, signals SignalStore) *StoreEraser {
	return &StoreEraser{pg: pg, watch: watch, signals: signals}
}

// PurgeUser implements Eraser.
func (e *StoreEraser) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	if e.watch != nil {
		after := model.WatchSessionKey{}
		for {
			keys, err := e.pg.WatchSessionKeys(ctx, userID, after, sessionKeyPage)
			if err != nil {
				return err
			}
			if len(keys) > 0 {
				if err := e.watch.DeleteWatchSessions(ctx, keys); err != nil {
					return err
				}
				after = keys[len(keys)-1]
			}
			if len(keys) < sessionKeyPage {
				break
			}
		}
		if err := e.watch.DeleteViewerHistory(ctx, userID); err != nil {
			return err
		}
	}
	if err := e.pg.PurgeUser(ctx, userID); err != nil {
		return err
	}
	if e.signals != nil {
		if err := e.signals.DeleteViewerSignals(ctx, userID); err != nil {
			return err
		}
	}
	return nil
}
