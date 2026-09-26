package reconcile

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atpost/post-service/internal/store/postgres"
)

/*
	EngagementReconciler is the safety net under comment_count: the request
	path moves the count by ±1 per transition, and this recount rewrites
	any stored figure that has drifted from the ONE definition in
	store/postgres/comment_counts.go — replies included, held/hidden/removed
	excluded, and posts whose last visible comment is gone reset to zero.

	It runs once shortly after boot (so a deploy that changes the
	definition, like the one that introduced it, corrects every post
	without waiting an hour) and then hourly.
*/
type EngagementReconciler struct {
	store        *postgres.Store
	initialDelay time.Duration
	interval     time.Duration
}

func NewEngagementReconciler(db *pgxpool.Pool) *EngagementReconciler {
	return &EngagementReconciler{store: postgres.New(db), initialDelay: 20 * time.Second, interval: time.Hour}
}

func (r *EngagementReconciler) Start(ctx context.Context) {
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

// Reconcile runs one recount and logs how many rows it corrected.
func (r *EngagementReconciler) Reconcile(ctx context.Context) {
	corrected, err := r.store.RecountCommentCounts(ctx)
	if err != nil {
		slog.Error("reconcile: comment count recount failed", "error", err)
		return
	}
	slog.Info("reconcile: comment counts reconciled", "corrected_rows", corrected)
}
