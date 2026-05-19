// Package reconcile keeps the local app.users projection converged with the
// identity profile-service. It is the durable safety net behind the
// event-driven projection: a UserRegistered Kafka event that is lost or
// arrives garbled leaves app.users stale, and nothing else repairs it. This
// job re-pulls the identity source on a schedule and upserts the difference.
package reconcile

import (
	"context"
	"log/slog"
	"time"

	"github.com/atpost/user-service/internal/identityclient"
	"github.com/atpost/user-service/internal/service"
	"github.com/atpost/user-service/internal/store"
)

// checkpointName identifies the app.users←profile.profiles reconcile cursor.
const checkpointName = store.CheckpointAppUsers

const (
	incrementalInterval = 5 * time.Minute
	fullInterval        = 6 * time.Hour
	pageLimit           = 200
	maxPagesPerRun      = 10000 // safety bound (~2M profiles per run)
)

// Reconciler syncs app.users from the identity profile-service.
type Reconciler struct {
	store    *store.Store
	identity *identityclient.Client
}

// New builds a Reconciler. A nil identity client disables it (Start no-ops).
func New(st *store.Store, idc *identityclient.Client) *Reconciler {
	return &Reconciler{store: st, identity: idc}
}

// runFrom pages the profile-changes feed starting at `since`, upserting each
// profile into app.users, until a short page signals it has caught up.
// Returns how many profiles were synced and the new high-water cursor.
func (r *Reconciler) runFrom(ctx context.Context, since time.Time) (int, time.Time, error) {
	synced := 0
	cursor := since
	for page := 0; page < maxPagesPerRun; page++ {
		pg, err := r.identity.ListChangedProfiles(ctx, cursor, pageLimit)
		if err != nil {
			return synced, cursor, err
		}
		for i := range pg.Items {
			in := service.ProjectionInputFromProfile(&pg.Items[i])
			if err := r.store.UpsertUserProjection(ctx, in); err != nil {
				return synced, cursor, err
			}
			synced++
		}
		if pg.Count < pageLimit {
			return synced, maxTime(cursor, pg.NextSince), nil // caught up
		}
		prev := cursor
		cursor = maxTime(cursor, pg.NextSince)
		if !cursor.After(prev) {
			// A full page whose rows all share one timestamp — the cursor
			// cannot advance. Stop rather than hot-loop; the next full run
			// (or a wider page) will make progress.
			slog.Warn("projection reconcile: cursor stalled", "at", cursor)
			return synced, cursor, nil
		}
	}
	return synced, cursor, nil
}

// RunIncremental syncs profiles changed since the stored checkpoint.
func (r *Reconciler) RunIncremental(ctx context.Context) (int, error) {
	var since time.Time
	if cp, err := r.store.GetCheckpoint(ctx, checkpointName); err != nil {
		return 0, err
	} else if cp != nil {
		since = cp.LastSyncedAt
	}
	synced, cursor, runErr := r.runFrom(ctx, since)
	if runErr != nil {
		_ = r.store.SaveCheckpoint(ctx, checkpointName, since, false, runErr.Error())
		return synced, runErr
	}
	return synced, r.store.SaveCheckpoint(ctx, checkpointName, cursor, true, "")
}

// RunFull re-syncs every profile (since = epoch), catching anything an
// incremental pass missed. The upsert is idempotent, so repeats are safe.
func (r *Reconciler) RunFull(ctx context.Context) (int, error) {
	synced, cursor, runErr := r.runFrom(ctx, time.Time{})
	if runErr != nil {
		_ = r.store.SaveCheckpoint(ctx, checkpointName, time.Time{}, false, runErr.Error())
		return synced, runErr
	}
	return synced, r.store.SaveCheckpoint(ctx, checkpointName, cursor, true, "")
}

// Start runs an immediate full reconcile, then incremental every 5m and full
// every 6h, until ctx is cancelled. Intended to run in its own goroutine.
func (r *Reconciler) Start(ctx context.Context) {
	if r.identity == nil {
		slog.Warn("projection reconcile disabled — no identity client configured")
		return
	}
	if n, err := r.RunFull(ctx); err != nil {
		slog.Error("projection reconcile: initial full run failed", "error", err)
	} else {
		slog.Info("projection reconcile: initial full run complete", "synced", n)
	}

	inc := time.NewTicker(incrementalInterval)
	full := time.NewTicker(fullInterval)
	defer inc.Stop()
	defer full.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-inc.C:
			if n, err := r.RunIncremental(ctx); err != nil {
				slog.Error("projection reconcile: incremental run failed", "error", err)
			} else if n > 0 {
				slog.Info("projection reconcile: incremental run", "synced", n)
			}
		case <-full.C:
			if n, err := r.RunFull(ctx); err != nil {
				slog.Error("projection reconcile: full run failed", "error", err)
			} else {
				slog.Info("projection reconcile: full run", "synced", n)
			}
		}
	}
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
