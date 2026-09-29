package reconcile

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atpost/post-service/internal/store/postgres"
)

// RestrictionCountReconciler is the nightly self-check of
// posts.active_restriction_count against the active post_restrictions rows
// (Copyright Match plan, section 9.5, "Restriction count"). The command
// path recounts inside its transaction and a deferred constraint trigger
// refuses a drifting commit, so a non-empty repair here is an alert, not
// routine: it is logged at error level with the post ids.
type RestrictionCountReconciler struct {
	store        *postgres.Store
	initialDelay time.Duration
	interval     time.Duration
	// invalidate is asked for every repaired post (the body cache).
	invalidate func(ctx context.Context, id string)
}

func NewRestrictionCountReconciler(db *pgxpool.Pool, invalidate func(ctx context.Context, id string)) *RestrictionCountReconciler {
	return &RestrictionCountReconciler{store: postgres.New(db), initialDelay: 5 * time.Minute, interval: 24 * time.Hour, invalidate: invalidate}
}

func (r *RestrictionCountReconciler) Start(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(r.initialDelay):
		r.Reconcile(ctx)
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.Reconcile(ctx)
		}
	}
}

// Reconcile runs one check and repair.
func (r *RestrictionCountReconciler) Reconcile(ctx context.Context) {
	repaired, err := r.store.RepairRestrictionCounts(ctx)
	for _, id := range repaired {
		if r.invalidate != nil {
			r.invalidate(ctx, id.String())
		}
	}
	if err != nil {
		slog.Error("reconcile: restriction count repair failed", "error", err, "repaired_before_failure", len(repaired))
		return
	}
	if len(repaired) > 0 {
		slog.Error("reconcile: restriction counts had drifted and were repaired", "posts", repaired)
		return
	}
	slog.Info("reconcile: restriction counts consistent")
}
