package reconcile

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// EngagementReconciler corrects post engagement count denormalization drift.
type EngagementReconciler struct {
	db *pgxpool.Pool
}

func NewEngagementReconciler(db *pgxpool.Pool) *EngagementReconciler {
	return &EngagementReconciler{db: db}
}

func (r *EngagementReconciler) Start(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.reconcile(ctx)
		}
	}
}

func (r *EngagementReconciler) reconcile(ctx context.Context) {
	_, err := r.db.Exec(ctx, `
		UPDATE post_engagement_counts ec
		SET comment_count = actual.cnt,
		    updated_at = NOW()
		FROM (
		    SELECT post_id, COUNT(*) AS cnt
		    FROM comments
		    WHERE is_deleted = FALSE
		    GROUP BY post_id
		) actual
		WHERE ec.post_id = actual.post_id
		  AND ec.comment_count != actual.cnt
	`)
	if err != nil {
		slog.Error("reconcile: comment count update failed", "error", err)
	}
	slog.Info("reconcile: post engagement counts reconciliation complete")
}
